package transport_test

// Shared HTTP+DB test plumbing. Endpoint tests run against the real
// PostgreSQL test database; external platforms are replaced by labeled fakes.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"launchpad/internal/config"
	"launchpad/internal/core/groups"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func truncate(t *testing.T, pool *pgxpool.Pool, tables ...string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), "TRUNCATE "+strings.Join(tables, ", ")+" CASCADE"); err != nil {
		t.Fatalf("truncate %v: %v", tables, err)
	}
}

func testConfig() config.Config {
	return config.Config{
		DatabaseURL:        testURL,
		Port:               "0",
		LogLevel:           "error",
		CORSAllowedOrigins: []string{"http://localhost:5173"},
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newGroupsService(pool *pgxpool.Pool) *groups.Service {
	return groups.New(store.New(pool))
}

// newApp builds the full wiring (pool → store → core → transport) with the
// fake Google gateway and truncates every table tests touch.
func newApp(t *testing.T) *httptest.Server {
	t.Helper()
	pool := newTestPool(t)
	truncate(t, pool, "groups", "oauth_states", "integrations", "integration_credentials")
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool), pool.Ping)
	return httptestNew(app, t)
}

func httptestNew(handler http.Handler, t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// rawGet issues a GET that does NOT follow redirects (for 302 assertions).
func rawGet(t *testing.T, srv *httptest.Server, path string) *http.Response {
	t.Helper()
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(srv.URL + path) //nolint:bodyclose — closed by the caller
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// doReq performs a request on an explicit client and decodes the JSON body.
func doReq(t *testing.T, client *http.Client, method, url string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := client.Do(req) //nolint:bodyclose — closed below
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}
