package slack_test

// Membership audit adapter tests (PRD §4.7) against a LOCAL FIXTURE server
// standing in for Slack — no real workspace is contacted. Verifies member
// detection per role, missing-member reporting, and fail-closed degradation.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	coreaudit "launchpad/internal/core/audit"
)

func membersFixture(t *testing.T, memberIDs []string, emailToID map[string]string) coreaudit.MemberChecker {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/conversations.members":
			w.Write([]byte(`{"ok":true,"members":["` + joinIDs(memberIDs) + `"],"response_metadata":{"next_cursor":""}}`))
		case "/users.lookupByEmail":
			_ = r.ParseForm()
			id := emailToID[r.PostForm.Get("email")]
			if id == "" {
				w.Write([]byte(`{"ok":false,"error":"users_not_found"}`))
				return
			}
			w.Write([]byte(`{"ok":true,"user":{"id":"` + id + `"}}`))
		default:
			w.Write([]byte(`{"ok":false,"error":"unknown_endpoint"}`))
		}
	}))
	t.Cleanup(srv.Close)
	gw := fixtureGateway(srv.URL + "/")
	memberChecker, ok := gw.(coreaudit.MemberChecker)
	if !ok {
		t.Fatal("gateway does not implement audit.MemberChecker")
	}
	return memberChecker
}

func joinIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += `","`
		}
		out += id
	}
	return out
}

func TestCheckMembersReportsMissingMembersPerRole(t *testing.T) {
	checker := membersFixture(t,
		[]string{"U100", "U200"}, // channel contains staff + TA only
		map[string]string{
			"staff@test.example":    "U100",
			"lecturer@test.example": "U200",
			"ta@test.example":       "U300", // in workspace, not in channel
		})
	channels := []coreaudit.Resource{{Provider: "slack", Kind: "channel", Key: "g-general", ID: "C1"}}
	results := checker.CheckMembers(context.Background(), channels, []coreaudit.ExpectedMember{
		{Role: "teaching_staff", Email: "staff@test.example"},
		{Role: "lecturer", Email: "lecturer@test.example"},
		{Role: "ta", Email: "ta@test.example"},
		{Role: "ta", Email: "ghost@test.example"}, // not in workspace at all
	})
	byRole := map[string]string{}
	for _, r := range results {
		byRole[r.Key] = r.Status
	}
	if byRole["teaching_staff"] != coreaudit.StatusOK {
		t.Errorf("teaching_staff=%s, want ok", byRole["teaching_staff"])
	}
	if byRole["lecturer"] != coreaudit.StatusOK {
		t.Errorf("lecturer=%s, want ok", byRole["lecturer"])
	}
	if byRole["ta"] != coreaudit.StatusMissingOrInaccessible {
		t.Errorf("ta=%s, want missing_or_inaccessible", byRole["ta"])
	}
}

func TestCheckMembersFailsClosedOnAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":false,"error":"internal_error"}`))
	}))
	t.Cleanup(srv.Close)
	gw := fixtureGateway(srv.URL + "/")
	checker := gw.(coreaudit.MemberChecker)
	results := checker.CheckMembers(context.Background(),
		[]coreaudit.Resource{{Provider: "slack", Kind: "channel", ID: "C1"}},
		[]coreaudit.ExpectedMember{{Role: "teaching_staff", Email: "staff@test.example"}})
	if len(results) != 1 || results[0].Status != coreaudit.StatusUnavailable {
		t.Fatalf("results=%+v, want a single unavailable result", results)
	}
}
