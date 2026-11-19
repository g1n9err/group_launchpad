package slack

import (
	"context"
	"errors"

	slackgo "github.com/slack-go/slack"

	"launchpad/internal/core/audit"
)

func (g *Gateway) CheckResource(ctx context.Context, resource audit.Resource) string {
	channel, err := g.client.GetConversationInfoContext(ctx, &slackgo.GetConversationInfoInput{ChannelID: resource.ID})
	if err != nil {
		var response slackgo.SlackErrorResponse
		if errors.As(err, &response) && response.Err == "channel_not_found" {
			return "missing_or_inaccessible"
		}
		return "unavailable"
	}
	if channel == nil || channel.ID != resource.ID {
		return "unavailable"
	}
	if channel.IsArchived {
		return "archived"
	}
	if channel.Name != resource.Key {
		return "misconfigured"
	}
	return "ok"
}
