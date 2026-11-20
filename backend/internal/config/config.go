// Package config loads runtime configuration from environment variables.
// Fail-fast: the process must refuse to boot with a broken configuration.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
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

	// CoordinatorPassword protects coordinator-only routes. Empty leaves
	// those routes unavailable.
	CoordinatorPassword string

	// Intake email is queued only when an SMTP worker is explicitly enabled.
	AuditNotificationEmail                                   string
	SMTPEnabled, GmailEnabled                                bool
	SMTPHost, SMTPPort, SMTPUsername, SMTPPassword, SMTPFrom string

	// Google OAuth connection credentials. Empty ⇒ Google OAuth disabled
	// (endpoints return 503 NOT_READY); nothing is simulated.
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string

	// TokenEncryptionKey (base64, 32 bytes) encrypts stored OAuth refresh
	// tokens (AES-256-GCM). Empty ⇒ Google OAuth disabled.
	TokenEncryptionKey string

	// SlackBotToken authenticates the installed workspace bot. Empty ⇒ Slack
	// integration disabled. Env-only: never persisted, never logged.
	SlackBotToken string

	// SlackSigningSecret verifies Events API request signatures. Empty ⇒
	// the events webhook is disabled. Env-only: never persisted, never logged.
	SlackSigningSecret string

	// SlackEventsAddr is the bind address of the dedicated events-only
	// listener (tunnel-facing). Loopback default: only a local tunnel/proxy
	// can reach it. Ignored when the signing secret is absent — an
	// unprotected listener is never started.
	SlackEventsAddr string

	// SessionTTL is the lifetime of multi-role bearer sessions. Default 12h.
	SessionTTL time.Duration

	// LoginRateMax is the failed-login budget per IP+email within the
	// window before the account address pair is throttled (429).
	LoginRateMax int
	// LoginRateWindow is the login throttle window.
	LoginRateWindow time.Duration

	// TrustProxy enables X-Forwarded-For-based client-IP extraction for the
	// login throttle and logs. Enable ONLY behind a reverse proxy that
	// OVERWRITES (not appends to) X-Forwarded-For — otherwise clients can
	// forge their apparent address. Default false: the direct socket peer is
	// used, which misattributes (and globally caps) all users behind a proxy.
	TrustProxy bool
}

// LoginRateMaxOrDefault returns the configured max or 10.
func (c Config) LoginRateMaxOrDefault() int {
	if c.LoginRateMax > 0 {
		return c.LoginRateMax
	}
	return 10
}

// LoginRateWindowOrDefault returns the configured window or 5 minutes.
func (c Config) LoginRateWindowOrDefault() time.Duration {
	if c.LoginRateWindow > 0 {
		return c.LoginRateWindow
	}
	return 5 * time.Minute
}

// SessionTTLOrDefault returns the configured session TTL or 12h.
func (c Config) SessionTTLOrDefault() time.Duration {
	if c.SessionTTL > 0 {
		return c.SessionTTL
	}
	return 12 * time.Hour
}

// GoogleOAuthConfigured reports whether every prerequisite for the Google
// OAuth flow is present.
func (c Config) GoogleOAuthConfigured() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != "" &&
		c.GoogleRedirectURL != "" && c.TokenEncryptionKey != ""
}

// SlackConfigured reports whether the Slack bot token is present.
func (c Config) SlackConfigured() bool {
	return c.SlackBotToken != ""
}

// SlackEventsConfigured reports whether the signing secret is present.
func (c Config) SlackEventsConfigured() bool {
	return c.SlackSigningSecret != ""
}

// SlackEventsListenerEnabled reports whether the dedicated events-only
// listener should start: requires the signing secret (never unprotected).
func (c Config) SlackEventsListenerEnabled() bool {
	return c.SlackEventsConfigured() && c.SlackEventsAddr != ""
}

// WorkerID returns an optional override for River's generated client ID.
func (c Config) WorkerID() string {
	return strings.TrimSpace(os.Getenv("WORKER_ID"))
}

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		AuditNotificationEmail: getenv("AUDIT_NOTIFICATION_EMAIL", ""),
		SMTPHost:               getenv("SMTP_HOST", ""),
		SMTPPort:               getenv("SMTP_PORT", "587"),
		SMTPUsername:           getenv("SMTP_USERNAME", ""),
		SMTPPassword:           os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:               getenv("SMTP_FROM", ""),
		Port:                   getenv("PORT", "8080"),
		LogLevel:               getenv("LOG_LEVEL", "info"),
		CoordinatorPassword:    os.Getenv("COORDINATOR_PASSWORD"),
		CORSAllowedOrigins:     splitCSV(getenv("CORS_ALLOWED_ORIGINS", "http://localhost:5173")),
		GoogleClientID:         getenv("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret:     getenv("GOOGLE_CLIENT_SECRET", ""),
		GoogleRedirectURL:      getenv("GOOGLE_REDIRECT_URL", ""),
		TokenEncryptionKey:     getenv("TOKEN_ENCRYPTION_KEY", ""),
		SlackBotToken:          getenv("SLACK_BOT_TOKEN", ""),
		SlackSigningSecret:     getenv("SLACK_SIGNING_SECRET", ""),
		SlackEventsAddr:        getenv("SLACK_EVENTS_ADDR", "127.0.0.1:8081"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("config: DATABASE_URL is required")
	}
	if ttl, err := time.ParseDuration(getenv("AUTH_SESSION_TTL", "12h")); err != nil {
		return Config{}, fmt.Errorf("config: AUTH_SESSION_TTL must be a duration (e.g. 12h): %w", err)
	} else {
		cfg.SessionTTL = ttl
	}
	loginMax, err := strconv.Atoi(getenv("LOGIN_RATE_MAX", "10"))
	if err != nil || loginMax <= 0 {
		return Config{}, fmt.Errorf("config: LOGIN_RATE_MAX must be a positive integer")
	}
	cfg.LoginRateMax = loginMax
	if window, err := time.ParseDuration(getenv("LOGIN_RATE_WINDOW", "5m")); err != nil {
		return Config{}, fmt.Errorf("config: LOGIN_RATE_WINDOW must be a duration (e.g. 5m): %w", err)
	} else {
		cfg.LoginRateWindow = window
	}
	cfg.TrustProxy, err = strconv.ParseBool(getenv("TRUST_PROXY", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("config: TRUST_PROXY must be a boolean")
	}
	if cfg.CoordinatorPassword != "" && (len(cfg.CoordinatorPassword) < 32 || strings.TrimSpace(cfg.CoordinatorPassword) != cfg.CoordinatorPassword) {
		return Config{}, fmt.Errorf("config: COORDINATOR_PASSWORD must contain at least 32 characters without surrounding whitespace")
	}
	if cfg.Port == "" {
		return Config{}, fmt.Errorf("config: PORT must not be empty")
	}
	migrateOnBoot, err := strconv.ParseBool(getenv("MIGRATE_ON_BOOT", "true"))
	if err != nil {
		return Config{}, fmt.Errorf("config: MIGRATE_ON_BOOT must be a boolean: %w", err)
	}
	cfg.MigrateOnBoot = migrateOnBoot
	cfg.SMTPEnabled, err = strconv.ParseBool(getenv("SMTP_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("config: SMTP_ENABLED must be a boolean")
	}
	cfg.GmailEnabled, err = strconv.ParseBool(getenv("GMAIL_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("config: GMAIL_ENABLED must be a boolean")
	}
	if cfg.GmailEnabled && cfg.SMTPEnabled {
		return Config{}, fmt.Errorf("config: enable only one intake email provider")
	}
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
