// Slack membership audit (PRD §4.7: "Slack staff: required staff are
// members", "Slack lecturers: assigned lecturers are members").
package slack

import (
	"context"

	slackgo "github.com/slack-go/slack"

	coreaudit "launchpad/internal/core/audit"
)

// memberPage is one conversations.members page.
type memberPage struct {
	ids  []string
	next string
}

// channelMemberIDs returns every member user ID of a channel, paginating
// conversations.members until exhausted.
func (g *Gateway) channelMemberIDs(ctx context.Context, channelID string) (map[string]bool, error) {
	members := map[string]bool{}
	cursor := ""
	for {
		page, err := withRateLimit(ctx, func() (memberPage, error) {
			params := &slackgo.GetUsersInConversationParameters{
				ChannelID: channelID, Limit: 200, Cursor: cursor,
			}
			ids, next, err := g.client.GetUsersInConversationContext(ctx, params)
			return memberPage{ids: ids, next: next}, err
		})
		if err != nil {
			return nil, classify(err)
		}
		for _, id := range page.ids {
			members[id] = true
		}
		if page.next == "" {
			return members, nil
		}
		cursor = page.next
	}
}

// CheckMembers verifies every expected member against every channel's live
// membership. One aggregate Result per role — statuses only, no emails
// (PII-safe). Errors degrade to "unavailable", never a false "ok".
func (g *Gateway) CheckMembers(ctx context.Context, channels []coreaudit.Resource, expected []coreaudit.ExpectedMember) []coreaudit.Result {
	// Resolve each channel's member set once.
	memberSets := make([]map[string]bool, len(channels))
	var unavailable bool
	for i, channel := range channels {
		ids, err := g.channelMemberIDs(ctx, channel.ID)
		if err != nil {
			unavailable = true
			continue
		}
		memberSets[i] = ids
	}
	if unavailable {
		return roleResults(expected, coreaudit.StatusUnavailable)
	}

	// Resolve expected emails to workspace user IDs (one call each).
	userIDs := make(map[string]string, len(expected)) // email → slack user id
	lookupFailed := false
	for _, m := range expected {
		if _, done := userIDs[m.Email]; done {
			continue
		}
		id, err := g.LookupUserByEmail(ctx, m.Email)
		if err != nil {
			lookupFailed = true // missing from workspace entirely
			userIDs[m.Email] = ""
			continue
		}
		userIDs[m.Email] = id
	}
	if lookupFailed {
		// A member missing from the workspace cannot be in any channel —
		// report per role conservatively below; the empty ID marks it.
	}

	missing := map[string]int{} // role → missing count
	expectedN := map[string]int{}
	for _, m := range expected {
		expectedN[m.Role]++
		id := userIDs[m.Email]
		var absent bool
		if id == "" {
			absent = true // not in the workspace
		} else {
			for _, set := range memberSets {
				if !set[id] {
					absent = true
					break
				}
			}
		}
		if absent {
			missing[m.Role]++
		}
	}
	_ = lookupFailed
	results := make([]coreaudit.Result, 0, len(expectedN))
	for role := range expectedN {
		status := coreaudit.StatusOK
		if missing[role] > 0 {
			status = coreaudit.StatusMissingOrInaccessible
		}
		results = append(results, coreaudit.Result{
			Resource: coreaudit.Resource{Provider: "slack", Kind: "membership", Key: role},
			Status:   status,
		})
	}
	return results
}

func roleResults(expected []coreaudit.ExpectedMember, status string) []coreaudit.Result {
	seen := map[string]bool{}
	var out []coreaudit.Result
	for _, m := range expected {
		if seen[m.Role] {
			continue
		}
		seen[m.Role] = true
		out = append(out, coreaudit.Result{
			Resource: coreaudit.Resource{Provider: "slack", Kind: "membership", Key: m.Role},
			Status:   status,
		})
	}
	return out
}
