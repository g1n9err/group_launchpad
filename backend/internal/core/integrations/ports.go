// Package integrations contains connection-management business rules for the
// external platforms. Google OAuth lives here (Stage 1); Slack connection
// metadata arrives in Stage 2. Pure Go: HTTP to Google is the Gateway port
// (impl: internal/integrations/google), SQL is the Repository port (impl:
// internal/store), encryption is the TokenCipher port (impl: internal/secrets).
package integrations

import (
	"context"
	"errors"
	"time"
)

// Provider values stored in the integrations table.
const (
	ProviderGoogle = "google"
)

// Connection statuses stored in integrations.connection_status.
const (
	StatusConnected    = "connected"
	StatusDisconnected = "disconnected"
	StatusError        = "error"
)

// StateTTL bounds how long a pending OAuth state stays usable.
const StateTTL = 10 * time.Minute

// GoogleScopes are the OAuth scopes justified by the PRD — no more:
//   - 4.3 Classroom: create courses, manage teachers/students
//   - 4.4 Drive:     create the app-owned group folder tree + permissions
//   - 4.5 Forms:     create intake forms, read responses
//   - 4.6 matching:  roster emails
var GoogleScopes = []string{
	"openid",
	"email",
	"profile",
	"https://www.googleapis.com/auth/classroom.courses",
	"https://www.googleapis.com/auth/classroom.rosters",
	"https://www.googleapis.com/auth/classroom.profile.emails",
	"https://www.googleapis.com/auth/drive.file",
	"https://www.googleapis.com/auth/forms.body",
	"https://www.googleapis.com/auth/forms.responses.readonly",
}

// ErrRefreshRevoked signals that Google permanently rejected our refresh
// token (user revoked access, or a Testing-mode token expired after 7 days).
var ErrRefreshRevoked = errors.New("google refresh token rejected")

// GoogleStatus is the connection state exposed to coordinators. It never
// contains tokens.
type GoogleStatus struct {
	Status       string    `json:"status"` // connected | disconnected | error
	AccountEmail string    `json:"accountEmail,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
	ConnectedAt  time.Time `json:"connectedAt,omitempty"`
}

// GoogleTokenBundle is what a successful authorization-code exchange yields.
type GoogleTokenBundle struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	AccountEmail string
}

// TokenCipher encrypts values stored at rest (impl: internal/secrets).
type TokenCipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(encoded string) (string, error)
	KeyID() string
}

// GoogleAuthGateway performs the browser-redirect URL construction and token
// HTTP exchanges with Google (impl: internal/integrations/google).
type GoogleAuthGateway interface {
	// AuthCodeURL builds the Google consent URL (PKCE S256 + offline access).
	AuthCodeURL(state, codeChallenge string) string
	// Exchange swaps the authorization code (with PKCE verifier) for tokens
	// and the connected account's email.
	Exchange(ctx context.Context, code, codeVerifier string) (GoogleTokenBundle, error)
	// Refresh mints an access token from a refresh token. Returns
	// ErrRefreshRevoked when Google permanently rejects the refresh token.
	Refresh(ctx context.Context, refreshToken string) (accessToken string, expiry time.Time, err error)
}

// StoredGoogleConnection is the persisted connection plus the (encrypted)
// credential material needed to mint access tokens.
type StoredGoogleConnection struct {
	Status          GoogleStatus
	RefreshTokenEnc string // non-empty iff Status.Status == connected
	KeyID           string
}

// GoogleAuthRepository is the persistence port (impl: internal/store).
type GoogleAuthRepository interface {
	// CreateState stores a pending OAuth state (hashed) with its encrypted
	// PKCE verifier.
	CreateState(ctx context.Context, stateHash, verifierEnc string, expiresAt time.Time) error
	// ConsumeState atomically marks a live state as used and returns the
	// stored encrypted verifier. Unknown, expired, or already-used states
	// return a domain error.
	ConsumeState(ctx context.Context, stateHash string) (verifierEnc string, err error)
	// SaveConnection upserts the 'google' integration row and atomically
	// replaces the active credential with the given encrypted refresh token.
	SaveConnection(ctx context.Context, accountEmail string, scopes []string, refreshTokenEnc, keyID string) error
	// GoogleConnection returns the current connection (Status.Status ==
	// StatusDisconnected when none exists).
	GoogleConnection(ctx context.Context) (StoredGoogleConnection, error)
	// MarkGoogleError flags the connection as errored (refresh rejected).
	MarkGoogleError(ctx context.Context) error
}
