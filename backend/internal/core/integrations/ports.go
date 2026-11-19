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

	"github.com/google/uuid"
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
const GmailSendScope = "https://www.googleapis.com/auth/gmail.send"

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

// Slack error categories. The adapter classifies raw transport errors into
// these typed errors so the service can decide what to persist and expose —
// no raw error text (which may echo request fragments) ever crosses the port.
var (
	// ErrSlackAuthRejected means Slack permanently rejected the bot token
	// (invalid, revoked, or the app was removed). Worth persisting as error.
	ErrSlackAuthRejected = errors.New("slack rejected the bot token")
	// ErrSlackUnavailable means a transient failure (network, timeout,
	// Slack-side 5xx). NOT persisted — the next check may succeed.
	ErrSlackUnavailable = errors.New("slack unreachable")
	// ErrSlackChannelNameTaken means a channel with the requested name
	// already exists — the idempotent path looks it up and reuses it.
	ErrSlackChannelNameTaken = errors.New("slack channel name already taken")
	// ErrSlackUserNotFound means no workspace member matches the email.
	ErrSlackUserNotFound = errors.New("slack user not found for email")
)

// DefaultChannelSuffixes are the channels created for every group (PRD 4.2).
var DefaultChannelSuffixes = []string{"general", "announcements", "questions", "resources"}

// Resource types stored in group_resources for Slack.
const (
	ResourceTypeChannel        = "channel"
	ResourceTypeWelcomeMessage = "welcome_message"
)

// Resource statuses in group_resources.
const (
	ResourceStatusActive  = "active"
	ResourceStatusPending = "pending"
	ResourceStatusError   = "error"
)

// InviteOutcome describes the idempotent result of inviting one user to one
// channel (already_in is a SUCCESS for retries).
type InviteOutcome string

const (
	InviteOutcomeInvited       InviteOutcome = "invited"
	InviteOutcomeAlreadyMember InviteOutcome = "already_member"
)

// GroupResource is the idempotency-registry view of one external resource
// owned by a group (PRD §8).
type GroupResource struct {
	Provider   string
	Type       string
	Name       string
	ExternalID string // channel ID / message ts / course ID …
	Status     string // active | pending | error
}

// SlackStatus is the Slack connection state exposed to coordinators. It never
// contains the bot token or signing secret.
type SlackStatus struct {
	Status      string    `json:"status"` // connected | disconnected | error
	TeamID      string    `json:"teamId,omitempty"`
	TeamName    string    `json:"teamName,omitempty"` // workspace name — public metadata
	ConnectedAt time.Time `json:"connectedAt,omitempty"`
}

// SlackWorkspace is the verified workspace identity from auth.test.
type SlackWorkspace struct {
	TeamID    string
	TeamName  string
	BotUserID string // the bot's own Slack user ID (classifies bot joins)
}

// SlackEventStatus values for slack_events.process_status.
const (
	EventStatusPending    = "pending"
	EventStatusProcessed  = "processed"
	EventStatusUnresolved = "unresolved"
	EventStatusBot        = "bot"
	EventStatusIgnored    = "ignored"
)

// RecordedSlackEvent is one Events API envelope, persisted for dedup and
// durable processing. Payload is stored as-is (member_joined_channel carries
// IDs and metadata only — never credentials).
type RecordedSlackEvent struct {
	EventID   string
	EventType string
	TeamID    string
	UserID    string
	ChannelID string
	Payload   []byte
}

// SlackEventRequest is the raw, unparsed webhook input. The raw body is
// required for HMAC verification and MUST be preserved before any parsing.
type SlackEventRequest struct {
	Timestamp string // X-Slack-Request-Timestamp
	Signature string // X-Slack-Signature
	RawBody   string
}

// SlackEventAck is the processing outcome acknowledged to Slack.
type SlackEventAck struct {
	Kind      string // challenge | duplicate | processed | unresolved | bot | ignored | bad_request
	Challenge string // set only for Kind == "challenge"
}

// SlackEventVerifier checks Slack's v0 request signature over the raw body.
// Implementation lives in internal/integrations/slack.
type SlackEventVerifier interface {
	Verify(timestamp, rawBody, receivedSignature string) bool
}

// SlackEventRepository persists and resolves events (impl: internal/store).
type SlackEventRepository interface {
	// InsertSlackEvent atomically records the event for dedup; inserted=false
	// means this event_id was already seen (safe under concurrency).
	InsertSlackEvent(ctx context.Context, ev RecordedSlackEvent) (inserted bool, err error)
	// CompleteSlackEvent records the durable processing outcome.
	CompleteSlackEvent(ctx context.Context, eventID, status, unresolvedReason string) error
	// SlackBotUserID returns the connected bot's Slack user ID ("" unknown).
	SlackBotUserID(ctx context.Context) (string, error)
	// FindGroupIDBySlackChannel maps a Slack channel ID → our group.
	FindGroupIDBySlackChannel(ctx context.Context, channelID string) (uuid.UUID, bool, error)
	// FindUserIDBySlack maps a Slack user ID → our user.
	FindUserIDBySlack(ctx context.Context, slackUserID string) (uuid.UUID, bool, error)
	// FindGroupMember returns the member row id and onboarding status.
	FindGroupMember(ctx context.Context, groupID, userID uuid.UUID) (memberID uuid.UUID, onboardingStatus string, found bool, err error)
	// MarkGroupMemberJoinedSlack records the Slack join and completes onboarding if the form exists.
	MarkGroupMemberJoinedSlack(ctx context.Context, memberID uuid.UUID) error
}

// ExternalOperation records one external API call (PRD 8.7). Error is a
// sanitized category string, never raw transport output. GroupID is nil for
// connection-level operations and set for per-group operations.
type ExternalOperation struct {
	GroupID    *uuid.UUID
	Provider   string // "slack" | "google" | ...
	Operation  string // e.g. "slack.auth.test"
	Error      string // "" on success, else category ("auth_rejected", "unreachable")
	DurationMS int64
}

// SlackGateway performs Slack API calls (impl: internal/integrations/slack).
type SlackGateway interface {
	// AuthTest verifies the bot token and returns the workspace identity.
	// Errors are ErrSlackAuthRejected or ErrSlackUnavailable.
	AuthTest(ctx context.Context) (SlackWorkspace, error)

	// LookupChannelIDs resolves channel names to IDs via paginated
	// conversations.list. Channels that do not exist are absent from the map.
	LookupChannelIDs(ctx context.Context, names []string) (map[string]string, error)

	// CreateChannel creates a public channel; ErrSlackChannelNameTaken means
	// a concurrent creation won and the caller should look the name up.
	CreateChannel(ctx context.Context, name string) (channelID string, err error)

	// LookupUserByEmail resolves a workspace member by email;
	// ErrSlackUserNotFound when nobody matches.
	LookupUserByEmail(ctx context.Context, email string) (userID string, err error)

	// UserEmail resolves a Slack user ID to the account's email (requires
	// users:read.email). Empty string when the profile exposes no email;
	// ErrSlackUserNotFound when the account is gone.
	UserEmail(ctx context.Context, slackUserID string) (email string, err error)

	// InviteUserToChannel idempotently invites one user to one channel —
	// already_in is returned as InviteOutcomeAlreadyMember, not an error.
	InviteUserToChannel(ctx context.Context, channelID, userID string) (InviteOutcome, error)

	// PostMessage publishes one message and returns its timestamp (used as
	// the external resource ID for welcome-message dedup).
	PostMessage(ctx context.Context, channelID, text string) (messageTS string, err error)
}

// SlackResourceRepository persists the per-group idempotency registry
// (impl: internal/store).
type SlackResourceRepository interface {
	// GetResource returns the registry row for a group resource; ok=false
	// when none exists.
	GetResource(ctx context.Context, groupID uuid.UUID, provider, resourceType, name string) (GroupResource, bool, error)
	// InsertResource claims the row (ON CONFLICT DO NOTHING): inserted=false
	// means another execution already owns it.
	InsertResource(ctx context.Context, groupID uuid.UUID, r GroupResource) (inserted bool, err error)
	// UpdateResourceResult records the external ID and final status.
	UpdateResourceResult(ctx context.Context, groupID uuid.UUID, r GroupResource) error
}

// SlackRepository is the persistence port for the Slack connection (impl:
// internal/store). The bot token is NEVER persisted — only workspace metadata.
type SlackRepository interface {
	// UpsertSlackConnection upserts provider='slack' with account_identifier
	// = team_id, status 'connected', and the bot's Slack user ID; returns the
	// row (stable created_at on repeats thanks to ON CONFLICT DO UPDATE).
	// Safe to repeat.
	UpsertSlackConnection(ctx context.Context, teamID, botUserID string) (SlackStatus, error)
	// MarkSlackError flags the persisted connection as errored.
	MarkSlackError(ctx context.Context) error
	// RecordExternalOperation appends to external_operation_logs (PRD 8.7).
	RecordExternalOperation(ctx context.Context, op ExternalOperation) error
}

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
	Scopes       []string
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
