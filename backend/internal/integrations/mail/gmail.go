package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"launchpad/internal/core/integrations"
)

type GmailCredentials interface {
	GmailCredentials(context.Context) (token, from string, err error)
}

type Gmail struct {
	credentials GmailCredentials
	endpoint    string
	client      *http.Client
}

func NewGmail(credentials GmailCredentials) *Gmail {
	return &Gmail{
		credentials: credentials,
		endpoint:    "https://gmail.googleapis.com/gmail/v1/users/me/messages/send",
		client:      &http.Client{Timeout: 15 * time.Second},
	}
}

func (g *Gmail) Send(ctx context.Context, email integrations.IntakeEmail) (bool, error) {
	if !validAddress(email.Recipient) {
		return false, errors.New("student email address is invalid")
	}
	token, from, err := g.credentials.GmailCredentials(ctx)
	if err != nil {
		return false, errors.New("connected Google account is not ready to send email")
	}
	if !validAddress(from) {
		return false, errors.New("connected Google account has no valid sender address")
	}
	payload, err := json.Marshal(map[string]string{"raw": base64.RawURLEncoding.EncodeToString(intakeMessage(from, email))})
	if err != nil {
		return false, errors.New("could not encode Gmail message")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(payload))
	if err != nil {
		return false, errors.New("could not build Gmail send request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := g.client.Do(req)
	if err != nil {
		return true, errors.New("Gmail acknowledgement was lost; delivery may have succeeded")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		unknown := resp.StatusCode < 400 || resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout
		return unknown, gmailResponseError(resp)
	}
	var confirmation struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&confirmation); err != nil || confirmation.ID == "" {
		return true, errors.New("Gmail returned no message ID; delivery may have succeeded")
	}
	return false, nil
}

// Decode only known provider error codes. Google response text can contain
// account details and must not be copied to delivery records.
func gmailResponseError(resp *http.Response) error {
	var failure struct {
		Error struct {
			Errors  []struct{ Reason string } `json:"errors"`
			Details []struct{ Reason string } `json:"details"`
		} `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&failure) == nil {
		for _, detail := range failure.Error.Details {
			switch detail.Reason {
			case "SERVICE_DISABLED":
				return errors.New("Gmail API is disabled in the Google Cloud project")
			case "ACCESS_TOKEN_SCOPE_INSUFFICIENT":
				return errors.New("Gmail rejected the send scope; reconnect Google and allow sending")
			}
		}
		for _, item := range failure.Error.Errors {
			switch item.Reason {
			case "accessNotConfigured":
				return errors.New("Gmail API is disabled in the Google Cloud project")
			case "domainPolicy":
				return errors.New("Google Workspace policy blocks Gmail API access")
			case "insufficientPermissions":
				return errors.New("Gmail rejected the send scope; reconnect Google and allow sending")
			}
		}
	}
	return fmt.Errorf("Gmail send returned HTTP %d", resp.StatusCode)
}
