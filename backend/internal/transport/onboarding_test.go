package transport_test

// Stage 3C integration tests: GET /api/groups/{id}/onboarding against real
// PostgreSQL — every summary scenario, rounding, statuses, 404, empty roster.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// mustUUID parses or fails.
func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return id
}

// joinFlips converts emails to a Postgres array literal arg.
func joinFlips(t *testing.T, emails []string) []string {
	t.Helper()
	return emails
}

// createGroupWithRoster creates a group via the API and returns its id.
func createGroupWithRoster(t *testing.T, srv *httptest.Server, name string, emails []string) string {
	t.Helper()
	payload := fmt.Sprintf(`{"name":%q,"courseName":"C","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"]`, name)
	if emails != nil {
		list := ""
		for i, e := range emails {
			if i > 0 {
				list += ","
			}
			list += fmt.Sprintf("%q", e)
		}
		payload += `,"studentEmails":[` + list + `]`
	}
	payload += "}"
	status, resp := doJSON(t, srv, http.MethodPost, "/api/groups", payload)
	if status != http.StatusCreated {
		t.Fatalf("create %s: %d (%v)", name, status, resp)
	}
	return resp["id"].(string)
}

func TestOnboardingAllPending(t *testing.T) {
	srv := newApp(t)
	id := createGroupWithRoster(t, srv, "Pending Group", []string{"p1@x.example", "p2@x.example", "p3@x.example"})

	status, body := doJSON(t, srv, http.MethodGet, "/api/groups/"+id+"/onboarding", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d (%v)", status, body)
	}
	sum := body["summary"].(map[string]any)
	if sum["expectedCount"] != float64(3) || sum["joinedCount"] != float64(0) || sum["pendingCount"] != float64(3) {
		t.Errorf("summary = %v", sum)
	}
	if sum["completionPercentage"] != float64(0) {
		t.Errorf("percentage = %v, want 0", sum["completionPercentage"])
	}
	for _, s := range body["students"].([]any) {
		if s.(map[string]any)["onboardingStatus"] != "pending" {
			t.Errorf("student status = %v", s)
		}
	}
}

func TestOnboardingSomeJoinedAndRounding(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users", "group_members")
	app := newAppForPool(t, pool)
	srv := httptestNew(app, t)

	// 24 students, 21 joined — the PRD example (87.5%).
	emails := make([]string, 24)
	for i := range emails {
		emails[i] = fmt.Sprintf("s%02d@x.example", i)
	}
	id := createGroupWithRoster(t, srv, "PRD Example", emails)

	// Flip 21 members to joined_slack directly (statuses are 3A-owned; SQL
	// seeding is legitimate for read-model tests).
	if _, err := pool.Exec(ctxbg(), `
		UPDATE group_members SET onboarding_status='joined_slack'
		WHERE group_id=$1 AND user_id IN (
		  SELECT id FROM users WHERE email = ANY($2)
		)`, mustUUID(t, id), joinFlips(t, emails[:21])); err != nil {
		t.Fatalf("flip: %v", err)
	}

	_, body := doJSON(t, srv, http.MethodGet, "/api/groups/"+id+"/onboarding", "")
	sum := body["summary"].(map[string]any)
	if sum["joinedCount"] != float64(21) || sum["pendingCount"] != float64(3) {
		t.Errorf("summary = %v", sum)
	}
	if sum["completionPercentage"] != 87.5 {
		t.Errorf("percentage = %v, want 87.5", sum["completionPercentage"])
	}
}

func TestOnboardingAllJoined(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users", "group_members")
	app := newAppForPool(t, pool)
	srv := httptestNew(app, t)

	id := createGroupWithRoster(t, srv, "All Joined", []string{"j1@x.example", "j2@x.example"})
	if _, err := pool.Exec(ctxbg(), `
		UPDATE group_members SET onboarding_status='complete'
		WHERE group_id=$1`, mustUUID(t, id)); err != nil {
		t.Fatalf("flip: %v", err)
	}

	_, body := doJSON(t, srv, http.MethodGet, "/api/groups/"+id+"/onboarding", "")
	sum := body["summary"].(map[string]any)
	if sum["joinedCount"] != float64(2) || sum["pendingCount"] != float64(0) || sum["completionPercentage"] != float64(100) {
		t.Errorf("summary = %v", sum)
	}
}

func TestOnboardingPostJoinStatusesCountAsJoined(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users", "group_members")
	app := newAppForPool(t, pool)
	srv := httptestNew(app, t)

	id := createGroupWithRoster(t, srv, "Mixed States",
		[]string{"a@x.example", "b@x.example", "c@x.example", "d@x.example", "e@x.example"})
	// One of each status.
	if _, err := pool.Exec(ctxbg(), `
		UPDATE group_members gm SET onboarding_status = x.status
		FROM (VALUES ('a@x.example','pending'),('b@x.example','invited'),
		             ('c@x.example','joined_slack'),('d@x.example','form_submitted'),
		             ('e@x.example','complete')) AS x(email, status)
		JOIN users u ON lower(btrim(u.email)) = x.email
		WHERE gm.user_id = u.id AND gm.group_id = $1`, mustUUID(t, id)); err != nil {
		t.Fatalf("statuses: %v", err)
	}

	_, body := doJSON(t, srv, http.MethodGet, "/api/groups/"+id+"/onboarding", "")
	sum := body["summary"].(map[string]any)
	if sum["joinedCount"] != float64(2) { // joined_slack + complete
		t.Errorf("joinedCount = %v, want 2", sum["joinedCount"])
	}
}

func TestOnboardingEmptyRoster(t *testing.T) {
	srv := newApp(t)
	id := createGroupWithRoster(t, srv, "No Students", nil)

	status, body := doJSON(t, srv, http.MethodGet, "/api/groups/"+id+"/onboarding", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	sum := body["summary"].(map[string]any)
	if sum["expectedCount"] != float64(0) || sum["completionPercentage"] != float64(0) {
		t.Errorf("empty summary = %v", sum)
	}
	if students, ok := body["students"].([]any); !ok || len(students) != 0 {
		t.Errorf("students = %v", body["students"])
	}
}

func TestOnboardingUnknownGroup(t *testing.T) {
	srv := newApp(t)
	status, body := doJSON(t, srv, http.MethodGet,
		"/api/groups/00000000-0000-0000-0000-000000000000/onboarding", "")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d (%v)", status, body)
	}
	if code := errCode(t, body); code != "NOT_FOUND" {
		t.Errorf("code = %q", code)
	}
}
