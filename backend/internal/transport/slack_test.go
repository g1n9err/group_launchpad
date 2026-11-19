package transport_test

// HTTP-surface tests for the Slack status endpoint. Slack is replaced by a
// labeled fake gateway; persistence (upsert + operation log) runs against the
// real PostgreSQL test database.

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"launchpad/internal/core/integrations"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

// ctxbg is a tiny alias to keep the SQL-assertion tests readable.
func ctxbg() context.Context { return context.Background() }

func TestSlackStatusUnconfigured(t *testing.T) {
	srv := newAppNoSlack(t)
	status, body := doJSON(t, srv, http.MethodGet, "/api/integrations/slack/status", "")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if code := errCode(t, body); code != "NOT_READY" {
		t.Errorf("code = %q, want NOT_READY", code)
	}
	if _, ok := body["error"].(map[string]any)["message"].(string); !ok {
		t.Errorf("missing message: %v", body)
	}
}

func TestSlackStatusConnectedPersistsRowAndLogsOperation(t *testing.T) {
	srv := newApp(t)

	status, body := doJSON(t, srv, http.MethodGet, "/api/integrations/slack/status", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d (body %v)", status, body)
	}
	if body["status"] != "connected" || body["teamId"] != "TTESTWS" || body["teamName"] != "Fixture Workspace" {
		t.Errorf("body = %v", body)
	}
	if _, ok := body["connectedAt"]; !ok {
		t.Error("connectedAt missing")
	}

	// Whitelist assertion: only the documented, safe fields are present.
	for key := range body {
		switch key {
		case "status", "teamId", "teamName", "connectedAt":
		default:
			t.Errorf("unexpected field %q in response — possible leak surface", key)
		}
	}
}

func TestSlackStatusRepeatedChecksAreIdempotent(t *testing.T) {
	srv := newApp(t)
	for i := 1; i <= 3; i++ {
		status, body := doJSON(t, srv, http.MethodGet, "/api/integrations/slack/status", "")
		if status != http.StatusOK || body["status"] != "connected" {
			t.Fatalf("check %d: status=%d body=%v", i, status, body)
		}
	}
	// One row per check is acceptable (upsert), but the operation log must
	// record each auth.test call.
	pool, err := pgxpool.New(ctxbg(), testURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	var ops int
	if err := pool.QueryRow(ctxbg(),
		`SELECT count(*) FROM external_operation_logs WHERE provider='slack' AND operation='slack.auth.test'`,
	).Scan(&ops); err != nil {
		t.Fatalf("query op log: %v", err)
	}
	if ops != 3 {
		t.Errorf("operation log entries = %d, want 3", ops)
	}
	var rows int
	if err := pool.QueryRow(ctxbg(),
		`SELECT count(*) FROM integrations WHERE provider='slack' AND account_identifier='TTESTWS' AND connection_status='connected'`,
	).Scan(&rows); err != nil {
		t.Fatalf("query integrations: %v", err)
	}
	if rows != 1 {
		t.Errorf("slack integration rows = %d, want exactly 1 (upsert)", rows)
	}
}

func TestSlackStatusInvalidTokenFirstCheckReportsErrorWithoutPersisting(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "integrations", "external_operation_logs")
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		integrations.NewSlackAuthService(store.New(pool), &fakeSlackGateway{err: integrations.ErrSlackAuthRejected}, true),
		integrations.NewSlackChannelsService(nil, nil, store.New(pool), false), slackEventsServiceForTest(pool), pool.Ping)
	srv := httptestNew(app, t)

	status, body := doJSON(t, srv, http.MethodGet, "/api/integrations/slack/status", "")
	if status != http.StatusOK {
		t.Fatalf("auth rejection is a reported state, got HTTP %d (%v)", status, body)
	}
	if body["status"] != "error" {
		t.Errorf("body = %v, want status=error", body)
	}

	// No workspace was ever verified, so there is no row to attribute the
	// error to — nothing is persisted on a first-check failure.
	var count int
	if err := pool.QueryRow(ctxbg(), `SELECT count(*) FROM integrations WHERE provider='slack'`).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("first-check failure must not persist rows, found %d", count)
	}
}

func TestSlackStatusRevokedTokenAfterConnectFlipsRowToError(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "integrations", "external_operation_logs")
	// Phase 1: healthy token — connects and persists the row.
	healthy := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		integrations.NewSlackAuthService(store.New(pool), &fakeSlackGateway{workspace: integrations.SlackWorkspace{TeamID: "TTESTWS", TeamName: "WS"}}, true),
		integrations.NewSlackChannelsService(nil, nil, store.New(pool), false), slackEventsServiceForTest(pool), pool.Ping)
	srv1 := httptestNew(healthy, t)
	if s, body := doJSON(t, srv1, http.MethodGet, "/api/integrations/slack/status", ""); s != http.StatusOK || body["status"] != "connected" {
		t.Fatalf("phase 1: status=%d body=%v", s, body)
	}

	// Phase 2: token revoked — same workspace, row flips to error.
	revoked := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		integrations.NewSlackAuthService(store.New(pool), &fakeSlackGateway{err: integrations.ErrSlackAuthRejected}, true),
		integrations.NewSlackChannelsService(nil, nil, store.New(pool), false), slackEventsServiceForTest(pool), pool.Ping)
	srv2 := httptestNew(revoked, t)
	s, body := doJSON(t, srv2, http.MethodGet, "/api/integrations/slack/status", "")
	if s != http.StatusOK || body["status"] != "error" {
		t.Fatalf("phase 2: status=%d body=%v", s, body)
	}

	var connStatus string
	if err := pool.QueryRow(ctxbg(), `SELECT connection_status FROM integrations WHERE provider='slack' AND account_identifier='TTESTWS'`).Scan(&connStatus); err != nil {
		t.Fatalf("query: %v", err)
	}
	if connStatus != "error" {
		t.Errorf("persisted status = %q, want error (revoked after connect)", connStatus)
	}
}

func TestSlackStatusNetworkFailureDoesNotPersistError(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "integrations", "external_operation_logs")
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		integrations.NewSlackAuthService(store.New(pool), &fakeSlackGateway{err: integrations.ErrSlackUnavailable}, true),
		integrations.NewSlackChannelsService(nil, nil, store.New(pool), false), slackEventsServiceForTest(pool), pool.Ping)
	srv := httptestNew(app, t)

	status, body := doJSON(t, srv, http.MethodGet, "/api/integrations/slack/status", "")
	if status != http.StatusOK || body["status"] != "error" {
		t.Fatalf("status=%d body=%v, want 200/error", status, body)
	}
	var count int
	if err := pool.QueryRow(ctxbg(), `SELECT count(*) FROM integrations WHERE provider='slack'`).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("transient failure must not persist anything, found %d rows", count)
	}
}
