package transport_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func registerCoordinatorForTest(t *testing.T, srv *httptest.Server, email string) string {
	t.Helper()
	body := `{"fullName":"Workspace Owner","email":"` + email + `","password":"strong-password-123"}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/register", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register %s: status %d", email, resp.StatusCode)
	}
	var session struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	if session.Token == "" {
		t.Fatal("registration returned no session token")
	}
	return session.Token
}

func TestCoordinatorRegistrationCreatesPrivateWorkspace(t *testing.T) {
	srv, _, _ := newAuthApp(t)
	alpha := registerCoordinatorForTest(t, srv, "workspace-alpha@launchpad.test")
	beta := registerCoordinatorForTest(t, srv, "workspace-beta@launchpad.test")

	create := func(token string) string {
		status, body := doBearerJSON(t, srv, token, http.MethodPost, "/api/groups", `{"name":"Private Cohort","courseName":"Course","startDate":"2026-12-01","studentEmails":[],"staffEmails":["staff@workspace.test"],"lecturerEmails":["lecturer@workspace.test"]}`)
		if status != http.StatusCreated {
			t.Fatalf("create group: status %d, body %v", status, body)
		}
		id, _ := body["id"].(string)
		if id == "" {
			t.Fatal("created group has no ID")
		}
		return id
	}
	alphaGroup := create(alpha)
	betaGroup := create(beta) // same group name is allowed in a separate account

	status, result := doBearerJSON(t, srv, alpha, http.MethodGet, "/api/groups", "")
	if status != http.StatusOK {
		t.Fatalf("list Alpha groups: %d %v", status, result)
	}
	page := result["page"].(map[string]any)
	if page["total"] != float64(1) {
		t.Fatalf("Alpha sees %v groups, want 1", page["total"])
	}
	if code, _ := doBearerJSON(t, srv, alpha, http.MethodGet, "/api/groups/"+betaGroup, ""); code != http.StatusForbidden {
		t.Fatalf("Alpha read Beta's group: %d, want 403", code)
	}
	if code, _ := doBearerJSON(t, srv, beta, http.MethodGet, "/api/groups/"+alphaGroup, ""); code != http.StatusForbidden {
		t.Fatalf("Beta read Alpha's group: %d, want 403", code)
	}
	status, result = doBearerJSON(t, srv, beta, http.MethodGet, "/api/groups", "")
	if status != http.StatusOK || result["page"].(map[string]any)["total"] != float64(1) {
		t.Fatalf("Beta group list is not isolated: %d %v", status, result)
	}
}
