package slack_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	slackgo "github.com/slack-go/slack"

	"launchpad/internal/integrations/slack"
)

func TestPostAnnouncementDistinguishesRejectionFromUnknownOutcome(t *testing.T) {
	for _, scenario := range []struct {
		name, reply string
		wantUnknown bool
		wantID      string
	}{
		{"confirmed", `{"ok":true,"channel":"C123","ts":"1795000000.123456"}`, false, "1795000000.123456"},
		{"rejected", `{"ok":false,"error":"not_in_channel"}`, false, ""},
		{"ambiguous", `{"ok":false,"error":"internal_error"}`, true, ""},
		{"missing timestamp", `{"ok":true,"channel":"C123"}`, true, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/chat.postMessage" {
					t.Errorf("unexpected Slack request %s %s", r.Method, r.URL.Path)
				}
				_ = r.ParseForm()
				if r.Form.Get("channel") != "C123" || r.Form.Get("text") != "Smoke reminder" {
					t.Error("Slack request did not contain the expected channel and message")
				}
				_, _ = w.Write([]byte(scenario.reply))
			}))
			defer server.Close()
			gateway := slack.NewGatewayWithClient(slackgo.New(fakeToken, slackgo.OptionAPIURL(server.URL+"/")))
			id, unknown, err := gateway.PostAnnouncement(t.Context(), "C123", "Smoke reminder")
			if id != scenario.wantID || unknown != scenario.wantUnknown || (err == nil) != (scenario.wantID != "") {
				t.Fatalf("post announcement: id=%q unknown=%t err=%v", id, unknown, err)
			}
		})
	}
}
