package transport_test

// Stage 3B integration tests: student ingestion through the public API
// against real PostgreSQL — creation, reuse, case-variant reuse, duplicate
// membership prevention, atomic rollback, and concurrent creation.

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"launchpad/internal/core/groups"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

// groupsCreateParams builds the validated command the store expects.
func groupsCreateParams(name string, students []string) groups.CreateParams {
	return groups.CreateParams{Name: name, CourseName: "C", StudentEmails: students}
}

// countRowsSimple counts rows with a scalar query on the test pool.
func countRowsSimple(t *testing.T, pool *pgxpool.Pool, query string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctxbg(), query).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// newAppForPool builds the app over an externally owned pool (so tests can
// assert against the same connection).
func newAppForPool(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	return transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), nil, pool.Ping)
}
func TestStudentIngestionEndToEnd(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users", "group_members")
	app := newAppForPool(t, pool)
	srv := httptestNew(app, t)

	// ── Create with students (case/whitespace variants on purpose).
	body := `{"name":"Python Backend 13","courseName":"Full-Stack","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"],
	          "studentEmails":["  Alice@School.EDU  ","bob@school.edu","CarOL@school.edu"]}`
	status, resp := doJSON(t, srv, http.MethodPost, "/api/groups", body)
	if status != http.StatusCreated {
		t.Fatalf("status = %d (%v)", status, resp)
	}

	// 3 users created with normalized emails + student role.
	var users int
	if err := pool.QueryRow(ctxbg(),
		`SELECT count(*) FROM users WHERE role='student' AND email = lower(btrim(email))`).Scan(&users); err != nil {
		t.Fatalf("users: %v", err)
	}
	if users != 3 {
		t.Errorf("normalized student users = %d, want 3", users)
	}

	// 3 pending memberships.
	var members int
	if err := pool.QueryRow(ctxbg(),
		`SELECT count(*) FROM group_members WHERE member_role='student' AND onboarding_status='pending'`).Scan(&members); err != nil {
		t.Fatalf("members: %v", err)
	}
	if members != 3 {
		t.Errorf("memberships = %d, want 3", members)
	}

	// ── Existing-user reuse: second group shares one student — no new user.
	body2 := `{"name":"Data Science 7","courseName":"ML","startDate":"2027-01-11", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"],
	           "studentEmails":["alice@school.edu","new@school.edu"]}`
	if status, _ := doJSON(t, srv, http.MethodPost, "/api/groups", body2); status != http.StatusCreated {
		t.Fatalf("second create failed: %d", status)
	}
	var total int
	_ = pool.QueryRow(ctxbg(), `SELECT count(*) FROM users WHERE role='student'`).Scan(&total)
	if total != 4 { // 3 + new@school.edu; alice reused
		t.Errorf("total users = %d, want 4 (alice reused)", total)
	}

	// ── Case-variant reuse: pre-seed an UPPERCASE user, enroll lowercase —
	// must reuse (normalized unique index), never create a case twin.
	seed := `INSERT INTO users (full_name, email, role) VALUES ('Legacy', 'OLD.Student@School.EDU', 'student')`
	if _, err := pool.Exec(ctxbg(), seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body3 := `{"name":"Reuse Test 1","courseName":"X","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"],
	           "studentEmails":["old.student@school.edu"]}`
	if status, _ := doJSON(t, srv, http.MethodPost, "/api/groups", body3); status != http.StatusCreated {
		t.Fatalf("case-variant create failed: %d", status)
	}
	var twins int
	_ = pool.QueryRow(ctxbg(),
		`SELECT count(*) FROM users WHERE lower(btrim(email))='old.student@school.edu'`).Scan(&twins)
	if twins != 1 {
		t.Errorf("case-variant rows = %d, want exactly 1 (reuse, no twin)", twins)
	}

	// ── Duplicate-in-request rejection (422, nothing persisted).
	before := countRowsSimple(t, pool, `SELECT count(*) FROM users`)
	dup := `{"name":"Dup Reject","courseName":"X","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"],
	         "studentEmails":["same@school.edu","SAME@school.edu"]}`
	status4, resp4 := doJSON(t, srv, http.MethodPost, "/api/groups", dup)
	if status4 != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate status = %d (%v)", status4, resp4)
	}
	if fe := resp4["error"].(map[string]any)["fieldErrors"].(map[string]any); fe["studentEmails"] == nil {
		t.Errorf("missing studentEmails field error: %v", fe)
	}
	if after := countRowsSimple(t, pool, `SELECT count(*) FROM users`); after != before {
		t.Errorf("rejected request persisted users (%d → %d)", before, after)
	}

	// ── Same student in two groups: membership per group, no duplicates.
	var aliceMemberships int
	_ = pool.QueryRow(ctxbg(), `
		SELECT count(*) FROM group_members gm JOIN users u ON u.id=gm.user_id
		WHERE lower(btrim(u.email))='alice@school.edu'`).Scan(&aliceMemberships)
	if aliceMemberships != 2 {
		t.Errorf("alice memberships = %d, want 2 (one per group)", aliceMemberships)
	}
}

func TestStudentIngestionAtomicRollback(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users", "group_members")
	repo := store.New(pool)

	// A NUL byte passes service validation shape checks but PostgreSQL
	// rejects it inside the transaction — AFTER the group insert succeeded.
	// The whole transaction must roll back: no partial group.
	_, err := repo.Create(ctxbg(), groupsCreateParams("Rollback 1", []string{"bad\x00email@school.edu"}))
	if err == nil {
		t.Fatal("expected failure from NUL byte email")
	}
	if n := countRowsSimple(t, pool, `SELECT count(*) FROM groups WHERE name='Rollback 1'`); n != 0 {
		t.Errorf("group survived a failed student insert: %d rows", n)
	}
	if n := countRowsSimple(t, pool, `SELECT count(*) FROM users`); n != 0 {
		t.Errorf("users survived a failed student insert: %d rows", n)
	}
}

func TestConcurrentGroupCreationSharesStudents(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users", "group_members")
	repo := store.New(pool)

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range 4 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("Concurrent %d", i)
			_, err := repo.Create(ctxbg(), groupsCreateParams(name, []string{"shared@school.edu", fmt.Sprintf("own%d@school.edu", i)}))
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent create %d: %v", i, err)
		}
	}

	// Exactly ONE user row for the shared email despite four racing inserts.
	var shared int
	_ = pool.QueryRow(ctxbg(),
		`SELECT count(*) FROM users WHERE lower(btrim(email))='shared@school.edu'`).Scan(&shared)
	if shared != 1 {
		t.Errorf("shared-email user rows = %d, want exactly 1", shared)
	}
	// The shared user is a member of all four groups.
	var memberships int
	_ = pool.QueryRow(ctxbg(), `
		SELECT count(*) FROM group_members gm JOIN users u ON u.id=gm.user_id
		WHERE lower(btrim(u.email))='shared@school.edu'`).Scan(&memberships)
	if memberships != 4 {
		t.Errorf("shared user memberships = %d, want 4", memberships)
	}
}

func TestGroupCreationWithoutStudentsStillWorks(t *testing.T) {
	srv := newApp(t)
	status, _ := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Classic 1","courseName":"C","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"]}`)
	if status != http.StatusCreated {
		t.Fatalf("legacy request status = %d", status)
	}
}
