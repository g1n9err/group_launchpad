// cmd/worker — background-job process.
//
// Day 1 (honest placeholder): verifies database connectivity on a heartbeat
// and exits cleanly on SIGTERM. No jobs are simulated — River (Postgres job
// queue: retries, unique jobs, cron) is wired here on Day 2 together with the
// launch workflow, daily audit, and announcement publishing.
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

	"launchpad/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
	defer pool.Close()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	logger.Info("worker started", "note", "River job processing arrives Day 2; heartbeat only")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	heartbeat := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			logger.Error("worker heartbeat: postgres unreachable", "err", err.Error())
			return
		}
		logger.Debug("worker heartbeat ok")
	}

	heartbeat() // initial check
	for {
		select {
		case <-ticker.C:
			heartbeat()
		case sig := <-quit:
			logger.Info("worker shutting down", "signal", sig.String())
			return
		}
	}
}
