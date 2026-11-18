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

	"launchpad/internal/config"
	"launchpad/internal/core/groups"
	"launchpad/internal/core/integrations"
	"launchpad/internal/dbmigrate"
	"launchpad/internal/integrations/google"
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
	googleSvc := newGoogleService(cfg, repo, logger)
	app := transport.New(cfg, logger, groupsSvc, googleSvc, pool.Ping)

	// 5. Serve with graceful shutdown.
	srv := &http.Server{Addr: ":" + cfg.Port, Handler: app, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("api listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

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
	return srv.Shutdown(ctx)
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

// newGoogleService wires the Google OAuth flow when — and only when — every
// prerequisite is configured. Unset credentials leave the integration
// disabled (503 on its endpoints); nothing is simulated.
func newGoogleService(cfg config.Config, repo *store.Repo, logger *slog.Logger) *integrations.GoogleAuthService {
	if !cfg.GoogleOAuthConfigured() {
		logger.Warn("google oauth disabled: set GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, GOOGLE_REDIRECT_URL, TOKEN_ENCRYPTION_KEY")
		return integrations.NewGoogleAuthService(repo, nil, nil, false)
	}
	cipher, err := secrets.New(cfg.TokenEncryptionKey)
	if err != nil {
		logger.Error("google oauth disabled: invalid TOKEN_ENCRYPTION_KEY", "err", err.Error())
		return integrations.NewGoogleAuthService(repo, nil, nil, false)
	}
	gateway := google.NewGateway(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL, integrations.GoogleScopes)
	return integrations.NewGoogleAuthService(repo, gateway, cipher, true)
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
