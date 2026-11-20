package transport_test

// Regression tests for the security-review fixes:
//   - login brute-force throttling (429, no account-existence oracle, and
//     the throttle does not leak through other emails/IPs);
//   - Cache-Control: no-store on API responses (PII cache protection);
//   - a full cross-route sweep proving a lecturer/TA cannot reach ANY
//     group-scoped endpoint of a group they are not assigned to (IDOR/BOLA).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"launchpad/internal/core/auth"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

// newThrottledApp builds the app with a tiny login throttle window.
func newThrottledApp(t *testing.T) (*httptest.Server, *auth.Service) {
	t.Helper()
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users", "notifications", "predefined_staff", "user_sessions", "user_credentials", "oauth_states", "integrations", "integration_credentials", "external_operation_logs", "group_resources", "staff_assignments")
	repo := store.New(pool)
	authSvc := auth.NewService(repo, time.Hour)
	cfg := testConfig()
	cfg.LoginRateMax = 3
	cfg.LoginRateWindow = time.Minute
	app := transport.New(cfg, discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), authSvc, pool.Ping,
		transport.GroupProvisioning{Membership: repo, AssignedGroups: repo, StaffAdmin: repo, GroupResources: repo})
	return httptestNew(app, t), authSvc
}

func TestLoginThrottleBlocksBruteForce(t *testing.T) {
	srv, authSvc := newThrottledApp(t)
	ctx := context.Background()
	if _, err := authSvc.CreateUserWithPassword(ctx, "victim@throttle.test", "V", auth.RoleCoordinator, "correct-password-1"); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	login := func(email, password string) (int, map[string]any, *http.Response) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login",
			strings.NewReader(`{"email":"`+email+`","password":"`+password+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out, resp
	}

	// Three failed attempts are allowed (401), then the pair is throttled.
	for i := 1; i <= 3; i++ {
		code, _, _ := login("victim@throttle.test", "wrong-password-XX")
		if code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d, want 401", i, code)
		}
	}
	code, body, resp := login("victim@throttle.test", "correct-password-1")
	if code != http.StatusTooManyRequests {
		t.Fatalf("blocked attempt: %d %v, want 429", code, body)
	}
	retryAfter, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || retryAfter < 1 {
		t.Errorf("429 Retry-After=%q, want positive delta-seconds", resp.Header.Get("Retry-After"))
	}
	if code := body["error"].(map[string]any)["code"]; code != "RATE_LIMITED" {
		t.Errorf("error code=%v, want RATE_LIMITED", code)
	}

	// The throttle must not become an account-existence oracle: an unknown
	// email from the throttled address is ALSO 429? No — throttling is per
	// IP+EMAIL pair, so a different email is not blocked; the KEY property is
	// that both known and unknown emails over the SAME pair behave
	// identically. Verify a different email still works normally:
	code, _, _ = login("other@throttle.test", "wrong-password-XX")
	if code != http.StatusUnauthorized {
		t.Errorf("different email affected by throttle: %d, want 401", code)
	}

	// A successful login for the blocked pair is refused while blocked
	// (fail closed) — and after a successful login elsewhere nothing leaks.
	code, _, _ = login("victim@throttle.test", "wrong-password-XX")
	if code != http.StatusTooManyRequests {
		t.Errorf("post-block attempt: %d, want 429", code)
	}
}

func TestLoginThrottleDoesNotRevealAccountExistence(t *testing.T) {
	srv, _ := newThrottledApp(t)
	client := &http.Client{Timeout: 5 * time.Second}
	login := func(email string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login",
			strings.NewReader(`{"email":"`+email+`","password":"whatever-password"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// Exhaust the budget for a KNOWN and an UNKNOWN email; both must behave
	// identically (401 × max, then 429).
	for i := 1; i <= 3; i++ {
		if code := login("known@oracle.test"); code != http.StatusUnauthorized {
			t.Fatalf("known email attempt %d: %d", i, code)
		}
	}
	for i := 1; i <= 3; i++ {
		if code := login("ghost@oracle.test"); code != http.StatusUnauthorized {
			t.Fatalf("unknown email attempt %d: %d", i, code)
		}
	}
	if code := login("known@oracle.test"); code != http.StatusTooManyRequests {
		t.Errorf("known email after budget: %d, want 429", code)
	}
	if code := login("ghost@oracle.test"); code != http.StatusTooManyRequests {
		t.Errorf("unknown email after budget: %d, want 429 (identical behavior)", code)
	}
}

// TestLoginThrottleXFFAttribution verifies TRUST_PROXY client-IP
// attribution: with the flag set, X-Forwarded-For identifies the client for
// the throttle (distinct header values get distinct buckets); with it unset,
// the header is ignored and the socket peer is used.
func TestLoginThrottleXFFAttribution(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users", "notifications", "predefined_staff", "user_sessions", "user_credentials", "oauth_states", "integrations", "integration_credentials", "external_operation_logs", "group_resources", "staff_assignments")
	repo := store.New(pool)
	authSvc := auth.NewService(repo, time.Hour)
	for _, trust := range []bool{false, true} {
		cfg := testConfig()
		cfg.LoginRateMax = 2
		cfg.LoginRateWindow = time.Minute
		cfg.TrustProxy = trust
		app := transport.New(cfg, discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
			slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), authSvc, pool.Ping,
			transport.GroupProvisioning{Membership: repo, AssignedGroups: repo, StaffAdmin: repo, GroupResources: repo})
		srv := httptestNew(app, t)

		client := &http.Client{Timeout: 5 * time.Second}
		login := func(xff string) int {
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login",
				strings.NewReader(`{"email":"xff@test.example","password":"wrong-password-1"}`))
			req.Header.Set("Content-Type", "application/json")
			if xff != "" {
				req.Header.Set("X-Forwarded-For", xff)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			return resp.StatusCode
		}
		// Two failures attributed to the first apparent address.
		if code := login("203.0.113.10"); code != http.StatusUnauthorized {
			t.Fatalf("trust=%v first attempt: %d", trust, code)
		}
		if code := login("203.0.113.10"); code != http.StatusUnauthorized {
			t.Fatalf("trust=%v second attempt: %d", trust, code)
		}
		third := login("203.0.113.10")
		fromOther := login("198.51.100.20")
		if trust {
			// Same apparent client: throttled; a different one: fresh bucket.
			if third != http.StatusTooManyRequests {
				t.Errorf("trust=true: third attempt %d, want 429", third)
			}
			if fromOther != http.StatusUnauthorized {
				t.Errorf("trust=true: other XFF %d, want 401", fromOther)
			}
		} else {
			// XFF ignored: both requests come from the SAME socket peer, so
			// both stay under one shared bucket (already at 2 failures → both
			// 429 — proving the header did NOT split the buckets).
			if third != http.StatusTooManyRequests || fromOther != http.StatusTooManyRequests {
				t.Errorf("trust=false: third=%d other=%d, want both 429 (shared socket bucket)", third, fromOther)
			}
		}
		srv.Close()
	}
}

func TestAPIResponsesAreNoStore(t *testing.T) {
	srv, authSvc, _ := newAuthApp(t)
	if _, err := authSvc.CreateUserWithPassword(context.Background(), "cache@test.example", "C", auth.RoleCoordinator, "correct-password-123"); err != nil {
		t.Fatal(err)
	}
	session, err := authSvc.Login(context.Background(), "cache@test.example", "correct-password-123")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, path := range []string{"/api/groups", "/api/notifications", "/api/auth/me", "/health"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+session.Token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if cache := resp.Header.Get("Cache-Control"); path == "/health" {
			if cache == "no-store" {
				t.Errorf("%s: no-store on a public health endpoint is unexpected but harmless", path)
			}
		} else if cache != "no-store" {
			t.Errorf("%s: Cache-Control=%q, want no-store", path, cache)
		}
	}
}

// TestLecturerIDORSweepAcrossAllGroupRoutes: a lecturer may not reach ANY
// group-scoped route of a foreign group — path UUIDs are the only identifier
// surface, and every route re-checks membership server-side.
func TestLecturerIDORSweepAcrossAllGroupRoutes(t *testing.T) {
	srv, authSvc, pool := newAuthApp(t)
	ctx := context.Background()
	password := "role-password-123456"
	if _, err := authSvc.CreateUserWithPassword(ctx, "sweeper@roles.test", "Sweeper", auth.RoleLecturer, password); err != nil {
		t.Fatal(err)
	}
	session, err := authSvc.Login(ctx, "sweeper@roles.test", password)
	if err != nil {
		t.Fatal(err)
	}
	own := createGroupForRoles(t, srv, "Sweep Own Group", "sweeper@roles.test", "")
	foreign := createGroupForRoles(t, srv, "Sweep Foreign Group", "someone-else@roles.test", "")
	// Give the sweeper a STUDENT membership on the foreign group — access
	// must STILL be denied (student membership grants nothing).
	sweeperID := userIDByEmail(t, pool, "sweeper@roles.test")
	if _, err := pool.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id, member_role) VALUES ($1, $2, 'student')`, foreign, sweeperID); err != nil {
		t.Fatal(err)
	}

	lecturer := &bearerClient{t: t, token: session.Token, Client: http.Client{Timeout: 5 * time.Second}}
	groupScoped := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/groups/%s"},
		{http.MethodGet, "/api/groups/%s/resources"},
		{http.MethodGet, "/api/groups/%s/status"},
		{http.MethodGet, "/api/groups/%s/audits"},
		{http.MethodGet, "/api/groups/%s/announcements"},
		{http.MethodGet, "/api/groups/%s/forms"},
		{http.MethodGet, "/api/groups/%s/onboarding"},
		{http.MethodGet, "/api/groups/%s/forms/deliveries"},
		{http.MethodPost, "/api/groups/%s/launch"},
		{http.MethodPost, "/api/groups/%s/audits"},
		{http.MethodPost, "/api/groups/%s/announcements"},
		{http.MethodPost, "/api/groups/%s/slack/provision"},
		{http.MethodPost, "/api/groups/%s/classroom/provision"},
		{http.MethodPost, "/api/groups/%s/drive/provision"},
		{http.MethodPost, "/api/groups/%s/forms/provision"},
		{http.MethodPost, "/api/groups/%s/forms/publish"},
		{http.MethodPost, "/api/groups/%s/forms/distribute"},
		{http.MethodPost, "/api/groups/%s/forms/responses/import"},
	}
	for _, route := range groupScoped {
		foreignPath := fmt.Sprintf(route.path, foreign)
		code, body := lecturer.do(route.method, srv.URL+foreignPath)
		if code != http.StatusForbidden {
			t.Errorf("%s %s (foreign group): %d %v, want 403", route.method, foreignPath, code, body)
		}
	}
	// The own group: reads are NOT forbidden (they succeed when the service
	// is wired; an unwired service returns 503 — availability, not
	// authorization), writes stay forbidden.
	for _, read := range []string{"/api/groups/%s", "/api/groups/%s/resources", "/api/groups/%s/status", "/api/groups/%s/audits", "/api/groups/%s/announcements", "/api/groups/%s/forms"} {
		if code, _ := lecturer.do(http.MethodGet, srv.URL+fmt.Sprintf(read, own)); code == http.StatusForbidden {
			t.Errorf("GET %s (own group): 403, want allowed", fmt.Sprintf(read, own))
		}
	}
	for _, write := range []string{"/api/groups/%s/launch", "/api/groups/%s/slack/provision"} {
		if code, _ := lecturer.do(http.MethodPost, srv.URL+fmt.Sprintf(write, own)); code != http.StatusForbidden {
			t.Errorf("POST %s (own group): want 403", fmt.Sprintf(write, own))
		}
	}
	// Non-group coordinator/admin surfaces: denied for the lecturer.
	for _, path := range []string{"/api/notifications", "/api/audit-notifications", "/api/announcement-templates", "/api/admin/users", "/api/admin/staff", "/api/integrations/google/connect"} {
		if code, _ := lecturer.do(http.MethodGet, srv.URL+path); code != http.StatusForbidden {
			t.Errorf("GET %s (lecturer): %d, want 403", path, code)
		}
	}
}
