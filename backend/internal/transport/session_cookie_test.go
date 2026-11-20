package transport_test

// Persistent sign-in (HttpOnly session cookie) lifecycle:
//   login sets the cookie → resume rotates it one-time → replay fails →
//   logout (cookie-only) revokes and clears → resume is dead.
// The cookie must be HttpOnly, Path=/api/auth and SameSite=Strict, and must
// NEVER be observable via document.cookie semantics (it is opaque to JS).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"launchpad/internal/core/auth"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

func TestSessionCookieResumeLifecycle(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users", "user_sessions", "user_credentials")
	repo := store.New(pool)
	authSvc := auth.NewService(repo, time.Hour)
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), authSvc, pool.Ping)
	srv := httptestNew(app, t)

	ctx := context.Background()
	if _, err := authSvc.CreateUserWithPassword(ctx, "resume@test.example", "R", auth.RoleCoordinator, "correct-password-1"); err != nil {
		t.Fatal(err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	var body map[string]any
	do := func(method, path string) (*http.Response, map[string]any) {
		req, err := http.NewRequest(method, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var parsed map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&parsed)
		return resp, parsed
	}

	// 1. Login sets the HttpOnly session cookie with the right attributes.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login",
		strings.NewReader(`{"email":"resume@test.example","password":"correct-password-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %v %v", resp, err)
	}
	resp.Body.Close()
	// Attribute assertions against the RAW Set-Cookie header (the cookie
	// jar applies but does not expose attributes).
	setCookie := resp.Header.Get("Set-Cookie")
	for _, want := range []string{"HttpOnly", "Path=/api/auth", "SameSite=Strict", "launchpad_session="} {
		if !strings.Contains(setCookie, want) {
			t.Errorf("Set-Cookie missing %q: %s", want, setCookie)
		}
	}
	cookies := jar.Cookies(mustURL(t, srv.URL+"/api/auth/resume"))
	if len(cookies) != 1 {
		t.Fatalf("session cookies after login = %d, want 1", len(cookies))
	}
	firstCookieValue := cookies[0].Value

	// 2. Resume with the cookie (no Authorization header) rotates it.
	resp, body = do(http.MethodPost, "/api/auth/resume")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resume: %d %v", resp.StatusCode, body)
	}
	if _, hasToken := body["token"]; !hasToken {
		t.Fatal("resume response has no token")
	}
	rotated := jar.Cookies(mustURL(t, srv.URL+"/api/auth/resume"))[0].Value
	if rotated == firstCookieValue || rotated == "" {
		t.Fatal("resume did not rotate the cookie")
	}

	// 3. Replay of the OLD cookie value fails (one-time use) — the server
	// cleared it and answered 401.
	jar.SetCookies(mustURL(t, srv.URL+"/api/auth/resume"), []*http.Cookie{{
		Name: "launchpad_session", Value: firstCookieValue, Path: "/api/auth",
	}})
	if resp, body = do(http.MethodPost, "/api/auth/resume"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed cookie resumed: %d %v", resp.StatusCode, body)
	}

	// 4. Fresh login again, then logout with ONLY the cookie (no header):
	// session revoked, cookie cleared, resume dead.
	req2, err2 := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login",
		strings.NewReader(`{"email":"resume@test.example","password":"correct-password-1"}`))
	if err2 != nil {
		t.Fatal(err2)
	}
	req2.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req2)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("second login: %v", err)
	}
	resp.Body.Close()
	if resp, body = do(http.MethodPost, "/api/auth/logout"); resp.StatusCode != http.StatusOK {
		t.Fatalf("cookie-only logout: %d %v", resp.StatusCode, body)
	}
	if resp, body = do(http.MethodPost, "/api/auth/resume"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("resume after logout: %d %v", resp.StatusCode, body)
	}
	if remaining := jar.Cookies(mustURL(t, srv.URL+"/api/auth/resume")); len(remaining) != 0 {
		t.Errorf("cookie not cleared on logout: %+v", remaining)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
