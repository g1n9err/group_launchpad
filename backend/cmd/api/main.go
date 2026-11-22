// cmd/api — composition root. The ONLY file that knows how everything
// connects: config → pool → store → core services → transport → serve.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"launchpad/internal/config"
	"launchpad/internal/core/auth"
	"launchpad/internal/core/groups"
	"launchpad/internal/core/integrations"
	"launchpad/internal/dbmigrate"
	"launchpad/internal/integrations/google"
	mailgw "launchpad/internal/integrations/mail"
	slackgw "launchpad/internal/integrations/slack"
	"launchpad/internal/jobs"
	"launchpad/internal/secrets"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	// 1. Configuration — fail fast on a broken environment.
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel)

	// 2. Database pool — retry briefly so `docker compose up` races don't kill boots.
	pool, err := connectWithRetry(cfg.DatabaseURL, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	// 3. Migrations (dev convenience; production pipelines run cmd/migrate first).
	if cfg.MigrateOnBoot {
		if err := dbmigrate.Up(cfg.DatabaseURL); err != nil {
			return fmt.Errorf("migrate on boot: %w", err)
		}
		logger.Info("migrations up to date")
	}

	// 4. Wire the layers: store implements core ports, transport consumes core.
	repo := store.New(pool)
	groupsSvc := groups.New(repo)
	googleSvc := google.NewAuthService(cfg, repo, logger)
	slackSvc, slackChannelsSvc := newSlackServices(cfg, repo)
	var slackCipher *secrets.Cipher
	if cfg.TokenEncryptionKey != "" {
		slackCipher, err = secrets.New(cfg.TokenEncryptionKey)
		if err != nil {
			return err
		}
	}
	var fallbackSlack integrations.SlackGateway
	if cfg.SlackConfigured() {
		fallbackSlack = slackgw.NewGateway(cfg.SlackBotToken)
	}
	slackResolver := slackgw.NewWorkspaceResolver(repo, slackCipher, fallbackSlack)
	var slackOAuthGateway integrations.SlackOAuthGateway
	if cfg.SlackOAuthConfigured() {
		slackOAuthGateway = slackgw.NewOAuthGateway(cfg.SlackOAuthClientID, cfg.SlackOAuthClientSecret, cfg.SlackOAuthRedirectURI)
	}
	if cfg.SlackConfigured() || cfg.SlackOAuthConfigured() {
		slackChannelsSvc.SetGatewayResolver(slackResolver)
	}
	slackOAuthSvc := integrations.NewSlackOAuthService(repo, slackOAuthGateway, slackCipher, cfg.SlackOAuthConfigured())
	slackEventsSvc := newSlackEventsService(cfg, repo, logger)
	if cfg.SlackConfigured() || cfg.SlackOAuthConfigured() {
		slackEventsSvc.SetTeamGatewayResolver(slackResolver, repo)
	}
	queue, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return fmt.Errorf("initialize River insert client: %w", err)
	}
	launch := &jobs.SlackLaunchService{Queue: repo, River: queue}
	classroom := &integrations.ClassroomService{Tokens: googleSvc, Gateway: google.NewClassroomGateway(), Store: repo, Operations: repo}
	if cfg.SMTPEnabled || cfg.GmailEnabled {
		classroom.QueueJoinEmails = jobs.QueueClassroomJoinEmails(repo, queue)
	}
	drive := &integrations.DriveService{Tokens: googleSvc, Gateway: google.NewDriveGateway(), Store: repo, Operations: repo}
	forms := &integrations.FormsService{Tokens: googleSvc, GroupTokens: googleSvc, Gateway: google.NewFormsGateway(), Store: repo, Operations: repo}
	announcements := &jobs.AnnouncementService{Store: repo, River: queue}
	audit := &jobs.AuditService{Store: repo, River: queue}
	delivery := &jobs.IntakeDeliveryService{Queue: repo, River: queue}
	if cfg.GmailEnabled {
		delivery.CheckReady = googleSvc.CheckGmailPermission
	}
	// Unified multi-platform launcher (River queue).
	// ResultStore ensures stale results are overwritten with "running"
	// before the job is queued, so the UI always sees the current state.
	launcher := &jobs.RiverLauncher{Client: queue, ResultStore: repo}

	// Multi-role authentication (PRD §3, §7.4). The bootstrap admin is
	// created only when no admin exists and both values are configured.
	authSvc := auth.NewService(repo, cfg.SessionTTLOrDefault())
	bootstrapEmail, bootstrapPassword := os.Getenv("ADMIN_EMAIL"), os.Getenv("ADMIN_INITIAL_PASSWORD")
	if created, err := authSvc.BootstrapAdmin(context.Background(), bootstrapEmail, bootstrapPassword); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	} else if created {
		logger.Info("bootstrap admin account created", "email", bootstrapEmail)
	}

	var passwordRecovery *transport.PasswordRecovery
	if siteURL := os.Getenv("PASSWORD_RESET_SITE_URL"); siteURL != "" {
		site, parseErr := url.Parse(siteURL)
		if parseErr != nil || site.Scheme != "https" || site.Hostname() == "" || site.User != nil || site.Fragment != "" || site.RawQuery != "" {
			return errors.New("PASSWORD_RESET_SITE_URL must be the public HTTPS frontend URL")
		}
		if !cfg.GmailEnabled || !googleSvc.Configured() || cfg.TokenEncryptionKey == "" {
			return errors.New("password recovery requires Gmail and Google OAuth configuration")
		}
		accountID, parseErr := uuid.Parse(os.Getenv("PASSWORD_RESET_GMAIL_ACCOUNT_ID"))
		if parseErr != nil || accountID == uuid.Nil {
			return errors.New("PASSWORD_RESET_GMAIL_ACCOUNT_ID must identify the Google integration used for password recovery")
		}
		gmail := mailgw.NewGmail(mailgw.GmailCredentialsFunc(func(ctx context.Context) (string, string, error) {
			return googleSvc.GmailCredentialsForWorkspace(ctx, accountID)
		}))
		passwordRecovery = &transport.PasswordRecovery{
			Auth: auth.Recovery{Repo: repo, Key: cfg.TokenEncryptionKey}, SiteURL: site.String(), Send: gmail.SendMessage,
		}
	}

	app := transport.New(cfg, logger, groupsSvc, googleSvc, slackSvc, slackChannelsSvc, slackEventsSvc, authSvc, pool.Ping,
		transport.GroupProvisioning{
			PasswordRecovery: passwordRecovery, TranslateText: google.TextTranslator(os.Getenv("GOOGLE_TRANSLATION_API_KEY")),
			Launch: launch, GroupLauncher: launcher, LaunchStatus: repo,
			Classroom: classroom, Drive: drive, Forms: forms,
			Delivery: delivery, Audit: audit, AuditNotifications: repo, Announcements: announcements,
			DeliveryEnabled: cfg.SMTPEnabled || cfg.GmailEnabled,
			Notifications:   repo, Templates: repo, StaffAssignments: repo, GroupStaff: repo, GroupRoster: repo, LaunchSteps: repo,
			Membership: repo, AssignedGroups: repo, StaffAdmin: repo, GroupResources: repo,
			SlackOAuth: slackOAuthSvc, SlackWorkspaces: repo,
			GoogleWorkspaces:    repo,
			GroupWorkspaceOwner: repo,
		})

	// 5. Serve with graceful shutdown.
	servers := []*http.Server{{
		Addr:              ":" + cfg.Port,
		Handler:           app,
		ReadHeaderTimeout: 5 * time.Second,
	}}

	// 5b. Dedicated Slack Events listener (tunnel-facing, loopback by
	// default). Only started when the signing secret is configured — an
	// unprotected listener must never exist.
	if cfg.SlackEventsListenerEnabled() {
		eventsApp := transport.NewSlackEventsListener(logger, slackEventsSvc)
		servers = append(servers, &http.Server{
			Addr:              cfg.SlackEventsAddr,
			Handler:           eventsApp,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       60 * time.Second,
		})
		logger.Info("slack events listener (events-only)", "addr", cfg.SlackEventsAddr)
	} else {
		logger.Warn("slack events listener not started: SLACK_SIGNING_SECRET (or SLACK_EVENTS_ADDR) not configured")
	}

	errCh := make(chan error, len(servers))
	for _, srv := range servers {
		srv := srv
		go func() {
			logger.Info("api listening", "addr", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	select {
	case sig := <-quit:
		logger.Info("shutting down", "signal", sig.String())
	case err := <-errCh:
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(ctx); err != nil {
			return err
		}
	}
	return nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// newSlackServices wires the Slack connection and channel operations when —
// and only when — the bot token is configured. The token stays in the
// adapter; the service and the database only ever see workspace metadata.
func newSlackServices(cfg config.Config, repo *store.Repo) (*integrations.SlackAuthService, *integrations.SlackChannelsService) {
	if !cfg.SlackConfigured() {
		logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
		logger.Warn("slack integration disabled: set SLACK_BOT_TOKEN")
		return integrations.NewSlackAuthService(repo, nil, false),
			integrations.NewSlackChannelsService(nil, nil, repo, false)
	}
	gateway := slackgw.NewGateway(cfg.SlackBotToken)
	channels := integrations.NewSlackChannelsService(gateway, repo, repo, true)
	channels.Joins = repo // successful invites reconcile onboarding joins
	return integrations.NewSlackAuthService(repo, gateway, true), channels
}

// newSlackEventsService wires the Events webhook when the signing secret is
// configured. Verification happens with the signing secret only — the bot
// token is irrelevant to this endpoint. Identity linking additionally needs
// the bot token (users.info lookups), so the linker is only attached when
// both credentials exist.
func newSlackEventsService(cfg config.Config, repo *store.Repo, logger *slog.Logger) *integrations.SlackEventsService {
	if !cfg.SlackEventsConfigured() {
		logger.Warn("slack events disabled: set SLACK_SIGNING_SECRET")
		return integrations.NewSlackEventsService(nil, repo, nil, time.Now, false)
	}
	verifier := slackgw.NewRequestVerifier(cfg.SlackSigningSecret)
	var linker *integrations.SlackIdentityLinker
	if cfg.SlackConfigured() {
		linker = integrations.NewSlackIdentityLinker(slackgw.NewGateway(cfg.SlackBotToken), repo)
	} else {
		logger.Warn("slack identity linking disabled: SLACK_BOT_TOKEN not configured")
	}
	return integrations.NewSlackEventsService(verifier, repo, linker, time.Now, true)
}

func connectWithRetry(url string, logger *slog.Logger) (*pgxpool.Pool, error) {
	const attempts = 30
	const delay = 1 * time.Second
	var pool *pgxpool.Pool
	var err error
	for range attempts {
		pool, err = pgxpool.New(context.Background(), url)
		if err == nil {
			if pingErr := pool.Ping(context.Background()); pingErr == nil {
				return pool, nil
			} else {
				err = pingErr
				pool.Close()
			}
		}
		logger.Warn("postgres not ready, retrying", "err", err.Error())
		time.Sleep(delay)
	}
	return nil, fmt.Errorf("postgres unreachable after retries: %w", err)
}
