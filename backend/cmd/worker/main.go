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
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"launchpad/internal/config"
	"launchpad/internal/core/integrations"
	"launchpad/internal/dbmigrate"
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
	if cfg.SlackConfigured() {
		slack = integrations.NewSlackChannelsService(slackgw.NewGateway(cfg.SlackBotToken), repo, repo, true)
	}
	river.AddWorker(workers, &jobs.SlackLaunchWorker{Store: repo, Slack: slack})
	client, err := jobs.NewClient(pool, workers, jobs.Config{
		WorkerID: cfg.WorkerID(),
	})
	if err != nil {
		return err
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
