package transport_test

// Tests for the dedicated events-only listener: exactly one route exists,
// everything else 404s/405s, signed requests reach the shared handler, and
// the main API (with its full route set) is unaffected.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"launchpad/internal/core/integrations"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

// newEventsListener builds the events-only app over the real verifier, fixed
// clock, and real PostgreSQL test database.
func newEventsListener(t *testing.T, configured bool) *httptest.Server {
	t.Helper()
	pool := newTestPool(t)
	truncate(t, pool, "slack_events")
	svc := integrations.NewSlackEventsService(slackEventsVerifierForTest(), store.New(pool), nil, testClock, configured)
	app := transport.NewSlackEventsListener(discardLogger(), svc)
	return httptestNew(app, t)
}

// postTo sends a raw signed/unsigned POST to the listener.
func postTo(t *testing.T, srv *httptest.Server, path, ts, body, sig string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if ts != "" {
		req.Header.Set("X-Slack-Request-Timestamp", ts)
		req.Header.Set("X-Slack-Signature", sig)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = decodeJSON(resp, &out)
	return resp.StatusCode, out
}

func TestEventsListenerValidSignedRequestReachesHandler(t *testing.T) {
	srv := newEventsListener(t, true)
	ts := fmt.Sprint(testClock().Unix())
	body := `{"type":"url_verification","challenge":"listener-ok"}`

	code, resp := postTo(t, srv, "/api/integrations/slack/events", ts, body, signBody(ts, body))
	if code != http.StatusOK || resp["challenge"] != "listener-ok" {
		t.Fatalf("code=%d resp=%v — signed request did not reach the shared handler", code, resp)
	}
}

func TestEventsListenerRejectsInvalidSignature(t *testing.T) {
	srv := newEventsListener(t, true)
	ts := fmt.Sprint(testClock().Unix())
	body := `{"type":"url_verification","challenge":"x"}`

	code, resp := postTo(t, srv, "/api/integrations/slack/events", ts, body, "v0=invalid")
	if code != http.StatusUnauthorized {
		t.Fatalf("code=%d resp=%v, want 401", code, resp)
	}
	if c := errCode(t, resp); c != "UNAUTHORIZED" {
		t.Errorf("code=%q", c)
	}
}

func TestEventsListenerOnlyExposesTheEventsRoute(t *testing.T) {
	srv := newEventsListener(t, true)

	paths := []string{
		"/health",
		"/readyz",
		"/api/groups",
		"/api/integrations/slack/status",
		"/api/integrations/google/status",
		"/api/integrations/google/connect",
		"/api/groups/00000000-0000-0000-0000-000000000000/slack/provision",
	}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			req, _ := http.NewRequest(method, srv.URL+path, nil)
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404 (route must not exist on the events listener)", method, path, resp.StatusCode)
			}
		}
	}
}

func TestEventsListenerWrongMethodIs405(t *testing.T) {
	srv := newEventsListener(t, true)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/integrations/slack/events", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET on events path = %d, want 405", resp.StatusCode)
	}
}

func TestEventsListenerUnconfiguredStillRefusesEverything(t *testing.T) {
	// Defense in depth: even if an unconfigured listener were ever started,
	// its single route 503s and nothing else exists.
	srv := newEventsListener(t, false)
	ts := fmt.Sprint(testClock().Unix())
	body := `{"type":"url_verification","challenge":"x"}`

	code, resp := postTo(t, srv, "/api/integrations/slack/events", ts, body, signBody(ts, body))
	if code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d resp=%v, want 503", code, resp)
	}
	if c := errCode(t, resp); c != "NOT_READY" {
		t.Errorf("code=%q", c)
	}
}

func TestMainAPIStillServesFullRouteSet(t *testing.T) {
	// The main API (port 8080 in production) is unchanged: full routes,
	// including its own copy of the events endpoint.
	srv := newApp(t)

	if code, _ := doJSON(t, srv, http.MethodGet, "/health", ""); code != http.StatusOK {
		t.Errorf("main /health = %d", code)
	}
	if code, _ := doJSON(t, srv, http.MethodGet, "/api/groups", ""); code != http.StatusOK {
		t.Errorf("main /api/groups = %d", code)
	}
	ts := fmt.Sprint(testClock().Unix())
	body := `{"type":"url_verification","challenge":"main-ok"}`
	code, resp := doJSONSigned(t, srv, "/api/integrations/slack/events", ts, body, signBody(ts, body))
	if code != http.StatusOK || resp["challenge"] != "main-ok" {
		t.Errorf("main events route: code=%d resp=%v", code, resp)
	}
}

// doJSONSigned posts a signed webhook request to any server (shared helper).
func doJSONSigned(t *testing.T, srv *httptest.Server, path, ts, body, sig string) (int, map[string]any) {
	t.Helper()
	return postTo(t, srv, path, ts, body, sig)
}
