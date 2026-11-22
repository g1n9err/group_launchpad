package mail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"launchpad/internal/core/integrations"
)

type testGmailCredentials struct{}

func (testGmailCredentials) GmailCredentials(context.Context) (string, string, error) {
	return "gmail-test-token", "coordinator@example.test", nil
}

func TestGmailSendsFromConnectedAccount(t *testing.T) {
	const message = "Բարև։\nԽնդրում եմ լրացնել ձևաթուղթը։"
	const responderURL = "https://docs.google.com/forms/d/e/test/viewform"
	for _, tc := range []struct {
		name    string
		code    int
		unknown bool
	}{
		{"accepted", 200, false},
		{"permission denied", 403, false},
		{"unknown server failure", 500, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer gmail-test-token" {
					t.Error("expected an authenticated Gmail send request")
				}
				var payload struct{ Raw string }
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				raw, err := base64.RawURLEncoding.DecodeString(payload.Raw)
				if err != nil || !strings.Contains(string(raw), "From: coordinator@example.test\r\nTo: student@example.test") ||
					!strings.Contains(string(raw), "Content-Type: text/plain; charset=UTF-8") {
					t.Errorf("unexpected message %q (%v)", raw, err)
				}
				_, body, _ := strings.Cut(string(raw), "\r\n\r\n")
				decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
				if got := strings.ReplaceAll(string(decoded), "\r\n", "\n"); err != nil || got != message+"\n\n"+responderURL+"\n" {
					t.Errorf("unexpected decoded body %q (%v)", got, err)
				}
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(`{"id":"gmail-test-id"}`))
			}))
			defer srv.Close()
			gmail := NewGmail(testGmailCredentials{})
			gmail.endpoint, gmail.client = srv.URL, srv.Client()
			unknown, err := gmail.Send(t.Context(), integrations.IntakeEmail{
				Recipient: "student@example.test", GroupName: "Test Group", ResponderURL: responderURL, Message: message,
			})
			if unknown != tc.unknown || (err == nil) != (tc.code == 200) {
				t.Fatalf("unknown=%t err=%v", unknown, err)
			}
		})
	}
}

func TestGmailResponseErrorReportsDisabledAPIWithoutProviderDetails(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"private account details","details":[{"reason":"SERVICE_DISABLED"}]}}`)),
	}
	defer resp.Body.Close()
	if got := gmailResponseError(resp).Error(); got != "Gmail API is disabled in the Google Cloud project" {
		t.Fatalf("unexpected public error: %q", got)
	}
}
