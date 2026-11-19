package slack_test

// Adapter tests against a LOCAL FIXTURE server standing in for Slack — no
// real workspace is contacted (clearly labeled fake per project rules). The
// fixture asserts the bot token is transmitted but never echoes it;
// classification is verified across success / invalid token / revoked token /
// scope error / HTTP 500 / network failure / identity-less response.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	slackgo "github.com/slack-go/slack"

	coreintegrations "launchpad/internal/core/integrations"
	"launchpad/internal/integrations/slack"
)

const fakeToken = "xoxb-fixture-token-not-real"

// fixtureGateway builds a gateway bound to the fixture API URL.
func fixtureGateway(apiURL string) coreintegrations.SlackGateway {
	client := slackgo.New(fakeToken, slackgo.OptionAPIURL(apiURL))
	return slack.NewGatewayWithClient(client)
}

func TestAuthTestSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth.test" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_ = r.ParseForm()
		if got := r.PostForm.Get("token"); got != fakeToken {
			t.Errorf("token not transmitted as expected (len mismatch)")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"team_id":"T123TEAM","team":"Launchpad WS","user":"launchpad-bot","user_id":"U123"}`))
	}))
	defer srv.Close()

	gw := fixtureGateway(srv.URL + "/")
	ws, err := gw.AuthTest(context.Background())
	if err != nil {
		t.Fatalf("AuthTest: %v", err)
	}
	if ws.TeamID != "T123TEAM" || ws.TeamName != "Launchpad WS" {
		t.Errorf("workspace = %+v", ws)
	}
}

func TestAuthTestInvalidTokenClassifiedAsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	}))
	defer srv.Close()

	gw := fixtureGateway(srv.URL + "/")
	_, err := gw.AuthTest(context.Background())
	if err != coreintegrations.ErrSlackAuthRejected { // errors.Is via equality: sentinel returned unwrapped
		t.Fatalf("want ErrSlackAuthRejected, got %v", err)
	}
	if strings.Contains(err.Error(), fakeToken) {
		t.Error("error leaks the token")
	}
}

func TestAuthTestRevokedTokenIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"token_revoked"}`))
	}))
	defer srv.Close()

	gw := fixtureGateway(srv.URL + "/")
	_, err := gw.AuthTest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("want auth-rejected, got %v", err)
	}
}

func TestAuthTestOtherAPIErrorIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"missing_scope","needed":"channels:manage","provided":"chat:write"}`))
	}))
	defer srv.Close()

	gw := fixtureGateway(srv.URL + "/")
	_, err := gw.AuthTest(context.Background())
	if err == nil || !strings.Contains(err.Error(), coreintegrations.ErrSlackUnavailable.Error()) {
		t.Fatalf("want ErrSlackUnavailable, got %v", err)
	}
}

func TestAuthTestHTTP500IsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	gw := fixtureGateway(srv.URL + "/")
	_, err := gw.AuthTest(context.Background())
	if err == nil || !strings.Contains(err.Error(), coreintegrations.ErrSlackUnavailable.Error()) {
		t.Fatalf("want ErrSlackUnavailable, got %v", err)
	}
}

func TestAuthTestNetworkFailureIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL + "/"
	srv.Close() // refuse all connections

	gw := fixtureGateway(url)
	_, err := gw.AuthTest(context.Background())
	if err == nil || !strings.Contains(err.Error(), coreintegrations.ErrSlackUnavailable.Error()) {
		t.Fatalf("want ErrSlackUnavailable, got %v", err)
	}
	if strings.Contains(err.Error(), fakeToken) {
		t.Error("error leaks the token")
	}
}

func TestSuccessResponseMissingIdentityIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`)) // no team_id/team
	}))
	defer srv.Close()

	gw := fixtureGateway(srv.URL + "/")
	_, err := gw.AuthTest(context.Background())
	if err == nil || !strings.Contains(err.Error(), coreintegrations.ErrSlackUnavailable.Error()) {
		t.Fatalf("want ErrSlackUnavailable for identity-less response, got %v", err)
	}
}
