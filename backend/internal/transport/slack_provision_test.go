package transport_test

// End-to-end (fixture-backed) tests for POST /api/groups/{id}/slack/provision:
// real HTTP + real PostgreSQL idempotency registry, Slack replaced by the
// stateful fake workspace. Proves PRD §8 — re-running never duplicates.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"launchpad/internal/core/integrations"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

func TestSlackProvisionCreatesChannelsAndIsIdempotent(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "group_resources", "external_operation_logs", "integrations")
	fakeWS := newFakeSlackState()
	repo := store.New(pool)
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		integrations.NewSlackAuthService(repo, fakeWS, true),
		integrations.NewSlackChannelsService(fakeWS, repo, repo, true), slackEventsServiceForTest(pool), pool.Ping)
	srv := httptestNew(app, t)

	// Create the group via the public API.
	_, created := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Python Backend 13","courseName":"Full-Stack Python","startDate":"2026-12-01"}`)
	groupID := created["id"].(string)
	provisionURL := "/api/groups/" + groupID + "/slack/provision"

	// ── Run 1: one known member email, one unknown.
	status, body := doJSON(t, srv, http.MethodPost, provisionURL,
		`{"inviteEmails":["staff@x.example","ghost@x.example"]}`)
	if status != http.StatusOK {
		t.Fatalf("run 1 status = %d (%v)", status, body)
	}

	channels := body["channels"].([]any)
	if len(channels) != 4 {
		t.Fatalf("channels = %d, want 4", len(channels))
	}
	for _, c := range channels {
		cm := c.(map[string]any)
		if cm["status"] != "created" || cm["channelId"] == nil {
			t.Errorf("run 1 channel %+v", cm)
		}
	}
	if w := body["welcomeMessages"].([]any); len(w) != 4 {
		t.Fatalf("welcomes = %d, want 4", len(w))
	}
	invites := body["invites"].([]any)
	if len(invites) != 8 { // 2 emails × 4 channels
		t.Fatalf("invites = %d, want 8", len(invites))
	}
	var notFound int
	for _, iv := range invites {
		ivm := iv.(map[string]any)
		if ivm["status"] == "failed" && ivm["error"] == "user_not_found" {
			notFound++
		}
	}
	if notFound != 4 { // ghost@x.example × 4 channels — recorded, not dropped
		t.Errorf("user_not_found invite records = %d, want 4", notFound)
	}

	// Registry: 4 channel rows + 4 welcome rows.
	var rows int
	if err := pool.QueryRow(ctxbg(),
		`SELECT count(*) FROM group_resources WHERE provider='slack'`).Scan(&rows); err != nil {
		t.Fatalf("registry query: %v", err)
	}
	if rows != 8 {
		t.Errorf("registry rows = %d, want 8", rows)
	}
	// Every external call logged with group attribution.
	var ops int
	if err := pool.QueryRow(ctxbg(),
		`SELECT count(*) FROM external_operation_logs WHERE provider='slack' AND group_id IS NOT NULL`).Scan(&ops); err != nil {
		t.Fatalf("op-log query: %v", err)
	}
	if ops == 0 {
		t.Error("expected group-attributed operation log entries")
	}

	// ── Run 2: same request — everything reused/skipped/already_member.
	status2, body2 := doJSON(t, srv, http.MethodPost, provisionURL,
		`{"inviteEmails":["staff@x.example","ghost@x.example"]}`)
	if status2 != http.StatusOK {
		t.Fatalf("run 2 status = %d (%v)", status2, body2)
	}
	for _, c := range body2["channels"].([]any) {
		if c.(map[string]any)["status"] != "reused" {
			t.Errorf("run 2 channel not reused: %v", c)
		}
	}
	for _, w := range body2["welcomeMessages"].([]any) {
		if w.(map[string]any)["status"] != "skipped" {
			t.Errorf("run 2 welcome not skipped: %v", w)
		}
	}
	for _, iv := range body2["invites"].([]any) {
		m := iv.(map[string]any)
		if m["email"] == "staff@x.example" && m["status"] != "already_member" {
			t.Errorf("run 2 invite not already_member: %v", m)
		}
	}

	// No duplicate channels exist in the fake workspace.
	if len(fakeWS.channels) != 4 {
		t.Errorf("workspace channels = %d, want exactly 4 after two runs", len(fakeWS.channels))
	}
	// Exactly one welcome message per channel, ever.
	for chID, n := range fakeWS.posts {
		if n != 1 {
			t.Errorf("channel %s received %d welcome messages, want exactly 1", chID, n)
		}
	}
	// Registry unchanged by run 2.
	var rows2 int
	_ = pool.QueryRow(ctxbg(), `SELECT count(*) FROM group_resources WHERE provider='slack'`).Scan(&rows2)
	if rows2 != rows {
		t.Errorf("registry rows after run 2 = %d, want %d", rows2, rows)
	}
}

func TestSlackProvisionEmptyBodyIsAllowed(t *testing.T) {
	srv := newApp(t)
	_, created := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Data Science 7","courseName":"ML","startDate":"2027-01-11"}`)
	groupID := created["id"].(string)

	status, body := doJSON(t, srv, http.MethodPost, "/api/groups/"+groupID+"/slack/provision", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d (%v)", status, body)
	}
	if channels := body["channels"].([]any); len(channels) != 4 {
		t.Errorf("channels = %d, want 4 (no invites requested)", len(channels))
	}
	if invites, ok := body["invites"].([]any); ok && len(invites) > 0 {
		t.Errorf("unexpected invites: %v", invites)
	}
}

func TestSlackProvisionUnknownGroup(t *testing.T) {
	srv := newApp(t)
	status, body := doJSON(t, srv, http.MethodPost,
		"/api/groups/00000000-0000-0000-0000-000000000000/slack/provision", "")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d (%v)", status, body)
	}
}

func TestSlackProvisionUnconfigured(t *testing.T) {
	srv := newAppNoSlack(t)
	_, created := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Orphan 1","courseName":"X","startDate":"2026-12-01"}`)
	id := created["id"].(string)
	status, body := doJSON(t, srv, http.MethodPost, "/api/groups/"+id+"/slack/provision", "")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d (%v)", status, body)
	}
	if code := errCode(t, body); code != "NOT_READY" {
		t.Errorf("code = %q", code)
	}
}

// Compile-time: keep httptest referenced for the alias used in helpers.
var _ = httptest.NewServer

// silence unused fmt when assertions shrink.
var _ = fmt.Sprintf
