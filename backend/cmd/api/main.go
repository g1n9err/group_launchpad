// cmd/api — composition root. The ONLY file that knows how everything
// connects: config → pool → store → core services → transport → serve.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"launchpad/internal/config"
	"launchpad/internal/core/groups"
	"launchpad/internal/core/integrations"
	"launchpad/internal/dbmigrate"
	"launchpad/internal/integrations/google"
	slackgw "launchpad/internal/integrations/slack"
	"launchpad/internal/jobs"
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
	slackEventsSvc := newSlackEventsService(cfg, repo, logger)
	queue, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return fmt.Errorf("initialize River insert client: %w", err)
	}
	launch := &jobs.SlackLaunchService{Queue: repo, River: queue}
	classroom := &integrations.ClassroomService{Tokens: googleSvc, Gateway: google.NewClassroomGateway(), Store: repo, Operations: repo}
	drive := &integrations.DriveService{Tokens: googleSvc, Gateway: google.NewDriveGateway(), Store: repo, Operations: repo}
	forms := &integrations.FormsService{Tokens: googleSvc, Gateway: google.NewFormsGateway(), Store: repo, Operations: repo}
	audit := &jobs.AuditService{Store: repo, River: queue}
	delivery := &jobs.IntakeDeliveryService{Queue: repo, River: queue}
	if cfg.GmailEnabled {
		delivery.CheckReady = googleSvc.CheckGmailPermission
	}
	app := transport.New(cfg, logger, groupsSvc, googleSvc, slackSvc, slackChannelsSvc, slackEventsSvc, pool.Ping,
		transport.GroupProvisioning{Launch: launch, Classroom: classroom, Drive: drive, Forms: forms, Delivery: delivery, Audit: audit, DeliveryEnabled: cfg.SMTPEnabled || cfg.GmailEnabled})

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
	return integrations.NewSlackAuthService(repo, gateway, true),
		integrations.NewSlackChannelsService(gateway, repo, repo, true)
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
