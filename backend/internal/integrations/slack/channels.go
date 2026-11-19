package slack

// Channel-operation methods of the Slack gateway: paginated lookup, creation
// with name_taken handling, email resolution, idempotent invites, message
// posting. All calls are rate-limit aware (bounded Retry-After honoring) and
// all errors are classified before crossing the port boundary.

import (
	"context"
	"fmt"
	"time"

	slackgo "github.com/slack-go/slack"

	coreintegrations "launchpad/internal/core/integrations"
)

// rateLimitCap bounds how long a single Retry-After may sleep — Slack can ask
// for long waits under sustained load; provisioning prefers to surface a
// transient error than hang a request.
const rateLimitCap = 5 * time.Second

// withRateLimit runs fn, honoring at most one Slack 429 Retry-After cycle.
func withRateLimit[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	res, err := fn()
	if err == nil {
		return res, nil
	}
	var rl *slackgo.RateLimitedError
	if !asRateLimited(err, &rl) {
		return zero, err
	}
	wait := rl.RetryAfter
	if wait > rateLimitCap {
		wait = rateLimitCap
	}
	if err := sleepCtx(ctx, wait); err != nil {
		return zero, err
	}
	return fn()
}

func asRateLimited(err error, out **slackgo.RateLimitedError) bool {
	if rl, ok := err.(*slackgo.RateLimitedError); ok { //nolint:errorlint — SDK returns the concrete type
		*out = rl
		return true
	}
	return false
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// LookupChannelIDs resolves names to IDs via paginated conversations.list.
func (g *Gateway) LookupChannelIDs(ctx context.Context, names []string) (map[string]string, error) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	type page struct {
		channels []slackgo.Channel
		next     string
	}
	found := map[string]string{}
	cursor := ""
	for {
		p, err := withRateLimit(ctx, func() (page, error) {
			channels, next, err := g.client.GetConversationsContext(ctx, &slackgo.GetConversationsParameters{
				Cursor:          cursor,
				ExcludeArchived: true,
				Limit:           200,
				Types:           []string{"public_channel"},
			})
			return page{channels: channels, next: next}, err
		})
		if err != nil {
			return nil, classify(err)
		}
		for _, ch := range p.channels {
			if want[ch.Name] && ch.ID != "" {
				found[ch.Name] = ch.ID
			}
		}
		if len(found) == len(want) || p.next == "" {
			break
		}
		cursor = p.next
	}
	return found, nil
}

// CreateChannel creates a public channel; name collisions surface as
// ErrSlackChannelNameTaken so the caller can reuse the existing channel.
func (g *Gateway) CreateChannel(ctx context.Context, name string) (string, error) {
	ch, err := withRateLimit(ctx, func() (*slackgo.Channel, error) {
		return g.client.CreateConversationContext(ctx, slackgo.CreateConversationParams{
			ChannelName: name,
			IsPrivate:   false,
		})
	})
	if err != nil {
		if code, ok := apiErrorCode(err); ok {
			if code == "name_taken" {
				return "", coreintegrations.ErrSlackChannelNameTaken
			}
			return "", fmt.Errorf("%w: slack api error (%s)", coreintegrations.ErrSlackUnavailable, code)
		}
		return "", classify(err)
	}
	return ch.ID, nil
}

// LookupUserByEmail resolves a workspace member by email.
func (g *Gateway) LookupUserByEmail(ctx context.Context, email string) (string, error) {
	user, err := withRateLimit(ctx, func() (*slackgo.User, error) {
		return g.client.GetUserByEmailContext(ctx, email)
	})
	if err != nil {
		if code, ok := apiErrorCode(err); ok && (code == "users_not_found" || code == "user_not_found") {
			return "", coreintegrations.ErrSlackUserNotFound
		}
		return "", classify(err)
	}
	if user == nil || user.ID == "" {
		return "", coreintegrations.ErrSlackUserNotFound
	}
	return user.ID, nil
}

// UserEmail resolves a Slack user ID to the account's email via users.info
// (the app holds users:read.email). Missing profile email → empty string.
func (g *Gateway) UserEmail(ctx context.Context, userID string) (string, error) {
	user, err := withRateLimit(ctx, func() (*slackgo.User, error) {
		return g.client.GetUserInfoContext(ctx, userID)
	})
	if err != nil {
		if code, ok := apiErrorCode(err); ok && (code == "users_not_found" || code == "user_not_found") {
			return "", coreintegrations.ErrSlackUserNotFound
		}
		return "", classify(err)
	}
	if user == nil {
		return "", nil
	}
	return user.Profile.Email, nil
}

// InviteUserToChannel invites one user; already_in is a success outcome
// (retry-safe), everything else is classified.
func (g *Gateway) InviteUserToChannel(ctx context.Context, channelID, userID string) (coreintegrations.InviteOutcome, error) {
	_, err := withRateLimit(ctx, func() (*slackgo.Channel, error) {
		return g.client.InviteUsersToConversationContext(ctx, channelID, userID)
	})
	if err != nil {
		if code, ok := apiErrorCode(err); ok {
			switch code {
			case "already_in_channel", "already_in":
				return coreintegrations.InviteOutcomeAlreadyMember, nil
			case "cant_invite", "cant_invite_self", "ura_max_invites", "not_in_channel":
				// Permanent per-call failures — surface the code (safe, no
				// request fragments) as a typed unavailable error.
				return "", fmt.Errorf("%w: invite rejected (%s)", coreintegrations.ErrSlackUnavailable, code)
			}
		}
		return "", classify(err)
	}
	return coreintegrations.InviteOutcomeInvited, nil
}

// PostMessage publishes one message and returns its timestamp.
func (g *Gateway) PostMessage(ctx context.Context, channelID, text string) (string, error) {
	res, err := withRateLimit(ctx, func() (string, error) {
		ts, _, err := g.client.PostMessageContext(ctx, channelID, slackgo.MsgOptionText(text, false))
		return ts, err
	})
	if err != nil {
		if code, ok := apiErrorCode(err); ok {
			return "", fmt.Errorf("%w: slack api error (%s)", coreintegrations.ErrSlackUnavailable, code)
		}
		return "", classify(err)
	}
	return res, nil
}

// apiErrorCode extracts the Slack error code from SlackErrorResponse, if the
// error is one.
func apiErrorCode(err error) (string, bool) {
	var slackErr slackgo.SlackErrorResponse
	if asSlackError(err, &slackErr) {
		return slackErr.Err, true
	}
	return "", false
}

func asSlackError(err error, out *slackgo.SlackErrorResponse) bool {
	if se, ok := err.(slackgo.SlackErrorResponse); ok { //nolint:errorlint — SDK returns the value type
		*out = se
		return true
	}
	if se, ok := err.(*slackgo.SlackErrorResponse); ok { //nolint:errorlint — defensive
		*out = *se
		return true
	}
	return false
}
