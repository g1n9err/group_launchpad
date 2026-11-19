// cmd/worker — background job process powered by River (PostgreSQL-backed
// queue). Runs workers for the automation engine: launch pipelines, daily
// sync audits, and announcement scheduling (added in later phases).
//
// The worker is a SEPARATE process from the API: it never serves HTTP, and
// the API never runs job workers. They share the same PostgreSQL pool
// configuration but are independently deployable and restartable.
package main

import (
	"context"
	"fmt"
	"log/slog"
	stdmail "net/mail"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"launchpad/internal/config"
	"launchpad/internal/core/audit"
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

	// Connect to PostgreSQL with retry (compose races).
	pool, err := connectWithRetry(cfg.DatabaseURL, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Apply pending migrations (worker must see the same schema).
	if cfg.MigrateOnBoot {
		if err := dbmigrate.Up(cfg.DatabaseURL); err != nil {
			return fmt.Errorf("migrate on boot: %w", err)
		}
		logger.Info("migrations up to date")
	}

	// Build the River client with registered workers.
	workers := jobs.NewWorkers(logger)
	repo := store.New(pool)
	slack := integrations.NewSlackChannelsService(nil, repo, repo, false)
	var slackChecker audit.Checker
	if cfg.SlackConfigured() {
		slackGateway := slackgw.NewGateway(cfg.SlackBotToken)
		slack = integrations.NewSlackChannelsService(slackGateway, repo, repo, true)
		slackChecker = slackGateway
		river.AddWorker(workers, &jobs.AnnouncementWorker{Store: repo, Slack: slackGateway})
	}
	river.AddWorker(workers, &jobs.SlackLaunchWorker{Store: repo, Slack: slack})
	auth := google.NewAuthService(cfg, repo, logger)
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
	river.AddWorker(workers, &jobs.GroupAuditWorker{Audit: &audit.Service{Store: repo, Slack: slackChecker, Google: google.NewAuditChecker(auth)}, EmailEnabled: auditEmailEnabled})

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
