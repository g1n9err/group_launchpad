// cmd/worker — background job process powered by River (PostgreSQL-backed
// queue). Runs workers for the automation engine: unified group launches,
// daily sync audits, and announcement scheduling.
package main

import (
	"context"
	"fmt"
	"log/slog"
	stdmail "net/mail"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"launchpad/internal/config"
	"launchpad/internal/core/audit"
	"launchpad/internal/core/groups"
	"launchpad/internal/core/integrations"
	"launchpad/internal/dbmigrate"
	"launchpad/internal/integrations/google"
	mailgw "launchpad/internal/integrations/mail"
	slackgw "launchpad/internal/integrations/slack"
	"launchpad/internal/jobs"
	"launchpad/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel)

	pool, err := connectWithRetry(cfg.DatabaseURL, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.MigrateOnBoot {
		if err := dbmigrate.Up(cfg.DatabaseURL); err != nil {
			return fmt.Errorf("migrate on boot: %w", err)
		}
		logger.Info("migrations up to date")
	}

	// ── Build services first ──────────────────────────────────────────
	repo := store.New(pool)
	groupsSvc := groups.New(repo)
	// Staff emails for Drive/Classroom access (comma-separated env var).
	staffEmails := splitCSV(os.Getenv("STAFF_EMAILS"))

	// Slack
	slack := integrations.NewSlackChannelsService(nil, repo, repo, false)
	var slackChecker audit.Checker
	var memberChecker audit.MemberChecker
	if cfg.SlackConfigured() {
		slackGateway := slackgw.NewGateway(cfg.SlackBotToken)
		slack = integrations.NewSlackChannelsService(slackGateway, repo, repo, true)
		slackChecker = slackGateway
		memberChecker = slackGateway
	}

	// Google (Classroom, Drive, Forms share the OAuth token source)
	auth := google.NewAuthService(cfg, repo, logger)
	var classroomSvc *integrations.ClassroomService
	var driveSvc *integrations.DriveService
	var formsSvc *integrations.FormsService
	if auth.Configured() {
		classroomSvc = &integrations.ClassroomService{Tokens: auth, Gateway: google.NewClassroomGateway(), Store: repo, Operations: repo}
		driveSvc = &integrations.DriveService{Tokens: auth, Gateway: google.NewDriveGateway(), Store: repo, Operations: repo}
		formsSvc = &integrations.FormsService{Tokens: auth, Gateway: google.NewFormsGateway(), Store: repo, Operations: repo}
	}

	// ── Register workers ──────────────────────────────────────────────
	workers := jobs.NewLaunchWorkers(logger, slack, cfg.SlackConfigured(), classroomSvc, driveSvc, formsSvc, groupsSvc, staffEmails, repo, repo, repo)

	// Slack-only launch worker (legacy, still needed for older queue entries)
	river.AddWorker(workers, &jobs.SlackLaunchWorker{Store: repo, Slack: slack})

	if cfg.SlackConfigured() {
		slackGateway := slackgw.NewGateway(cfg.SlackBotToken)
		river.AddWorker(workers, &jobs.AnnouncementWorker{Store: repo, Slack: slackGateway})
	}

	river.AddWorker(workers, &jobs.DailyAuditWorker{Groups: repo})

	jobConfig := jobs.Config{WorkerID: cfg.WorkerID(), SlackAnnouncementsEnabled: cfg.SlackConfigured()}

	var mailer interface {
		integrations.IntakeMailer
		jobs.AuditMailer
	}
	if cfg.SMTPEnabled {
		relay, err := mailgw.NewRelay(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPFrom)
		if err != nil {
			return err
		}
		mailer = relay
	}
	if cfg.GmailEnabled {
		if !auth.Configured() {
			return fmt.Errorf("GMAIL_ENABLED requires valid Google OAuth configuration")
		}
		mailer = mailgw.NewGmail(auth)
	}

	auditEmailEnabled := mailer != nil && cfg.AuditNotificationEmail != ""
	if cfg.AuditNotificationEmail != "" {
		address, err := stdmail.ParseAddress(cfg.AuditNotificationEmail)
		if err != nil || address.Address != cfg.AuditNotificationEmail || address.Name != "" {
			return fmt.Errorf("AUDIT_NOTIFICATION_EMAIL must be a single email address")
		}
	}
	if mailer != nil {
		river.AddWorker(workers, &jobs.IntakeDeliveryWorker{Store: repo, Mailer: mailer})
		jobConfig.IntakeEmailWorkers = 2
	}
	if auditEmailEnabled {
		jobConfig.AuditEmailEnabled = true
		river.AddWorker(workers, &jobs.AuditEmailWorker{Store: repo, Mailer: mailer, Recipient: cfg.AuditNotificationEmail})
	}
	river.AddWorker(workers, &jobs.GroupAuditWorker{Audit: &audit.Service{Store: repo, Slack: slackChecker, Google: google.NewAuditChecker(auth), Members: memberChecker}, EmailEnabled: auditEmailEnabled})

	client, err := jobs.NewClient(pool, workers, jobConfig)
	if err != nil {
		return err
	}

	if _, err := client.PeriodicJobs().AddSafely(jobs.DailyAuditSchedule()); err != nil {
		return fmt.Errorf("register daily audit: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return jobs.RunWorker(ctx, client, logger)
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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

func connectWithRetry(url string, logger *slog.Logger) (*pgxpool.Pool, error) {
	const attempts = 30
	const delay = 1 * time.Second
	var pool *pgxpool.Pool
	var err error
	for i := 0; i < attempts; i++ {
		pool, err = pgxpool.New(context.Background(), url)
		if err == nil {
			if pingErr := pool.Ping(context.Background()); pingErr == nil {
				return pool, nil
			} else {
				err = pingErr
				pool.Close()
			}
		}
		logger.Warn("postgres not ready, retrying", "attempt", i+1, "err", err.Error())
		time.Sleep(delay)
	}
	return nil, fmt.Errorf("postgres unreachable after %d attempts: %w", attempts, err)
}
