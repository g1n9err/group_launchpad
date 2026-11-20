package transport_test

// End-to-end webhook tests: real HMAC signatures computed in-test, real
// PostgreSQL dedup + resolution (seeded users/channels/memberships), fixed
// clock. Slack itself is never contacted.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"launchpad/internal/core/integrations"

	"launchpad/internal/dbmigrate"
	slackgw "launchpad/internal/integrations/slack"
	"launchpad/internal/store"
	"launchpad/internal/testdb"
	"launchpad/internal/transport"
)

// slackEventsVerifierForTest exposes the real HMAC verifier (fixture secret)
// for HTTP tests that build custom apps.
func slackEventsVerifierForTest() *slackgw.RequestVerifier {
	return slackgw.NewRequestVerifier(testSigningSecret)
}

// decodeJSON decodes an HTTP response body into out.
func decodeJSON(resp *http.Response, out any) error {
	return json.NewDecoder(resp.Body).Decode(out)
}

// migrateSteps applies N migrations (negative = down) on the test database.
func migrateSteps(dbURL string, steps int) error {
	return dbmigrate.Down(dbURL, -steps)
}

// signBody computes a valid v0 signature with the fixture secret.
func signBody(ts, body string) string {
	mac := hmac.New(sha256.New, []byte(testSigningSecret))
	mac.Write([]byte("v0:" + ts + ":" + body))
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

func TestSlackEventsHTTPFlow(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "group_resources", "external_operation_logs", "integrations", "slack_events", "users", "group_members")
	repo := store.New(pool)
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool),
		integrations.NewSlackEventsService(slackEventsVerifierForTest(), repo, nil, testClock, true), nil, pool.Ping)
	srv := httptestNew(app, t)

	// Seed: group ← channel resource, user with slack id, member pending.
	_, created := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Python Backend 13","courseName":"Python","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"]}`)
	groupID := created["id"].(string)
	seedStmts := []string{
		`INSERT INTO group_resources (group_id, provider, resource_type, external_resource_id, resource_name, status)
		 VALUES ('` + groupID + `', 'slack', 'channel', 'CHTTP1', 'python-backend-13-general', 'active')`,
		`INSERT INTO users (full_name, email, role, slack_user_id)
		 VALUES ('Student One', 'student1@x.example', 'student', 'USTUHTTP')`,
		`INSERT INTO group_members (group_id, user_id, member_role, onboarding_status)
		 SELECT '` + groupID + `', id, 'student', 'pending' FROM users WHERE slack_user_id = 'USTUHTTP'`,
	}
	for _, stmt := range seedStmts {
		if _, err := pool.Exec(ctxbg(), stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	ts := fmt.Sprint(testClock().Unix())

	post := func(body, timestamp, signature string) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/integrations/slack/events", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if timestamp != "" {
			req.Header.Set("X-Slack-Request-Timestamp", timestamp)
			req.Header.Set("X-Slack-Signature", signature)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = decodeJSON(resp, &out)
		return resp.StatusCode, out
	}

	memberEvent := `{"type":"event_callback","event_id":"EvHTTP1","team_id":"T1","event":{"type":"member_joined_channel","user":"USTUHTTP","channel":"CHTTP1"}}`

	t.Run("missing headers are 401", func(t *testing.T) {
		code, body := post(memberEvent, "", "")
		if code != http.StatusUnauthorized {
			t.Fatalf("code=%d body=%v", code, body)
		}
	})

	t.Run("invalid signature is 401 and records nothing", func(t *testing.T) {
		code, body := post(memberEvent, ts, "v0=deadbeef")
		if code != http.StatusUnauthorized {
			t.Fatalf("code=%d body=%v", code, body)
		}
		var n int
		_ = pool.QueryRow(ctxbg(), `SELECT count(*) FROM slack_events`).Scan(&n)
		if n != 0 {
			t.Errorf("recorded %d events despite 401", n)
		}
	})

	t.Run("stale timestamp is 401", func(t *testing.T) {
		stale := fmt.Sprint(testClock().Add(-10 * time.Minute).Unix())
		code, _ := post(memberEvent, stale, signBody(stale, memberEvent))
		if code != http.StatusUnauthorized {
			t.Fatalf("stale code=%d", code)
		}
	})

	t.Run("url_verification echoes challenge", func(t *testing.T) {
		body := `{"type":"url_verification","challenge":"echo-me-123"}`
		code, resp := post(body, ts, signBody(ts, body))
		if code != http.StatusOK || resp["challenge"] != "echo-me-123" {
			t.Fatalf("code=%d resp=%v", code, resp)
		}
		var n int
		_ = pool.QueryRow(ctxbg(), `SELECT count(*) FROM slack_events`).Scan(&n)
		if n != 0 {
			t.Error("challenge persisted as event")
		}
	})

	t.Run("first member event processes and updates membership", func(t *testing.T) {
		code, resp := post(memberEvent, ts, signBody(ts, memberEvent))
		if code != http.StatusOK || resp["status"] != "processed" {
			t.Fatalf("code=%d resp=%v", code, resp)
		}
		var status string
		if err := pool.QueryRow(ctxbg(), `
			SELECT gm.onboarding_status FROM group_members gm
			JOIN users u ON u.id = gm.user_id WHERE u.slack_user_id='USTUHTTP'`).Scan(&status); err != nil {
			t.Fatalf("member query: %v", err)
		}
		if status != "joined_slack" {
			t.Errorf("onboarding_status = %q", status)
		}
		var evStatus string
		_ = pool.QueryRow(ctxbg(), `SELECT process_status FROM slack_events WHERE event_id='EvHTTP1'`).Scan(&evStatus)
		if evStatus != "processed" {
			t.Errorf("event status = %q", evStatus)
		}
	})

	t.Run("duplicate delivery acks 200 duplicate and does not reprocess", func(t *testing.T) {
		code, resp := post(memberEvent, ts, signBody(ts, memberEvent))
		if code != http.StatusOK || resp["status"] != "duplicate" {
			t.Fatalf("code=%d resp=%v", code, resp)
		}
		var rows int
		_ = pool.QueryRow(ctxbg(), `SELECT count(*) FROM slack_events WHERE event_id='EvHTTP1'`).Scan(&rows)
		if rows != 1 {
			t.Errorf("event rows = %d, want 1", rows)
		}
	})

	t.Run("unresolvable channel acks unresolved with reason", func(t *testing.T) {
		body := `{"type":"event_callback","event_id":"EvHTTP2","team_id":"T1","event":{"type":"member_joined_channel","user":"USTUHTTP","channel":"CNOPE"}}`
		code, resp := post(body, ts, signBody(ts, body))
		if code != http.StatusOK || resp["status"] != "unresolved" {
			t.Fatalf("code=%d resp=%v", code, resp)
		}
		var reason string
		_ = pool.QueryRow(ctxbg(), `SELECT unresolved_reason FROM slack_events WHERE event_id='EvHTTP2'`).Scan(&reason)
		if reason != "channel_not_linked" {
			t.Errorf("reason = %q", reason)
		}
	})

	t.Run("response surfaces no secret material", func(t *testing.T) {
		code, resp := post(memberEvent, ts, "v0=bad")
		if code != http.StatusUnauthorized {
			t.Fatalf("code=%d", code)
		}
		b := fmt.Sprint(resp)
		for _, forbidden := range []string{testSigningSecret, "xoxb", "Bearer"} {
			if strings.Contains(b, forbidden) {
				t.Errorf("response leaks %q", forbidden)
			}
		}
	})
}

func TestSlackEventsUnconfigured503(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "slack_events")
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool),
		integrations.NewSlackEventsService(nil, store.New(pool), nil, testClock, false), nil, pool.Ping)
	srv := httptestNew(app, t)

	code, body := doJSON(t, srv, http.MethodPost, "/api/integrations/slack/events", `{"type":"url_verification","challenge":"x"}`)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d body=%v", code, body)
	}
	if c := errCode(t, body); c != "NOT_READY" {
		t.Errorf("code=%q", c)
	}
}

// TestSlackEventsMigrationRollback rolls back to the version before Slack Events
// in a separate database so a failed check cannot affect the other HTTP tests.
func TestSlackEventsMigrationRollback(t *testing.T) {
	dsn := testdb.New(t)
	if err := dbmigrate.Up(dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	version, dirty, err := dbmigrate.Version(dsn)
	if err != nil || dirty || version < 4 {
		t.Fatalf("expected clean schema at version 4 or later: version=%d dirty=%t err=%v", version, dirty, err)
	}
	pool, err := pgxpool.New(ctxbg(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := dbmigrate.Down(dsn, int(version)-3); err != nil {
		t.Fatalf("migrate down to 0003: %v", err)
	}

	checks := []struct {
		name  string
		query string
	}{
		{"slack_events", "SELECT to_regclass('slack_events') IS NOT NULL"},
		{"users.slack_user_id", "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='users' AND column_name='slack_user_id')"},
		{"uq_users_email_normalized", "SELECT to_regclass('uq_users_email_normalized') IS NOT NULL"},
	}
	checkSchema := func(want bool) {
		t.Helper()
		for _, check := range checks {
			var exists bool
			if err := pool.QueryRow(ctxbg(), check.query).Scan(&exists); err != nil || exists != want {
				t.Errorf("%s exists=%t, want %t: %v", check.name, exists, want, err)
			}
		}
	}
	checkSchema(false)
	if err := dbmigrate.Up(dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	checkSchema(true)
}
