package integrations

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"launchpad/internal/core"
)

// ── test doubles ───────────────────────────────────────────────────────────

// fakeCipher is an XOR-free stand-in: marks ciphertext so Decrypt reverses it.
type fakeCipher struct{ prefix string }

func (f fakeCipher) Encrypt(p string) (string, error) { return f.prefix + ":" + p, nil }
func (f fakeCipher) Decrypt(e string) (string, error) {
	if !strings.HasPrefix(e, f.prefix+":") {
		return "", errors.New("cipher: wrong key")
	}
	return strings.TrimPrefix(e, f.prefix+":"), nil
}
func (f fakeCipher) KeyID() string { return "test-key" }

// fakeGateway records interactions and returns canned results.
type fakeGateway struct {
	authCodeURLFn func(state, challenge string) string // optional override
	exchange      func(code, verifier string) (GoogleTokenBundle, error)
	refreshFn     func(refreshToken string) (string, time.Time, error)
	refreshes     atomic.Int64
	block         chan struct{} // closed to release in-flight refreshes

	mu        sync.Mutex
	lastState string
}

func (g *fakeGateway) AuthCodeURL(state, challenge string) string {
	g.mu.Lock()
	g.lastState = state
	g.mu.Unlock()
	if g.authCodeURLFn != nil {
		return g.authCodeURLFn(state, challenge)
	}
	return "https://accounts.google.com/o/oauth2/v2/auth?state=" + state + "&code_challenge=" + challenge
}

// stateOf returns the state the service last generated.
func (g *fakeGateway) stateOf() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastState
}
func (g *fakeGateway) Exchange(_ context.Context, code, verifier string) (GoogleTokenBundle, error) {
	return g.exchange(code, verifier)
}
func (g *fakeGateway) Refresh(_ context.Context, refreshToken string) (string, time.Time, error) {
	g.refreshes.Add(1)
	if g.block != nil {
		<-g.block // hold the first refresh to prove single-flight
	}
	return g.refreshFn(refreshToken)
}

// fakeRepo is an in-memory GoogleAuthRepository.
type fakeRepo struct {
	mu          sync.Mutex
	states      map[string]storedState
	consumed    map[string]bool
	connection  *StoredGoogleConnection
	markedError bool
}

type storedState struct {
	verifierEnc string
	expiresAt   time.Time
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{states: map[string]storedState{}, consumed: map[string]bool{}}
}

func (r *fakeRepo) CreateState(_ context.Context, hash, verifierEnc string, expiresAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[hash] = storedState{verifierEnc: verifierEnc, expiresAt: expiresAt}
	return nil
}

func (r *fakeRepo) ConsumeState(_ context.Context, hash string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.states[hash]
	if !ok || r.consumed[hash] || time.Now().After(st.expiresAt) {
		return "", core.Validation("invalid or expired state", nil)
	}
	r.consumed[hash] = true
	return st.verifierEnc, nil
}

func (r *fakeRepo) SaveConnection(_ context.Context, email string, scopes []string, rtEnc, keyID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connection = &StoredGoogleConnection{
		Status:          GoogleStatus{Status: StatusConnected, AccountEmail: email, Scopes: scopes, ConnectedAt: time.Now().UTC()},
		RefreshTokenEnc: rtEnc,
		KeyID:           keyID,
	}
	return nil
}

func (r *fakeRepo) GoogleConnection(context.Context) (StoredGoogleConnection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.connection == nil {
		return StoredGoogleConnection{Status: GoogleStatus{Status: StatusDisconnected}}, nil
	}
	return *r.connection, nil
}

func (r *fakeRepo) MarkGoogleError(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markedError = true
	r.connection.Status.Status = StatusError
	return nil
}

func newService(repo *fakeRepo, gw *fakeGateway) *GoogleAuthService {
	return NewGoogleAuthService(repo, gw, fakeCipher{prefix: "k1"}, true)
}

// ── tests ──────────────────────────────────────────────────────────────────

func TestNotConfiguredRefusesEverything(t *testing.T) {
	svc := NewGoogleAuthService(newFakeRepo(), &fakeGateway{}, nil, false)
	if _, err := svc.ConnectURL(context.Background()); err == nil {
		t.Error("ConnectURL must fail when unconfigured")
	}
	if _, err := svc.Callback(context.Background(), "code", "state"); err == nil {
		t.Error("Callback must fail when unconfigured")
	}
	if _, err := svc.AccessToken(context.Background()); err == nil {
		t.Error("AccessToken must fail when unconfigured")
	}
	appErr, ok := err(t, func() error { _, e := svc.ConnectURL(context.Background()); return e }).(*core.AppError)
	if ok && appErr.Code != core.CodeNotReady {
		t.Errorf("code = %q, want NOT_READY", appErr.Code)
	}
}

// err is a tiny helper so the assertion above stays readable.
func err(t *testing.T, fn func() error) error {
	t.Helper()
	return fn()
}

func TestConnectURLStoresHashedStateAndEncryptedVerifier(t *testing.T) {
	repo := newFakeRepo()
	var gotChallenge string
	gw := &fakeGateway{authCodeURLFn: func(state, challenge string) string {
		gotChallenge = challenge
		return "https://accounts.google.com/o/oauth2/v2/auth?state=" + state
	}}
	svc := newService(repo, gw)

	url, err := svc.ConnectURL(context.Background())
	if err != nil {
		t.Fatalf("ConnectURL: %v", err)
	}
	if !strings.Contains(url, "accounts.google.com") {
		t.Errorf("url = %q", url)
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.states) != 1 {
		t.Fatalf("stored %d states, want 1", len(repo.states))
	}
	for hash, st := range repo.states {
		if hash == url { // raw state must not be stored — only its SHA-256
			t.Error("raw state appears to be stored directly")
		}
		if !strings.HasPrefix(st.verifierEnc, "k1:") {
			t.Errorf("verifier not stored encrypted: %q", st.verifierEnc)
		}
		if len(hash) != 64 { // hex sha256
			t.Errorf("state hash length = %d, want 64", len(hash))
		}
	}
	// S256 challenge: base64url(sha256(verifier)), 43 chars, no padding
	if len(gotChallenge) != 43 || strings.ContainsAny(gotChallenge, "+/=") {
		t.Errorf("S256 challenge malformed: %q", gotChallenge)
	}
}

func TestCallbackHappyPathStoresEncryptedRefreshToken(t *testing.T) {
	repo := newFakeRepo()
	var gotVerifier string
	gw := &fakeGateway{exchange: func(code, verifier string) (GoogleTokenBundle, error) {
		if code != "auth-code" {
			t.Errorf("code = %q", code)
		}
		gotVerifier = verifier
		return GoogleTokenBundle{
			AccessToken: "at", RefreshToken: "rt-secret", Expiry: time.Now().Add(time.Hour),
			AccountEmail: "coordinator@org.example",
		}, nil
	}}
	svc := newService(repo, gw)

	if _, err := svc.ConnectURL(context.Background()); err != nil {
		t.Fatalf("ConnectURL: %v", err)
	}
	state := gw.stateOf()

	status, err := svc.Callback(context.Background(), "auth-code", state)
	if err != nil {
		t.Fatalf("Callback: %v", err)
	}
	if status.Status != StatusConnected || status.AccountEmail != "coordinator@org.example" {
		t.Errorf("status = %+v", status)
	}
	if len(gotVerifier) != 43 {
		t.Errorf("PKCE verifier should be 43 chars, got %d", len(gotVerifier))
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.connection == nil || repo.connection.RefreshTokenEnc != "k1:rt-secret" {
		t.Errorf("refresh token not stored encrypted: %+v", repo.connection)
	}
	if repo.connection.KeyID != "test-key" {
		t.Errorf("keyID = %q", repo.connection.KeyID)
	}
}

func TestCallbackRejectsReusedUnknownAndExpiredState(t *testing.T) {
	tests := []struct {
		name    string
		expired bool
		reuse   bool
		unknown bool // use a state that was never issued
	}{
		{"reused state", false, true, false},
		{"expired state", true, false, false},
		{"unknown state", false, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo()
			gw := &fakeGateway{exchange: func(code, verifier string) (GoogleTokenBundle, error) {
				return GoogleTokenBundle{AccessToken: "at", RefreshToken: "rt", AccountEmail: "x@y.z"}, nil
			}}
			svc := newService(repo, gw)

			if _, err := svc.ConnectURL(context.Background()); err != nil {
				t.Fatalf("ConnectURL: %v", err)
			}
			state := gw.stateOf()
			if tc.unknown {
				state = "never-issued-state"
			}

			if tc.expired {
				repo.mu.Lock()
				for h, st := range repo.states {
					st.expiresAt = time.Now().Add(-time.Minute)
					repo.states[h] = st
				}
				repo.mu.Unlock()
			}
			if tc.reuse {
				if _, err := svc.Callback(context.Background(), "code", state); err != nil {
					t.Fatalf("first use should succeed: %v", err)
				}
			}

			_, err := svc.Callback(context.Background(), "code", state)
			appErr, ok := err.(*core.AppError)
			if !ok || appErr.Code != core.CodeValidation {
				t.Fatalf("expected VALIDATION_ERROR, got %v", err)
			}
		})
	}
}

func TestCallbackSurfacesExchangeFailureSanitized(t *testing.T) {
	repo := newFakeRepo()
	gw := &fakeGateway{exchange: func(code, verifier string) (GoogleTokenBundle, error) {
		return GoogleTokenBundle{}, errors.New("oauth2: cannot fetch token: 400 Bad Request Response: {\"error\":\"invalid_grant\"}")
	}}
	svc := newService(repo, gw)

	if _, err := svc.ConnectURL(context.Background()); err != nil {
		t.Fatalf("ConnectURL: %v", err)
	}
	state := gw.stateOf()

	_, err := svc.Callback(context.Background(), "code", state)
	appErr, ok := err.(*core.AppError)
	if !ok || appErr.Code != core.CodeOAuthExchange {
		t.Fatalf("expected OAUTH_EXCHANGE_FAILED, got %v", err)
	}
	if strings.Contains(appErr.Message, "400") || strings.Contains(appErr.Message, "invalid_grant") {
		t.Errorf("exchange error leaks transport detail: %q", appErr.Message)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.connection != nil {
		t.Error("failed exchange must not store a connection")
	}
}

func TestCallbackRejectsMissingRefreshToken(t *testing.T) {
	repo := newFakeRepo()
	gw := &fakeGateway{exchange: func(code, verifier string) (GoogleTokenBundle, error) {
		return GoogleTokenBundle{AccessToken: "at", RefreshToken: "", AccountEmail: "x@y.z"}, nil // no offline grant
	}}
	svc := newService(repo, gw)
	if _, err := svc.ConnectURL(context.Background()); err != nil {
		t.Fatalf("ConnectURL: %v", err)
	}
	state := gw.stateOf()

	_, err := svc.Callback(context.Background(), "code", state)
	appErr, ok := err.(*core.AppError)
	if !ok || appErr.Code != core.CodeOAuthExchange {
		t.Fatalf("expected OAUTH_EXCHANGE_FAILED, got %v", err)
	}
}

func TestStatusDisconnectedWhenNoConnection(t *testing.T) {
	svc := newService(newFakeRepo(), &fakeGateway{})
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Status != StatusDisconnected {
		t.Errorf("status = %q, want disconnected", st.Status)
	}
}

func TestAccessTokenLifecycle(t *testing.T) {
	repo := newFakeRepo()
	repo.connection = &StoredGoogleConnection{
		Status:          GoogleStatus{Status: StatusConnected, AccountEmail: "x@y.z"},
		RefreshTokenEnc: "k1:stored-rt",
		KeyID:           "test-key",
	}
	gw := &fakeGateway{refreshFn: func(rt string) (string, time.Time, error) {
		if rt != "stored-rt" {
			t.Errorf("refresh called with %q, want decrypted stored-rt", rt)
		}
		return "fresh-at", time.Now().Add(time.Hour), nil
	}}
	svc := newService(repo, gw)

	// First call: no cache -> refresh once
	tok, err := svc.AccessToken(context.Background())
	if err != nil || tok != "fresh-at" {
		t.Fatalf("AccessToken: %q err=%v", tok, err)
	}
	// Second call: cached -> no extra refresh
	if _, err := svc.AccessToken(context.Background()); err != nil {
		t.Fatalf("cached AccessToken: %v", err)
	}
	if got := gw.refreshes.Load(); got != 1 {
		t.Errorf("refresh count = %d, want 1 (cache miss only)", got)
	}
}

func TestAccessTokenRevokedMarksConnectionError(t *testing.T) {
	repo := newFakeRepo()
	repo.connection = &StoredGoogleConnection{
		Status:          GoogleStatus{Status: StatusConnected, AccountEmail: "x@y.z"},
		RefreshTokenEnc: "k1:stored-rt",
	}
	gw := &fakeGateway{refreshFn: func(string) (string, time.Time, error) {
		return "", time.Time{}, ErrRefreshRevoked
	}}
	svc := newService(repo, gw)

	_, err := svc.AccessToken(context.Background())
	appErr, ok := err.(*core.AppError)
	if !ok || appErr.Code != core.CodeNotReady {
		t.Fatalf("expected NOT_READY, got %v", err)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if !repo.markedError || repo.connection.Status.Status != StatusError {
		t.Error("revoked refresh must mark the connection errored")
	}
}

func TestAccessTokenFailsWhenDisconnected(t *testing.T) {
	svc := newService(newFakeRepo(), &fakeGateway{})
	if _, err := svc.AccessToken(context.Background()); err == nil {
		t.Error("expected error when not connected")
	}
}

func TestAccessTokenRefreshIsSingleFlight(t *testing.T) {
	repo := newFakeRepo()
	repo.connection = &StoredGoogleConnection{
		Status:          GoogleStatus{Status: StatusConnected, AccountEmail: "x@y.z"},
		RefreshTokenEnc: "k1:stored-rt",
	}
	block := make(chan struct{})
	gw := &fakeGateway{
		block:     block,
		refreshFn: func(string) (string, time.Time, error) { return "shared-at", time.Now().Add(time.Hour), nil },
	}
	svc := newService(repo, gw)

	const callers = 8
	var wg sync.WaitGroup
	results := make([]string, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := svc.AccessToken(context.Background())
			if err != nil {
				t.Errorf("AccessToken: %v", err)
			}
			results[i] = tok
		}()
	}
	time.Sleep(50 * time.Millisecond) // let all callers pile onto the lock
	close(block)                      // release the single in-flight refresh
	wg.Wait()

	if got := gw.refreshes.Load(); got != 1 {
		t.Errorf("refresh count = %d, want 1 (single-flight violated)", got)
	}
	for i, r := range results {
		if r != "shared-at" {
			t.Errorf("caller %d got %q", i, r)
		}
	}
}

func TestGmailCredentialsRequireGrantAndTrackReconnectedAccount(t *testing.T) {
	repo := newFakeRepo()
	if err := repo.SaveConnection(t.Context(), "first@example.test", GoogleScopes, "k1:first", "test-key"); err != nil {
		t.Fatal(err)
	}
	gateway := &fakeGateway{refreshFn: func(token string) (string, time.Time, error) {
		return token + "-access", time.Now().Add(time.Hour), nil
	}}
	service := newService(repo, gateway)
	if _, err := service.AccessToken(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.CheckGmailPermission(t.Context()); err == nil {
		t.Fatal("Gmail scope should be required")
	}
	if err := repo.SaveConnection(t.Context(), "other@example.test", []string{GmailSendScope}, "k1:other", "test-key"); err != nil {
		t.Fatal(err)
	}
	token, from, err := service.GmailCredentials(t.Context())
	if err != nil || token != "other-access" || from != "other@example.test" {
		t.Fatalf("Gmail credentials did not use the current connection: sender=%q err=%v", from, err)
	}
}
