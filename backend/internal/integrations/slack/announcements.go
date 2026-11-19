package slack

import (
	"context"
	"errors"
	"fmt"

	slackgo "github.com/slack-go/slack"
)

// PostAnnouncement reports ambiguous Slack errors separately so a worker
// never retries a message that may already have been published.
func (g *Gateway) PostAnnouncement(ctx context.Context, channelID, text string) (string, bool, error) {
	ts, err := withRateLimit(ctx, func() (string, error) {
		_, ts, err := g.client.PostMessageContext(ctx, channelID, slackgo.MsgOptionText(text, false))
		return ts, err
	})
	if err == nil && ts != "" {
		return ts, false, nil
	}
	var rateLimit *slackgo.RateLimitedError
	if errors.As(err, &rateLimit) {
		return "", false, errors.New("Slack rate limit prevented publication")
	}
	if code, ok := apiErrorCode(err); ok {
		switch code {
		case "channel_not_found", "is_archived", "not_in_channel", "invalid_auth",
			"missing_scope", "restricted_action", "msg_too_long", "no_text",
			"rate_limited", "ratelimited", "account_inactive", "token_revoked":
			return "", false, fmt.Errorf("Slack rejected the announcement (%s)", code)
		}
	}
	return "", true, errors.New("Slack did not confirm publication; check the channel before retrying")
}
