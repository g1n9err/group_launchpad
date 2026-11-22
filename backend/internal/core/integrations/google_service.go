package integrations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"launchpad/internal/core"
)

// GoogleAuthService drives the Google OAuth flow and token lifecycle.
// It is credential-gated: constructed with configured=false it refuses every
// operation — nothing is simulated.
type GoogleAuthService struct {
	repo       GoogleAuthRepository
	gateway    GoogleAuthGateway
	cipher     TokenCipher
	configured bool

	// access-token cache; guarded by mu (also serializes refresh: single-flight).
	mu               sync.Mutex
	access           string
	accessExp        time.Time
	cachedCredential string
	workspaceTokens  map[uuid.UUID]googleAccessToken
}

type googleAccessToken struct {
	value      string
	expiry     time.Time
	credential string
}

// NewGoogleAuthService builds the service. configured requires client
// credentials, redirect URL, and an encryption key to be present.
func NewGoogleAuthService(repo GoogleAuthRepository, gateway GoogleAuthGateway, cipher TokenCipher, configured bool) *GoogleAuthService {
	return &GoogleAuthService{repo: repo, gateway: gateway, cipher: cipher, configured: configured}
}

// Configured reports whether Google OAuth is usable in this environment.
func (s *GoogleAuthService) Configured() bool { return s.configured }

func errNotConfigured() error {
	return &core.AppError{Code: core.CodeNotReady, Message: "google oauth is not configured (GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, GOOGLE_REDIRECT_URL, TOKEN_ENCRYPTION_KEY)"}
}

// ConnectURL starts an OAuth flow: it generates a random state and a PKCE
// verifier, persists only the state HASH and the ENCRYPTED verifier (raw
// values never touch the database), and returns the Google consent URL.
func (s *GoogleAuthService) ConnectURL(ctx context.Context) (string, error) {
	if !s.configured {
		return "", errNotConfigured()
	}
	state, err := randomToken(32)
	if err != nil {
		return "", fmt.Errorf("oauth state: %w", err)
	}
	verifier, err := randomToken(32) // 43 chars in base64url — RFC 7636 range
	if err != nil {
		return "", fmt.Errorf("pkce verifier: %w", err)
	}
	verifierEnc, err := s.cipher.Encrypt(verifier)
	if err != nil {
		return "", fmt.Errorf("pkce verifier encrypt: %w", err)
	}
	if err := s.repo.CreateState(ctx, hashHex(state), verifierEnc, time.Now().Add(StateTTL)); err != nil {
		return "", err
	}
	return s.gateway.AuthCodeURL(state, s256Challenge(verifier)), nil
}

// Callback validates the state (single-use, within TTL), exchanges the
// authorization code with the PKCE verifier, and stores the connection with
// the encrypted refresh token. Access token is cached in memory only.
func (s *GoogleAuthService) Callback(ctx context.Context, code, state string) (GoogleStatus, error) {
	if !s.configured {
		return GoogleStatus{}, errNotConfigured()
	}
	if code == "" || state == "" {
		return GoogleStatus{}, core.Validation("missing oauth response parameters", map[string]string{
			"code":  "required",
			"state": "required",
		})
	}

	var verifierEnc string
	var err error
	if scopedRepo, ok := s.repo.(GoogleWorkspaceStateOwnerRepository); ok {
		var owner uuid.UUID
		verifierEnc, owner, err = scopedRepo.ConsumeWorkspaceState(ctx, hashHex(state))
		if owner != uuid.Nil {
			ctx = core.WithWorkspaceOwner(ctx, owner)
		}
	} else {
		verifierEnc, err = s.repo.ConsumeState(ctx, hashHex(state))
	}
	if err != nil {
		return GoogleStatus{}, core.Validation("oauth state is invalid, expired, or already used", nil)
	}
	verifier, err := s.cipher.Decrypt(verifierEnc)
	if err != nil {
		return GoogleStatus{}, core.Validation("oauth state could not be decrypted (encryption key changed?)", nil)
	}

	bundle, err := s.gateway.Exchange(ctx, code, verifier)
	if err != nil {
		// Sanitized on purpose: the underlying transport error can echo
		// request fragments; only the mapped category leaves the service.
		return GoogleStatus{}, &core.AppError{Code: core.CodeOAuthExchange, Message: "google authorization exchange failed"}
	}
	if bundle.RefreshToken == "" {
		// Without a refresh token the connection cannot survive its first
		// hour; treat as a failed exchange rather than storing a dead row.
		return GoogleStatus{}, &core.AppError{Code: core.CodeOAuthExchange, Message: "google did not return a refresh token (consent must be granted with offline access)"}
	}

	refreshTokenEnc, err := s.cipher.Encrypt(bundle.RefreshToken)
	if err != nil {
		return GoogleStatus{}, fmt.Errorf("encrypt refresh token: %w", err)
	}
	scopes := bundle.Scopes
	if scopes == nil {
		scopes = GoogleScopes
	}
	if err := s.repo.SaveConnection(ctx, bundle.AccountEmail, scopes, refreshTokenEnc, s.cipher.KeyID()); err != nil {
		return GoogleStatus{}, err
	}

	s.mu.Lock()
	s.access, s.accessExp, s.cachedCredential = bundle.AccessToken, bundle.Expiry, refreshTokenEnc
	s.mu.Unlock()

	return GoogleStatus{
		Status:       StatusConnected,
		AccountEmail: bundle.AccountEmail,
		Scopes:       scopes,
		ConnectedAt:  time.Now().UTC(),
	}, nil
}

// Status reports the persisted connection state — never any token material.
func (s *GoogleAuthService) Status(ctx context.Context) (GoogleStatus, error) {
	stored, err := s.repo.GoogleConnection(ctx)
	if err != nil {
		return GoogleStatus{}, err
	}
	return stored.Status, nil
}

// AccessToken returns a valid access token for the connected account,
// refreshing ahead of expiry. Concurrent callers share a single refresh
// (single-flight). A permanently rejected refresh token marks the connection
// errored and surfaces a reconnect-required domain error.
func (s *GoogleAuthService) AccessToken(ctx context.Context) (string, error) {
	token, _, err := s.accessToken(ctx, "")
	return token, err
}

// AccessTokenForGroup uses the Google account selected for the group. Groups
// without an explicit selection keep using the legacy default account.
func (s *GoogleAuthService) AccessTokenForGroup(ctx context.Context, groupID uuid.UUID) (string, error) {
	repo, ok := s.repo.(GoogleWorkspaceRepository)
	if !ok {
		return s.AccessToken(ctx)
	}
	id, assigned, err := repo.GroupGoogleWorkspace(ctx, groupID)
	if err != nil {
		return "", err
	}
	if !assigned {
		return s.AccessToken(ctx)
	}
	if !s.configured {
		return "", errNotConfigured()
	}
	credential, err := repo.GoogleWorkspaceCredential(ctx, id)
	if err != nil {
		return "", err
	}
	return s.workspaceAccessToken(ctx, id, credential)
}

// GmailCredentialsForWorkspace pins system email to an operator-selected account.
func (s *GoogleAuthService) GmailCredentialsForWorkspace(ctx context.Context, id uuid.UUID) (string, string, error) {
	repo, ok := s.repo.(GoogleWorkspaceRepository)
	if !s.configured || !ok {
		return "", "", errNotConfigured()
	}
	credential, err := repo.GoogleWorkspaceCredential(ctx, id)
	if err != nil {
		return "", "", err
	}
	if credential.Status != StatusConnected || !slices.Contains(credential.Scopes, GmailSendScope) {
		return "", "", &core.AppError{Code: core.CodeNotReady, Message: "reconnect the selected Google account and allow Gmail sending"}
	}
	token, err := s.workspaceAccessToken(ctx, id, credential)
	return token, credential.AccountEmail, err
}

func (s *GoogleAuthService) workspaceAccessToken(ctx context.Context, id uuid.UUID, credential GoogleWorkspaceCredential) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workspaceTokens == nil {
		s.workspaceTokens = make(map[uuid.UUID]googleAccessToken)
	}
	if cached, found := s.workspaceTokens[id]; found && cached.credential == credential.RefreshTokenEncrypted && cached.value != "" && time.Now().Add(30*time.Second).Before(cached.expiry) {
		return cached.value, nil
	}
	refresh, err := s.cipher.Decrypt(credential.RefreshTokenEncrypted)
	if err != nil {
		return "", &core.AppError{Code: core.CodeNotReady, Message: "stored Google account credential cannot be decrypted — reconnect required"}
	}
	token, expiry, err := s.gateway.Refresh(ctx, refresh)
	if err != nil {
		return "", &core.AppError{Code: core.CodeNotReady, Message: "Google account token refresh failed — reconnect required"}
	}
	s.workspaceTokens[id] = googleAccessToken{value: token, expiry: expiry, credential: credential.RefreshTokenEncrypted}
	return token, nil
}

func (s *GoogleAuthService) CheckGmailPermission(ctx context.Context) error {
	_, err := s.connectionWithScope(ctx, GmailSendScope)
	return err
}

func (s *GoogleAuthService) GmailCredentials(ctx context.Context) (token, from string, err error) {
	return s.accessToken(ctx, GmailSendScope)
}

func (s *GoogleAuthService) connectionWithScope(ctx context.Context, scope string) (StoredGoogleConnection, error) {
	if !s.configured {
		return StoredGoogleConnection{}, errNotConfigured()
	}
	stored, err := s.repo.GoogleConnection(ctx)
	if err != nil {
		return StoredGoogleConnection{}, err
	}
	if stored.Status.Status != StatusConnected || stored.RefreshTokenEnc == "" {
		return StoredGoogleConnection{}, &core.AppError{Code: core.CodeNotReady, Message: "google is not connected — run the connect flow first"}
	}
	if scope != "" && !slices.Contains(stored.Status.Scopes, scope) {
		return StoredGoogleConnection{}, &core.AppError{Code: core.CodeNotReady, Message: "reconnect Google and allow Gmail sending"}
	}
	return stored, nil
}

func (s *GoogleAuthService) accessToken(ctx context.Context, scope string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.connectionWithScope(ctx, scope)
	if err != nil {
		return "", "", err
	}
	if s.cachedCredential == stored.RefreshTokenEnc && s.access != "" && time.Now().Add(30*time.Second).Before(s.accessExp) {
		return s.access, stored.Status.AccountEmail, nil
	}
	refreshToken, err := s.cipher.Decrypt(stored.RefreshTokenEnc)
	if err != nil {
		return "", "", &core.AppError{Code: core.CodeNotReady, Message: "stored google credential cannot be decrypted (encryption key changed?) — reconnect required"}
	}

	token, expiry, err := s.gateway.Refresh(ctx, refreshToken)
	if err != nil {
		if err == ErrRefreshRevoked { // errors.Is not needed: gateway returns it unwrapped
			if markErr := s.repo.MarkGoogleError(ctx); markErr != nil {
				return "", "", fmt.Errorf("mark google errored after rejected refresh: %w", markErr)
			}
			return "", "", &core.AppError{Code: core.CodeNotReady, Message: "google connection expired or was revoked — reconnect required"}
		}
		return "", "", &core.AppError{Code: core.CodeNotReady, Message: "google token refresh failed"}
	}

	s.access, s.accessExp, s.cachedCredential = token, expiry, stored.RefreshTokenEnc
	return s.access, stored.Status.AccountEmail, nil
}

// ── helpers ───────────────────────────────────────────────────────────────

// randomToken returns n random bytes, base64url-encoded without padding.
func randomToken(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// hashHex returns the SHA-256 hex digest — used for state storage so the raw
// state value is never persisted.
func hashHex(v string) string {
	d := sha256.Sum256([]byte(v))
	return hex.EncodeToString(d[:])
}

// s256Challenge derives the RFC 7636 S256 code challenge from a verifier.
func s256Challenge(verifier string) string {
	d := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(d[:])
}
