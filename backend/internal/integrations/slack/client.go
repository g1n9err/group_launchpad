// Package slack implements core/integrations.SlackGateway against the real
// Slack Web API using the official slack-go/slack SDK. Credential-gated by
// construction: the composition root only wires it when SLACK_BOT_TOKEN is
// set. Every raw error is classified into a typed domain error BEFORE it
// leaves this package — raw transport text never crosses the port, and the
// bot token never appears in any error, log line, or return value.
package slack

import (
	"context"
	"errors"
	"fmt"
	"net"

	slackgo "github.com/slack-go/slack"

	coreintegrations "launchpad/internal/core/integrations"
)

// authRejectedCodes are Slack auth.test error codes that mean the token is
// permanently unusable. Anything else (missing_scope, rate limits, 5xx) is
// treated as transient from the connection-check perspective.
var authRejectedCodes = map[string]bool{
	"invalid_auth":     true,
	"not_authed":       true,
	"token_revoked":    true,
	"account_inactive": true,
}

// Gateway implements core/integrations.SlackGateway.
type Gateway struct {
	client *slackgo.Client
}

// NewGateway builds the Slack client. The token stays inside the SDK client.
func NewGateway(botToken string) *Gateway {
	return NewGatewayWithClient(slackgo.New(botToken))
}

// NewGatewayWithClient builds a gateway over a preconfigured SDK client —
// the seam tests use to bind to fixture servers (labeled fakes, never real).
func NewGatewayWithClient(client *slackgo.Client) *Gateway {
	return &Gateway{client: client}
}

// AuthTest calls Slack auth.test and returns the verified workspace identity.
// Errors are mapped to ErrSlackAuthRejected (permanent — persist error) or
// ErrSlackUnavailable (transient — do not persist).
func (g *Gateway) AuthTest(ctx context.Context) (coreintegrations.SlackWorkspace, error) {
	resp, err := g.client.AuthTestContext(ctx)
	if err != nil {
		return coreintegrations.SlackWorkspace{}, classify(err)
	}
	if resp.TeamID == "" || resp.Team == "" {
		// Defensive: a successful auth.test always carries workspace identity.
		return coreintegrations.SlackWorkspace{}, fmt.Errorf("%w: response missing workspace identity", coreintegrations.ErrSlackUnavailable)
	}
	return coreintegrations.SlackWorkspace{TeamID: resp.TeamID, TeamName: resp.Team, BotUserID: resp.UserID}, nil
}

// classify maps slack-go/transport errors onto the two domain categories,
// using the SDK's typed errors (never string-matching full messages).
func classify(err error) error {
	// API-level failure: Slack processed the request and answered with an
	// error code in SlackErrorResponse.Err (e.g. "invalid_auth").
	var slackErr slackgo.SlackErrorResponse
	if errors.As(err, &slackErr) {
		if authRejectedCodes[slackErr.Err] {
			return coreintegrations.ErrSlackAuthRejected
		}
		return fmt.Errorf("%w: slack api error (%s)", coreintegrations.ErrSlackUnavailable, slackErr.Err)
	}

	// Rate limiting: retryable by definition.
	var rateLimited *slackgo.RateLimitedError
	if errors.As(err, &rateLimited) {
		return fmt.Errorf("%w: rate limited", coreintegrations.ErrSlackUnavailable)
	}

	// Transport-level failures: DNS, refused connections, timeouts.
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: network", coreintegrations.ErrSlackUnavailable)
	}
	return fmt.Errorf("%w: unexpected", coreintegrations.ErrSlackUnavailable)
}
