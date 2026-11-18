package transport_test

// Integration tests for the HTTP surface: real echo app backed by a real
// PostgreSQL test database (no mocks). Requires the local docker compose
// Postgres; TestMain creates + migrates an isolated `launchpad_test` database
// and truncates groups before each test.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"launchpad/internal/dbmigrate"
)

var testURL string

func TestMain(m *testing.M) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		base = "postgres://launchpad:launchpad@localhost:5432/launchpad?sslmode=disable"
	}
	u, err := url.Parse(base)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal: bad TEST_DATABASE_URL:", err)
		os.Exit(1)
	}
	u.Path = "/launchpad_test" // isolated test database
	testURL = u.String()

	if err := ensureTestDB(base); err != nil {
		fmt.Fprintln(os.Stderr, "fatal: test db:", err)
		os.Exit(1)
	}
	if err := dbmigrate.Up(testURL); err != nil {
		fmt.Fprintln(os.Stderr, "fatal: migrate test db:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// ensureTestDB connects to the base database and creates launchpad_test if
// missing (CREATE DATABASE cannot run inside a transaction).
func ensureTestDB(base string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		return fmt.Errorf("connect: %w (is `docker compose up -d postgres` running?)", err)
	}
	defer conn.Close(context.Background())
	_, err = conn.Exec(ctx, `SELECT 1 FROM pg_database WHERE datname = 'launchpad_test'`)
	if err != nil {
		return err
	}
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'launchpad_test')`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := conn.Exec(ctx, `CREATE DATABASE launchpad_test`); err != nil {
			return err
		}
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────

func doJSON(t *testing.T, srv *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	var req *http.Request
	var err error
	if body != "" {
		req, err = http.NewRequest(method, srv.URL+path, bytes.NewBufferString(body))
	} else {
		req, err = http.NewRequest(method, srv.URL+path, nil)
	}
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func errCode(t *testing.T, body map[string]any) string {
	t.Helper()
	obj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error envelope: %v", body)
	}
	code, _ := obj["code"].(string)
	return code
}

// ── tests ─────────────────────────────────────────────────────────────────

func TestHealth(t *testing.T) {
	srv := newApp(t)
	status, body := doJSON(t, srv, http.MethodGet, "/health", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body["status"] != "ok" {
		t.Errorf("body = %v", body)
	}
}

func TestReadyz(t *testing.T) {
	srv := newApp(t)
	status, body := doJSON(t, srv, http.MethodGet, "/readyz", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", status, body)
	}
}

func TestCreateGroupHappyPath(t *testing.T) {
	srv := newApp(t)
	status, body := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Python Backend 13","courseName":"Go/Python Full Stack","startDate":"2026-12-01"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %v)", status, body)
	}
	if body["name"] != "Python Backend 13" || body["courseName"] != "Go/Python Full Stack" {
		t.Errorf("echo mismatch: %v", body)
	}
	if body["status"] != "draft" {
		t.Errorf("status = %v, want draft", body["status"])
	}
	if body["startDate"] != "2026-12-01" {
		t.Errorf("startDate = %v, want 2026-12-01", body["startDate"])
	}
	if id, ok := body["id"].(string); !ok || len(id) != 36 {
		t.Errorf("id = %v, want UUID", body["id"])
	}
	if _, ok := body["createdAt"]; !ok {
		t.Error("createdAt missing")
	}
}

func TestCreateGroupDuplicateNameRejected(t *testing.T) {
	srv := newApp(t)
	payload := `{"name":"Python Backend 13","courseName":"Go","startDate":"2026-12-01"}`
	if status, _ := doJSON(t, srv, http.MethodPost, "/api/groups", payload); status != http.StatusCreated {
		t.Fatalf("first create failed with %d", status)
	}
	status, body := doJSON(t, srv, http.MethodPost, "/api/groups", payload)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	if got := errCode(t, body); got != "DUPLICATE_GROUP" {
		t.Errorf("code = %q, want DUPLICATE_GROUP", got)
	}
}

func TestCreateGroupValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		field   string
	}{
		{"missing name", `{"courseName":"Go","startDate":"2026-12-01"}`, "name"},
		{"empty course", `{"name":"X","courseName":"","startDate":"2026-12-01"}`, "courseName"},
		{"bad date", `{"name":"X","courseName":"Go","startDate":"2026-02-30"}`, "startDate"},
		{"missing date", `{"name":"X","courseName":"Go"}`, "startDate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newApp(t)
			status, body := doJSON(t, srv, http.MethodPost, "/api/groups", tc.payload)
			if status != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body %v)", status, body)
			}
			if got := errCode(t, body); got != "VALIDATION_ERROR" {
				t.Errorf("code = %q, want VALIDATION_ERROR", got)
			}
			obj := body["error"].(map[string]any)
			fields, ok := obj["fieldErrors"].(map[string]any)
			if !ok {
				t.Fatalf("fieldErrors missing: %v", obj)
			}
			if _, present := fields[tc.field]; !present {
				t.Errorf("expected fieldErrors[%q], got %v", tc.field, fields)
			}
		})
	}
}

func TestCreateGroupMalformedJSON(t *testing.T) {
	srv := newApp(t)
	status, body := doJSON(t, srv, http.MethodPost, "/api/groups", `{not json`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %v)", status, body)
	}
}

func TestGetGroupRoundTrip(t *testing.T) {
	srv := newApp(t)
	_, created := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Python Backend 13","courseName":"Go","startDate":"2026-12-01"}`)
	id := created["id"].(string)

	status, body := doJSON(t, srv, http.MethodGet, "/api/groups/"+id, "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body["id"] != id || body["name"] != "Python Backend 13" {
		t.Errorf("round trip mismatch: %v", body)
	}
}

func TestGetGroupUnknownID(t *testing.T) {
	srv := newApp(t)
	status, body := doJSON(t, srv, http.MethodGet, "/api/groups/00000000-0000-0000-0000-000000000000", "")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	if got := errCode(t, body); got != "NOT_FOUND" {
		t.Errorf("code = %q, want NOT_FOUND", got)
	}
}

func TestListGroupsPagination(t *testing.T) {
	srv := newApp(t)
	for i := 1; i <= 3; i++ {
		payload := fmt.Sprintf(`{"name":"Group %d","courseName":"Go","startDate":"2026-12-01"}`, i)
		if status, _ := doJSON(t, srv, http.MethodPost, "/api/groups", payload); status != http.StatusCreated {
			t.Fatalf("seed %d failed", i)
		}
	}
	status, body := doJSON(t, srv, http.MethodGet, "/api/groups?limit=2&offset=1", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	data, ok := body["data"].([]any)
	if !ok {
		t.Fatalf("data missing: %v", body)
	}
	if len(data) != 2 {
		t.Errorf("len(data) = %d, want 2", len(data))
	}
	page := body["page"].(map[string]any)
	if page["total"] != float64(3) || page["limit"] != float64(2) || page["offset"] != float64(1) {
		t.Errorf("page envelope wrong: %v", page)
	}
}
