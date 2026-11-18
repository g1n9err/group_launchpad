// Package config loads runtime configuration from environment variables.
// Fail-fast: the process must refuse to boot with a broken configuration.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the complete runtime configuration for the API and worker.
type Config struct {
	// DatabaseURL is the PostgreSQL DSN. Required.
	DatabaseURL string

	// Port the HTTP API listens on. Default "8080".
	Port string

	// LogLevel: debug | info | warn | error. Default "info".
	LogLevel string

	// MigrateOnBoot applies pending migrations at startup (dev convenience;
	// production pipelines run cmd/migrate explicitly before deploy).
	MigrateOnBoot bool

	// CORSAllowedOrigins lists browser origins allowed to call the API.
	// Default covers the local Vite dev server.
	CORSAllowedOrigins []string

	// Google OAuth connection credentials. Empty ⇒ Google OAuth disabled
	// (endpoints return 503 NOT_READY); nothing is simulated.
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string

	// TokenEncryptionKey (base64, 32 bytes) encrypts stored OAuth refresh
	// tokens (AES-256-GCM). Empty ⇒ Google OAuth disabled.
	TokenEncryptionKey string
}

// GoogleOAuthConfigured reports whether every prerequisite for the Google
// OAuth flow is present.
func (c Config) GoogleOAuthConfigured() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != "" &&
		c.GoogleRedirectURL != "" && c.TokenEncryptionKey != ""
}

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		Port:               getenv("PORT", "8080"),
		LogLevel:           getenv("LOG_LEVEL", "info"),
		CORSAllowedOrigins: splitCSV(getenv("CORS_ALLOWED_ORIGINS", "http://localhost:5173")),
		GoogleClientID:     getenv("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: getenv("GOOGLE_CLIENT_SECRET", ""),
		GoogleRedirectURL:  getenv("GOOGLE_REDIRECT_URL", ""),
		TokenEncryptionKey: getenv("TOKEN_ENCRYPTION_KEY", ""),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("config: DATABASE_URL is required")
	}
	if cfg.Port == "" {
		return Config{}, fmt.Errorf("config: PORT must not be empty")
	}
	migrateOnBoot, err := strconv.ParseBool(getenv("MIGRATE_ON_BOOT", "true"))
	if err != nil {
		return Config{}, fmt.Errorf("config: MIGRATE_ON_BOOT must be a boolean: %w", err)
	}
	cfg.MigrateOnBoot = migrateOnBoot
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
