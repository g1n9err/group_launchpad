package transport_test

// HTTP-surface tests for the Google integration endpoints. The OAuth state
// lifecycle is exercised against real PostgreSQL; Google itself is replaced
// by the labeled fake gateway in integrations_fake_test.go.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"launchpad/internal/core/integrations"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

// newAppUnconfigured builds the app with Google OAuth disabled (503 paths).
func newAppUnconfigured(t *testing.T) *httptest.Server {
	t.Helper()
	pool := newTestPool(t)
	truncate(t, pool, "groups", "oauth_states", "integrations", "integration_credentials", "external_operation_logs", "group_resources")
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool),
		integrations.NewGoogleAuthService(store.New(pool), nil, nil, false), slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), nil, pool.Ping)
	return httptestNew(app, t)
}

// newAppNoSlack builds the app with Slack disabled (503 path).
func newAppNoSlack(t *testing.T) *httptest.Server {
	t.Helper()
	pool := newTestPool(t)
	truncate(t, pool, "groups", "oauth_states", "integrations", "integration_credentials", "external_operation_logs", "group_resources")
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		integrations.NewSlackAuthService(store.New(pool), nil, false),
		integrations.NewSlackChannelsService(nil, nil, store.New(pool), false), slackEventsServiceForTest(pool), nil, pool.Ping)
	return httptestNew(app, t)
}

func TestGoogleConnectRedirectsToConsent(t *testing.T) {
	srv := newApp(t)
	resp := rawGet(t, srv, "/api/integrations/google/connect")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "https://accounts.google.com/") {
		t.Fatalf("Location = %q", loc)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	q := u.Query()
	if q.Get("state") == "" || q.Get("code_challenge") == "" {
		t.Errorf("missing state/code_challenge in %q", loc)
	}
	if !strings.HasSuffix(u.Path, "/auth") {
		t.Errorf("unexpected auth path %q", u.Path)
	}
}

func TestGoogleFlowCallbackAndStatusEndToEnd(t *testing.T) {
	srv := newApp(t)

	// 1. Start the flow — capture state from the redirect.
	resp := rawGet(t, srv, "/api/integrations/google/connect")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("connect status = %d", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	resp.Body.Close()

	// 2. Complete the flow with the fake gateway.
	cb := srv.URL + "/api/integrations/google/callback?code=test-code&state=" + loc.Query().Get("state")
	status, body := doReq(t, srv.Client(), http.MethodGet, cb)
	if status != http.StatusOK {
		t.Fatalf("callback status = %d (body %v)", status, body)
	}
	if body["status"] != "connected" || body["accountEmail"] != "coordinator@test.example" {
		t.Errorf("callback body = %v", body)
	}

	// 3. Status reflects the stored connection.
	status, body = doJSON(t, srv, http.MethodGet, "/api/integrations/google/status", "")
	if status != http.StatusOK {
		t.Fatalf("status endpoint = %d", status)
	}
	if body["status"] != "connected" || body["accountEmail"] != "coordinator@test.example" {
		t.Errorf("status body = %v", body)
	}
	scopes, ok := body["scopes"].([]any)
	if !ok || len(scopes) != len(integrations.GoogleScopes) {
		t.Errorf("scopes = %v", body["scopes"])
	}
}

func TestGoogleCallbackRejectsReusedState(t *testing.T) {
	srv := newApp(t)
	resp := rawGet(t, srv, "/api/integrations/google/connect")
	loc, _ := url.Parse(resp.Header.Get("Location"))
	resp.Body.Close()
	state := loc.Query().Get("state")

	cb := "/api/integrations/google/callback?code=test-code&state=" + state
	if s, _ := doJSON(t, srv, http.MethodGet, cb, ""); s != http.StatusOK {
		t.Fatal("first callback should succeed")
	}
	s, body := doJSON(t, srv, http.MethodGet, cb, "")
	if s != http.StatusUnprocessableEntity {
		t.Fatalf("reuse status = %d, want 422", s)
	}
	if code := errCode(t, body); code != "VALIDATION_ERROR" {
		t.Errorf("code = %q", code)
	}
}

func TestGoogleCallbackUnknownStateRejected(t *testing.T) {
	srv := newApp(t)
	s, body := doJSON(t, srv, http.MethodGet, "/api/integrations/google/callback?code=c&state=bogus", "")
	if s != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", s)
	}
	if code := errCode(t, body); code != "VALIDATION_ERROR" {
		t.Errorf("code = %q", code)
	}
}

func TestGoogleCallbackConsentDenied(t *testing.T) {
	srv := newApp(t)
	s, body := doJSON(t, srv, http.MethodGet, "/api/integrations/google/callback?error=access_denied", "")
	if s != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", s)
	}
	if code := errCode(t, body); code != "VALIDATION_ERROR" {
		t.Errorf("code = %q", code)
	}
	if !strings.Contains(body["error"].(map[string]any)["message"].(string), "access_denied") {
		t.Errorf("message should echo provider code: %v", body)
	}
}

func TestGoogleEndpointsNotConfigured(t *testing.T) {
	srv := newAppUnconfigured(t)
	for _, path := range []string{"/api/integrations/google/connect", "/api/integrations/google/status"} {
		s, body := doJSON(t, srv, http.MethodGet, path, "")
		if s != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d, want 503", path, s)
		}
		if code := errCode(t, body); code != "NOT_READY" {
			t.Errorf("%s code = %q", path, code)
		}
	}
}
