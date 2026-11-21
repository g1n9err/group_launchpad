package google_test

// Audit checker tests: classroom states map to PRD §4.7 semantics — ACTIVE
// → ok, PROVISIONED → provisioned (an existing course; consumer accounts
// cannot activate via API), ARCHIVED → archived, everything else →
// inactive. A local fixture server stands in for Google.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"launchpad/internal/core/audit"
	"launchpad/internal/integrations/google"
)

type staticTokenSource struct{}

func (staticTokenSource) AccessToken(context.Context) (string, error) { return "fixture-token", nil }

func auditFixture(t *testing.T, courseState string) audit.Checker {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"course-1","courseState":"` + courseState + `"}`))
	}))
	t.Cleanup(srv.Close)
	return google.NewAuditChecker(staticTokenSource{}).WithHTTPClient(srv.Client(), srv.URL)
}

func TestAuditClassroomStates(t *testing.T) {
	cases := map[string]string{
		"ACTIVE":      "ok",
		"PROVISIONED": "provisioned",
		"ARCHIVED":    "archived",
		"SUSPENDED":   "inactive",
	}
	for state, want := range cases {
		checker := auditFixture(t, state)
		got := checker.CheckResource(context.Background(), audit.Resource{Provider: "google_classroom", Kind: "course", Key: "course", ID: "course-1"})
		if got != want {
			t.Errorf("courseState %s → %s, want %s", state, got, want)
		}
	}
}
