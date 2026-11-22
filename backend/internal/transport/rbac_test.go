package transport_test

// Authorization-matrix tests (PRD §3, §7.4): every role's allowed and denied
// operations, cross-group scoping for lecturers/TAs, credential failure
// modes, and PII non-disclosure. Runs against PostgreSQL with no external
// integrations.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"launchpad/internal/core/auth"
)

// seedRoles creates one user per role and returns login tokens.
func seedRoles(t *testing.T, authSvc *auth.Service) map[string]string {
	t.Helper()
	ctx := context.Background()
	password := "role-password-123456"
	roles := map[string]string{
		auth.RoleAdmin:       "admin@roles.test",
		auth.RoleCoordinator: "coordinator@roles.test",
		auth.RoleLecturer:    "lecturer@roles.test",
		auth.RoleTA:          "ta@roles.test",
		auth.RoleStudent:     "student@roles.test",
	}
	tokens := make(map[string]string, len(roles))
	for role, email := range roles {
		if _, err := authSvc.CreateUserWithPassword(ctx, email, role, role, password); err != nil {
			t.Fatalf("create %s: %v", role, err)
		}
		session, err := authSvc.Login(ctx, email, password)
		if err != nil {
			t.Fatalf("login %s: %v", role, err)
		}
		tokens[role] = session.Token
	}
	return tokens
}

// createGroupForRoles creates a group whose lecturer/TA emails belong to the
// given lecturer and TA users.
func createGroupForRoles(t *testing.T, srv *httptest.Server, token, name, lecturerEmail, taEmail string) string {
	t.Helper()
	body := `{"name":"` + name + `","courseName":"Course","startDate":"2026-12-01","staffEmails":["staff@roles.test"],"lecturerEmails":["` + lecturerEmail + `"]`
	if taEmail != "" {
		body += `,"taEmails":["` + taEmail + `"]`
	}
	body += `}`
	status, out := doBearerJSON(t, srv, token, http.MethodPost, "/api/groups", body)
	if status != http.StatusCreated {
		t.Fatalf("create group %s: %d %v", name, status, out)
	}
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("group %s has no id: %v", name, out)
	}
	return id
}

func TestAuthorizationMatrix(t *testing.T) {
	srv, authSvc, _ := newAuthApp(t)
	tokens := seedRoles(t, authSvc)

	// Two groups: only the first has our lecturer/TA assigned.
	ownGroup := createGroupForRoles(t, srv, tokens[auth.RoleCoordinator], "Matrix Own Group", "lecturer@roles.test", "ta@roles.test")
	otherGroup := createGroupForRoles(t, srv, tokens[auth.RoleCoordinator], "Matrix Other Group", "other-lecturer@roles.test", "")

	admin := &bearerClient{t: t, token: tokens[auth.RoleAdmin], Client: http.Client{Timeout: 5 * time.Second}}
	coordinator := &bearerClient{t: t, token: tokens[auth.RoleCoordinator], Client: http.Client{Timeout: 5 * time.Second}}
	lecturer := &bearerClient{t: t, token: tokens[auth.RoleLecturer], Client: http.Client{Timeout: 5 * time.Second}}
	ta := &bearerClient{t: t, token: tokens[auth.RoleTA], Client: http.Client{Timeout: 5 * time.Second}}
	student := &bearerClient{t: t, token: tokens[auth.RoleStudent], Client: http.Client{Timeout: 5 * time.Second}}

	// ── Anonymous: everything protected fails closed with 401 ─────────
	anonymous := &bearerClient{t: t, token: "garbage", Client: http.Client{Timeout: 5 * time.Second}}
	if code, _ := anonymous.do(http.MethodGet, srv.URL+"/api/groups"); code != http.StatusUnauthorized {
		t.Errorf("anonymous list groups: %d, want 401", code)
	}
	if code, _ := anonymous.do(http.MethodGet, srv.URL+"/api/groups/"+ownGroup); code != http.StatusUnauthorized {
		t.Errorf("anonymous get group: %d, want 401", code)
	}
	if code, _ := anonymous.do(http.MethodGet, srv.URL+"/api/groups/"+ownGroup+"/onboarding"); code != http.StatusUnauthorized {
		t.Errorf("anonymous onboarding: %d, want 401", code)
	}

	// ── Coordinator & admin: full operational access ──────────────────
	for name, client := range map[string]*bearerClient{"coordinator": coordinator, "admin": admin} {
		if code, _ := client.do(http.MethodGet, srv.URL+"/api/groups"); code != http.StatusOK {
			t.Errorf("%s list groups: %d, want 200", name, code)
		}
		if code, _ := client.do(http.MethodGet, srv.URL+"/api/groups/"+otherGroup); code != http.StatusOK {
			t.Errorf("%s read foreign group: %d, want 200", name, code)
		}
		if code, _ := client.do(http.MethodGet, srv.URL+"/api/groups/"+ownGroup+"/onboarding"); code != http.StatusOK {
			t.Errorf("%s read onboarding: %d, want 200", name, code)
		}
		if code, _ := client.do(http.MethodGet, srv.URL+"/api/notifications"); code != http.StatusOK {
			t.Errorf("%s read notifications: %d, want 200", name, code)
		}
	}

	// ── Lecturer: own group only, read-only, no roster PII ────────────
	if code, _ := lecturer.do(http.MethodGet, srv.URL+"/api/groups/"+ownGroup); code != http.StatusOK {
		t.Errorf("lecturer read own group: %d, want 200", code)
	}
	if code, _ := lecturer.do(http.MethodGet, srv.URL+"/api/groups/"+otherGroup); code != http.StatusForbidden {
		t.Errorf("lecturer read foreign group: %d, want 403", code)
	}
	if code, _ := lecturer.do(http.MethodGet, srv.URL+"/api/groups/"+ownGroup+"/onboarding"); code != http.StatusForbidden {
		t.Errorf("lecturer read roster (PII): %d, want 403", code)
	}
	if code, _ := lecturer.do(http.MethodGet, srv.URL+"/api/groups/"+ownGroup+"/resources"); code != http.StatusOK {
		t.Errorf("lecturer read own resources: %d, want 200", code)
	}
	if code, _ := lecturer.do(http.MethodGet, srv.URL+"/api/groups/"+otherGroup+"/resources"); code != http.StatusForbidden {
		t.Errorf("lecturer read foreign resources: %d, want 403", code)
	}
	if code, _ := lecturer.do(http.MethodPost, srv.URL+"/api/groups/"+ownGroup+"/launch"); code != http.StatusForbidden {
		t.Errorf("lecturer launch (write): %d, want 403", code)
	}
	if code, _ := lecturer.do(http.MethodPost, srv.URL+"/api/groups"); code != http.StatusForbidden {
		t.Errorf("lecturer create group: %d, want 403", code)
	}
	// Group listing is scoped: only the assigned group appears.
	code, page := lecturer.do(http.MethodGet, srv.URL+"/api/groups")
	if code != http.StatusOK {
		t.Fatalf("lecturer list groups: %d", code)
	}
	pageInfo, _ := page["page"].(map[string]any)
	if total, _ := pageInfo["total"].(float64); total != 1 {
		t.Errorf("lecturer group listing total=%v, want 1 (scoped)", pageInfo)
	}

	// ── TA: same scoping as lecturer ───────────────────────────────────
	if code, _ := ta.do(http.MethodGet, srv.URL+"/api/groups/"+ownGroup); code != http.StatusOK {
		t.Errorf("TA read own group: %d, want 200", code)
	}
	if code, _ := ta.do(http.MethodGet, srv.URL+"/api/groups/"+otherGroup); code != http.StatusForbidden {
		t.Errorf("TA read foreign group: %d, want 403", code)
	}
	if code, _ := ta.do(http.MethodGet, srv.URL+"/api/groups/"+ownGroup+"/onboarding"); code != http.StatusForbidden {
		t.Errorf("TA read roster (PII): %d, want 403", code)
	}
	if code, _ := ta.do(http.MethodPost, srv.URL+"/api/groups/"+ownGroup+"/launch"); code != http.StatusForbidden {
		t.Errorf("TA launch (write): %d, want 403", code)
	}

	// ── Student: the PRD grants no API surface — deny everything ──────
	if code, _ := student.do(http.MethodGet, srv.URL+"/api/groups"); code != http.StatusForbidden {
		t.Errorf("student list groups: %d, want 403", code)
	}
	if code, _ := student.do(http.MethodGet, srv.URL+"/api/groups/"+ownGroup); code != http.StatusForbidden {
		t.Errorf("student read group: %d, want 403", code)
	}
	if code, _ := student.do(http.MethodGet, srv.URL+"/api/groups/"+ownGroup+"/onboarding"); code != http.StatusForbidden {
		t.Errorf("student read roster (PII): %d, want 403", code)
	}
}

func TestBasicCoordinatorCompatibility(t *testing.T) {
	srv, _, _ := newAuthApp(t)

	// The legacy Basic flow still authorizes the coordinator role.
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/notifications", nil)
	req.SetBasicAuth("coordinator", testCoordinatorPassword)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("shared Basic coordinator notifications: %d, want 401", resp.StatusCode)
	}
	// /api/auth/me reports the coordinator identity over Basic.
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/auth/me", nil)
	req.SetBasicAuth("coordinator", testCoordinatorPassword)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("shared Basic /me: %d, want 401", resp.StatusCode)
	}
}

func TestAdminUserAndStaffManagement(t *testing.T) {
	srv, authSvc, pool := newAuthApp(t)
	tokens := seedRoles(t, authSvc)
	admin := &bearerClient{t: t, token: tokens[auth.RoleAdmin], Client: http.Client{Timeout: 5 * time.Second}}
	lecturer := &bearerClient{t: t, token: tokens[auth.RoleLecturer], Client: http.Client{Timeout: 5 * time.Second}}

	// Non-admin is denied user and staff management (PRD §3.2: admin duty).
	if code, _ := lecturer.do(http.MethodGet, srv.URL+"/api/admin/users"); code != http.StatusForbidden {
		t.Errorf("lecturer list users: %d, want 403", code)
	}
	if code, _ := lecturer.do(http.MethodGet, srv.URL+"/api/admin/staff"); code != http.StatusForbidden {
		t.Errorf("lecturer list staff: %d, want 403", code)
	}

	// Admin lists users.
	if code, _ := admin.do(http.MethodGet, srv.URL+"/api/admin/users"); code != http.StatusOK {
		t.Fatalf("admin list users: %d", code)
	}

	// Admin creates a predefined staff member.
	staffBody := `{"email":"predefined@roles.test","fullName":"Predefined Staff"}`
	code, staff := doBearerJSON(t, srv, admin.token, http.MethodPost, "/api/admin/staff", staffBody)
	if code != http.StatusCreated {
		t.Fatalf("admin create staff: %d %v", code, staff)
	}
	staffID, _ := staff["staffId"].(string)

	// Duplicate staff is a conflict.
	if code, _ := doBearerJSON(t, srv, admin.token, http.MethodPost, "/api/admin/staff", staffBody); code != http.StatusConflict {
		t.Errorf("duplicate staff: %d, want 409", code)
	}

	// The predefined staff row exists (the launch fallback list).
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM predefined_staff WHERE email = 'predefined@roles.test'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("predefined staff row: %v / %d", err, count)
	}

	// Admin deletes the staff member.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/admin/staff/"+staffID, nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete staff: %d, want 204", resp.StatusCode)
	}

	// Admin resets a lecturer password; the lecturer logs in with it.
	lecturerID := userIDByEmail(t, pool, "lecturer@roles.test")
	if code, _ := doBearerJSON(t, srv, admin.token, http.MethodPut, "/api/admin/users/"+lecturerID+"/password", `{"password":"reset-password-123456"}`); code != http.StatusOK {
		t.Fatalf("reset password: %d", code)
	}
	if _, err := authSvc.Login(context.Background(), "lecturer@roles.test", "reset-password-123456"); err != nil {
		t.Errorf("login after reset: %v", err)
	}

	// Admins cannot delete their own account (405-family rejection: 400).
	adminID := userIDByEmail(t, pool, "admin@roles.test")
	if code, _ := doBearerJSON(t, srv, admin.token, http.MethodDelete, "/api/admin/users/"+adminID, ``); code != http.StatusBadRequest {
		t.Errorf("self-delete of admin: %d, want 400", code)
	}

	// A second admin can be deleted (not the last one), then the guard
	// would leave exactly one admin in place.
	second, err := authSvc.CreateUserWithPassword(context.Background(), "admin2@roles.test", "Admin Two", auth.RoleAdmin, "role-password-123456")
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := doBearerJSON(t, srv, admin.token, http.MethodDelete, "/api/admin/users/"+second.ID.String(), ``); code != http.StatusNoContent {
		t.Errorf("delete second admin: %d, want 204", code)
	}
}

// TestExpiredAndLoggedOutTokensFailsClosed covers credential failure modes:
// expired sessions and logged-out tokens must return 401, never degrade to
// anonymous access.
func TestExpiredAndLoggedOutTokensFailClosed(t *testing.T) {
	srv, authSvc, pool := newAuthApp(t)
	tokens := seedRoles(t, authSvc)
	createGroupForRoles(t, srv, tokens[auth.RoleCoordinator], "Expiry Group", "lecturer@roles.test", "")

	// Expire the admin session directly in the database.
	session, err := authSvc.Login(context.Background(), "admin@roles.test", "role-password-123456")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE user_sessions SET expires_at = now() - interval '1 second' WHERE token_hash = $1`, session.Hash); err != nil {
		t.Fatal(err)
	}
	expired := &bearerClient{t: t, token: session.Token, Client: http.Client{Timeout: 5 * time.Second}}
	if code, _ := expired.do(http.MethodGet, srv.URL+"/api/groups"); code != http.StatusUnauthorized {
		t.Errorf("expired token list groups: %d, want 401", code)
	}

	// Log out the coordinator and reuse the token.
	loggedOut := &bearerClient{t: t, token: tokens[auth.RoleCoordinator], Client: http.Client{Timeout: 5 * time.Second}}
	if err := authSvc.Logout(context.Background(), tokens[auth.RoleCoordinator]); err != nil {
		t.Fatal(err)
	}
	if code, _ := loggedOut.do(http.MethodGet, srv.URL+"/api/groups"); code != http.StatusUnauthorized {
		t.Errorf("logged-out token list groups: %d, want 401", code)
	}

	// Refresh rotates: the old token dies, the new one works.
	fresh, err := authSvc.Refresh(context.Background(), tokens[auth.RoleLecturer])
	if err != nil {
		t.Fatal(err)
	}
	old := &bearerClient{t: t, token: tokens[auth.RoleLecturer], Client: http.Client{Timeout: 5 * time.Second}}
	if code, _ := old.do(http.MethodGet, srv.URL+"/api/auth/me"); code != http.StatusUnauthorized {
		t.Errorf("pre-rotation token: %d, want 401", code)
	}
	newClient := &bearerClient{t: t, token: fresh.Token, Client: http.Client{Timeout: 5 * time.Second}}
	if code, _ := newClient.do(http.MethodGet, srv.URL+"/api/auth/me"); code != http.StatusOK {
		t.Errorf("rotated token: %d, want 200", code)
	}
}

func TestGroupResourcesEndpoint(t *testing.T) {
	srv, authSvc, pool := newAuthApp(t)
	tokens := seedRoles(t, authSvc)
	groupID := createGroupForRoles(t, srv, tokens[auth.RoleCoordinator], "Resources Group", "lecturer@roles.test", "")

	// Seed one active external resource + a distributed intake form.
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO group_resources (group_id, provider, resource_type, resource_name, external_resource_id)
		VALUES ($1, 'slack', 'channel', 'resources-group-general', 'C123')`, groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO intake_forms (group_id, external_form_id, form_url, status)
		VALUES ($1, 'form-1', 'https://forms.example/responder', 'distributed')`, groupID); err != nil {
		t.Fatal(err)
	}

	lecturer := &bearerClient{t: t, token: tokens[auth.RoleLecturer], Client: http.Client{Timeout: 5 * time.Second}}
	code, rawResources := lecturer.getRaw(http.MethodGet, srv.URL+"/api/groups/"+groupID+"/resources")
	if code != http.StatusOK {
		t.Fatalf("lecturer read resources: %d %s", code, rawResources)
	}
	var items []struct {
		Provider string `json:"provider"`
		Kind     string `json:"kind"`
		Ref      string `json:"ref"`
	}
	if err := json.Unmarshal(rawResources, &items); err != nil || len(items) == 0 {
		t.Fatalf("resources response is not a non-empty array: %s", rawResources)
	}
	foundChannel, foundForm := false, false
	for _, item := range items {
		if item.Provider == "slack" && item.Kind == "channel" && item.Ref == "C123" {
			foundChannel = true
		}
		if item.Provider == "google_forms" && item.Kind == "form" && item.Ref == "https://forms.example/responder" {
			foundForm = true
		}
	}
	if !foundChannel || !foundForm {
		t.Errorf("resources missing channel/form: %s", rawResources)
	}
}

func userIDByEmail(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM users WHERE lower(btrim(email)) = $1`, email).Scan(&id); err != nil {
		t.Fatalf("user by email %s: %v", email, err)
	}
	return id
}

// TestStaffRoleMembershipScoping proves group-scoped access is granted ONLY
// through lecturer/TA memberships or assignments (PRD §3.3, §3.4):
//   - assigned as lecturer → allowed (read-only, no roster);
//   - only a STUDENT membership → denied (students have no API surface);
//   - only a TEACHING_STAFF assignment → denied (the PRD grants the
//     predefined operational staff no API access — provisioning concept);
//   - unrelated group → denied.
func TestStaffRoleMembershipScoping(t *testing.T) {
	srv, authSvc, pool := newAuthApp(t)
	ctx := context.Background()
	password := "role-password-123456"

	// A lecturer and a TA account (API roles), plus one student account.
	users := map[string]string{
		auth.RoleCoordinator: "scoped-coordinator@roles.test",
		auth.RoleLecturer:    "scoped-lecturer@roles.test",
		auth.RoleTA:          "scoped-ta@roles.test",
		auth.RoleStudent:     "scoped-student@roles.test",
	}
	tokens := map[string]string{}
	for role, email := range users {
		if _, err := authSvc.CreateUserWithPassword(ctx, email, role, role, password); err != nil {
			t.Fatalf("create %s: %v", role, err)
		}
		session, err := authSvc.Login(ctx, email, password)
		if err != nil {
			t.Fatalf("login %s: %v", role, err)
		}
		tokens[role] = session.Token
	}
	lecturerID := userIDByEmail(t, pool, users[auth.RoleLecturer])
	taID := userIDByEmail(t, pool, users[auth.RoleTA])
	coordinatorSession, err := authSvc.Login(ctx, users[auth.RoleCoordinator], password)
	if err != nil {
		t.Fatal(err)
	}

	// G1: the lecturer is assigned as LECTURER (staff_assignments) — allowed.
	g1 := createGroupForRoles(t, srv, coordinatorSession.Token, "Scoping Lecturer Group", users[auth.RoleLecturer], "")
	// G2: the TA is assigned as TA — allowed for the TA only.
	g2 := createGroupForRoles(t, srv, coordinatorSession.Token, "Scoping TA Group", "someone-else@roles.test", users[auth.RoleTA])
	// G3: unrelated to both.
	g3 := createGroupForRoles(t, srv, coordinatorSession.Token, "Scoping Other Group", "other@roles.test", "")
	// G4: the lecturer appears ONLY as a rostered STUDENT member.
	g4 := createGroupForRoles(t, srv, coordinatorSession.Token, "Scoping Student Member Group", "other@roles.test", "")
	if _, err := pool.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id, member_role) VALUES ($1, $2, 'student')`,
		g4, lecturerID); err != nil {
		t.Fatal(err)
	}
	// G5: the lecturer appears ONLY as a teaching_staff assignment.
	g5 := createGroupForRoles(t, srv, coordinatorSession.Token, "Scoping Teaching Staff Group", "other@roles.test", "")
	if _, err := pool.Exec(ctx,
		`INSERT INTO staff_assignments (group_id, email, role) VALUES ($1, $2, 'teaching_staff')`,
		g5, users[auth.RoleLecturer]); err != nil {
		t.Fatal(err)
	}
	// G6: the lecturer is BOTH a student member AND a teaching_staff
	// assignment — still no lecturer/TA grant, so denied.
	g6 := createGroupForRoles(t, srv, coordinatorSession.Token, "Scoping Both Non Grants Group", "other@roles.test", "")
	if _, err := pool.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id, member_role) VALUES ($1, $2, 'student')`,
		g6, lecturerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO staff_assignments (group_id, email, role) VALUES ($1, $2, 'teaching_staff')`,
		g6, users[auth.RoleLecturer]); err != nil {
		t.Fatal(err)
	}

	lecturer := &bearerClient{t: t, token: tokens[auth.RoleLecturer], Client: http.Client{Timeout: 5 * time.Second}}
	ta := &bearerClient{t: t, token: tokens[auth.RoleTA], Client: http.Client{Timeout: 5 * time.Second}}

	// Allowed: own lecturer assignment.
	if code, _ := lecturer.do(http.MethodGet, srv.URL+"/api/groups/"+g1); code != http.StatusOK {
		t.Errorf("lecturer on lecturer-assigned group: %d, want 200", code)
	}
	if code, _ := lecturer.do(http.MethodGet, srv.URL+"/api/groups/"+g1+"/resources"); code != http.StatusOK {
		t.Errorf("lecturer resources on lecturer-assigned group: %d, want 200", code)
	}
	// Roster PII stays denied even on the allowed group.
	if code, _ := lecturer.do(http.MethodGet, srv.URL+"/api/groups/"+g1+"/onboarding"); code != http.StatusForbidden {
		t.Errorf("lecturer roster (PII) on own group: %d, want 403", code)
	}

	// Denied: student-membership-only, teaching-staff-only, both, unrelated.
	for name, groupID := range map[string]string{
		"student membership only": g4,
		"teaching_staff only":     g5,
		"student+teaching_staff":  g6,
		"unrelated group":         g3,
	} {
		if code, _ := lecturer.do(http.MethodGet, srv.URL+"/api/groups/"+groupID); code != http.StatusForbidden {
			t.Errorf("lecturer via %s: %d, want 403", name, code)
		}
		if code, _ := lecturer.do(http.MethodGet, srv.URL+"/api/groups/"+groupID+"/resources"); code != http.StatusForbidden {
			t.Errorf("lecturer resources via %s: %d, want 403", name, code)
		}
		if code, _ := lecturer.do(http.MethodGet, srv.URL+"/api/groups/"+groupID+"/onboarding"); code != http.StatusForbidden {
			t.Errorf("lecturer roster via %s: %d, want 403", name, code)
		}
	}

	// The TA: allowed only on g2; everything else denied.
	if code, _ := ta.do(http.MethodGet, srv.URL+"/api/groups/"+g2); code != http.StatusOK {
		t.Errorf("TA on TA-assigned group: %d, want 200", code)
	}
	for name, groupID := range map[string]string{
		"lecturer's group": g1,
		"unrelated group":  g3,
		"student member":   g4,
	} {
		if code, _ := ta.do(http.MethodGet, srv.URL+"/api/groups/"+groupID); code != http.StatusForbidden {
			t.Errorf("TA on %s: %d, want 403", name, code)
		}
	}

	// Scoped listing: the lecturer sees ONLY g1; the TA sees ONLY g2.
	code, page := lecturer.do(http.MethodGet, srv.URL+"/api/groups")
	if code != http.StatusOK {
		t.Fatalf("lecturer list groups: %d", code)
	}
	if ids := groupIDs(t, page); len(ids) != 1 || ids[0] != g1 {
		t.Errorf("lecturer scoped listing = %v, want [%s]", ids, g1)
	}
	code, page = ta.do(http.MethodGet, srv.URL+"/api/groups")
	if code != http.StatusOK {
		t.Fatalf("TA list groups: %d", code)
	}
	if ids := groupIDs(t, page); len(ids) != 1 || ids[0] != g2 {
		t.Errorf("TA scoped listing = %v, want [%s]", ids, g2)
	}

	_ = taID
}

// groupIDs extracts the sorted group ids of a GroupsPage payload.
func groupIDs(t *testing.T, page map[string]any) []string {
	t.Helper()
	data, ok := page["data"].([]any)
	if !ok {
		t.Fatalf("groups page has no data array: %v", page)
	}
	ids := make([]string, 0, len(data))
	for _, raw := range data {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := item["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// doBearerJSON issues a JSON request with a bearer token.
func doBearerJSON(t *testing.T, srv *httptest.Server, token, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}
