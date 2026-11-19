package transport_test

// HTTP-level identity-linking test (Stage 3A): a signed member_joined_channel
// event for an UNLINKED user resolves via the user's Slack email, persists
// users.slack_user_id, and flips group_members.onboarding_status — all
// against real PostgreSQL, with Slack replaced by the stateful fake.

import (
	"fmt"
	"net/http"
	"testing"

	"launchpad/internal/core/integrations"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

func TestSlackEventLinksUnlinkedUserEndToEnd(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "group_resources", "integrations", "slack_events", "users", "group_members")
	fakeWS := newFakeSlackState()
	repo := store.New(pool)
	linker := integrations.NewSlackIdentityLinker(fakeWS, repo)
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		integrations.NewSlackAuthService(repo, fakeWS, true),
		integrations.NewSlackChannelsService(fakeWS, repo, repo, true),
		integrations.NewSlackEventsService(slackEventsVerifierForTest(), repo, linker, testClock, true), pool.Ping)
	srv := httptestNew(app, t)

	// Group + channel resource + member whose email is on file but has NO
	// slack_user_id. The fake Slack knows the joining user's email.
	_, created := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Linkers 1","courseName":"Onboarding","startDate":"2026-12-01"}`)
	groupID := created["id"].(string)
	fakeWS.emails["linker@x.example"] = "UNEWDUDE"
	stmts := []string{
		`INSERT INTO group_resources (group_id, provider, resource_type, external_resource_id, resource_name, status)
		 VALUES ('` + groupID + `', 'slack', 'channel', 'CLINK1', 'linkers-1-general', 'active')`,
		// Case variant on purpose: normalization must bridge it.
		`INSERT INTO users (full_name, email, role) VALUES ('Link Student', 'Linker@X.Example', 'student')`,
		`INSERT INTO group_members (group_id, user_id, member_role, onboarding_status)
		 SELECT '` + groupID + `', id, 'student', 'pending' FROM users WHERE lower(email) = 'linker@x.example'`,
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctxbg(), stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	ts := fmt.Sprint(testClock().Unix())
	body := `{"type":"event_callback","event_id":"EvLINKHTTP","team_id":"T1","event":{"type":"member_joined_channel","user":"UNEWDUDE","channel":"CLINK1"}}`

	code, resp := postTo(t, srv, "/api/integrations/slack/events", ts, body, signBody(ts, body))
	if code != http.StatusOK || resp["status"] != "processed" {
		t.Fatalf("code=%d resp=%v", code, resp)
	}

	var linkedID string
	if err := pool.QueryRow(ctxbg(),
		`SELECT slack_user_id FROM users WHERE lower(email)='linker@x.example'`).Scan(&linkedID); err != nil {
		t.Fatalf("user lookup: %v", err)
	}
	if linkedID != "UNEWDUDE" {
		t.Errorf("slack_user_id = %q, want UNEWDUDE (case-variant email matched via normalization)", linkedID)
	}

	var onboarding string
	if err := pool.QueryRow(ctxbg(), `
		SELECT gm.onboarding_status FROM group_members gm
		JOIN users u ON u.id = gm.user_id WHERE u.slack_user_id = 'UNEWDUDE'`).Scan(&onboarding); err != nil {
		t.Fatalf("member lookup: %v", err)
	}
	if onboarding != "joined_slack" {
		t.Errorf("onboarding_status = %q, want joined_slack", onboarding)
	}

	var evStatus string
	_ = pool.QueryRow(ctxbg(), `SELECT process_status FROM slack_events WHERE event_id='EvLINKHTTP'`).Scan(&evStatus)
	if evStatus != "processed" {
		t.Errorf("event status = %q", evStatus)
	}
}
