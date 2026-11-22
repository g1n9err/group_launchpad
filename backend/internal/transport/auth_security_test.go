package transport_test

// Security tests for the coordinator protection on group creation,
// onboarding roster, and Google status email redaction (feat/protect-coordinator-actions).

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// doJSONWithAuthPair is a helper for JSON requests with an auth header.
func doJSONWithAuthPair(t *testing.T, srv *httptest.Server, method, path, body, auth string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// coordinatorAuthHeader returns a valid Basic auth header value for tests.
func coordinatorAuthHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("coordinator:"+testCoordinatorPassword))
}

// TestAnonymousCreateGroupRejected proves POST /api/groups returns 401 for
// anonymous callers and no group/student records are persisted.
func TestAnonymousCreateGroupRejected(t *testing.T) {
	srv := newApp(t)

	// Send a TRULY anonymous request (no Authorization header).
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/groups",
		strings.NewReader(`{"name":"Unauthorized","courseName":"C","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"],"studentEmails":["x@y.z"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous create status = %d (%v), want 401", resp.StatusCode, body)
	}
	if code := body["error"].(map[string]any)["code"]; code != "UNAUTHORIZED" {
		t.Errorf("code = %v, want UNAUTHORIZED", code)
	}

	// Verify no group was created.
	pool := newTestPool(t)
	truncate(t, pool, "groups", "group_members", "users")
	var count int
	if err := pool.QueryRow(ctxbg(), `SELECT count(*) FROM groups WHERE name = 'Unauthorized'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("group was created despite 401: %d rows", count)
	}

	// Verify no user/student was created.
	var users int
	_ = pool.QueryRow(ctxbg(), `SELECT count(*) FROM users WHERE email = 'x@y.z'`).Scan(&users)
	if users != 0 {
		t.Errorf("student user was created despite 401: %d rows", users)
	}
}

// TestAuthorizedCreateGroupWorks proves a coordinator with valid Basic auth
// can create a group with students.
func TestAuthorizedCreateGroupWorks(t *testing.T) {
	srv := newApp(t)

	status, out := doJSONWithAuthPair(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Authorized Group","courseName":"C","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"],"studentEmails":["auth@x.example"]}`,
		coordinatorAuthHeader())

	if status != http.StatusCreated {
		t.Fatalf("authorized create status = %d (%v), want 201", status, out)
	}
	if out["name"] != "Authorized Group" {
		t.Errorf("name = %v", out["name"])
	}
}

// TestAnonymousOnboardingRejected proves GET /api/groups/{id}/onboarding
// returns 401 without credentials and never returns student emails.
func TestAnonymousOnboardingRejected(t *testing.T) {
	srv := newApp(t)

	// Create a group first (with auth).
	_, created := doJSONWithAuthPair(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Onboard Test","courseName":"C","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"],"studentEmails":["p@x.example"]}`,
		coordinatorAuthHeader())
	groupID := created["id"].(string)

	// Anonymous onboarding fetch → 401.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/groups/"+groupID+"/onboarding", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous onboarding status = %d (%v), want 401", resp.StatusCode, out)
	}
	// The response must NOT contain student data.
	if b := fmt.Sprint(out); strings.Contains(b, "p@x.example") || strings.Contains(b, "students") {
		t.Errorf("401 response leaked student data: %v", out)
	}
}

// TestAnonymousGoogleStatusStripsEmail proves the Google status endpoint
// returns connection state but strips accountEmail for anonymous callers.
// Uses a TRULY anonymous request (no Authorization header) and seeds a
// connected status first so the redaction is actually exercised.
func TestAnonymousGoogleStatusStripsEmail(t *testing.T) {
	srv := newApp(t)

	// Seed a connected Google integration so accountEmail would be present
	// if the endpoint didn't strip it.
	pool := newTestPool(t)
	if _, err := pool.Exec(ctxbg(), `
		INSERT INTO integrations (provider, account_identifier, connection_status)
		VALUES ('google', 'coordinator@test.example', 'connected')
	`); err != nil {
		t.Fatalf("seed google connection: %v", err)
	}

	// Send a TRULY anonymous request — no Authorization header.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/integrations/google/status", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous google status = %d (%v), want 200 (public status)", resp.StatusCode, body)
	}
	// Must NOT contain accountEmail — this is the core redaction assertion.
	if _, has := body["accountEmail"]; has {
		t.Errorf("anonymous response contains accountEmail: %v", body["accountEmail"])
	}
	// Must NOT contain scopes.
	if _, has := body["scopes"]; has {
		t.Errorf("anonymous response contains scopes: %v", body["scopes"])
	}
	// Must NOT contain connectedAt.
	if _, has := body["connectedAt"]; has {
		t.Errorf("anonymous response contains connectedAt: %v", body["connectedAt"])
	}
	// Must contain status.
	if _, has := body["status"]; !has {
		t.Error("anonymous response missing status field")
	}

	// Now verify the AUTHORIZED response DOES include the email — proving
	// the stripping is authentication-conditional, not always-absent.
	reqAuth, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/integrations/google/status", nil)
	reqAuth.Header.Set("Authorization", coordinatorAuthHeader())
	respAuth, err := srv.Client().Do(reqAuth)
	if err != nil {
		t.Fatalf("authorized do: %v", err)
	}
	defer respAuth.Body.Close()
	var authBody map[string]any
	_ = json.NewDecoder(respAuth.Body).Decode(&authBody)

	if respAuth.StatusCode != http.StatusOK {
		t.Fatalf("authorized google status = %d", respAuth.StatusCode)
	}
	if authBody["accountEmail"] == nil {
		t.Error("authorized response should include accountEmail for coordinator")
	}
}

// TestInvalidCredentialsRejected proves wrong password gets 401.
func TestInvalidCredentialsRejected(t *testing.T) {
	srv := newApp(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/groups",
		strings.NewReader(`{"name":"Bad Auth","courseName":"C","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("coordinator:wrong-password-0123456789abcdef")))

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid credentials status = %d, want 401", resp.StatusCode)
	}
}

// TestUnauthorizedOriginRejected proves POST from a disallowed origin is 403.
func TestUnauthorizedOriginRejected(t *testing.T) {
	srv := newApp(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/groups",
		strings.NewReader(`{"name":"Origin Test","courseName":"C","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", coordinatorAuthHeader())
	req.Header.Set("Origin", "https://evil.example.com")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("disallowed origin status = %d, want 403", resp.StatusCode)
	}
}
