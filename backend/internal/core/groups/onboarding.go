package groups

import (
	"context"
	"math"
	"strings"

	"github.com/google/uuid"

	"launchpad/internal/core"
)

// JoinedOnboardingStatuses are the member states that count as "joined
// Slack" (Stage 3A/3B conventions): the transition moment and everything
// after it. pending/invited have not joined.
var JoinedOnboardingStatuses = map[string]bool{
	"joined_slack":   true,
	"form_submitted": true,
	"complete":       true,
}

// Student is one roster entry with its onboarding state.
type Student struct {
	UserID           uuid.UUID
	FullName         string
	Email            string
	OnboardingStatus string
}

// OnboardingSummary aggregates the roster (PRD 4.6).
type OnboardingSummary struct {
	ExpectedCount        int     `json:"expectedCount"`
	JoinedCount          int     `json:"joinedCount"`
	PendingCount         int     `json:"pendingCount"`
	CompletionPercentage float64 `json:"completionPercentage"`
}

// OnboardingReport is the complete visibility payload for one group.
type OnboardingReport struct {
	Group    Group
	Summary  OnboardingSummary
	Students []Student
}

// Onboarding assembles the read model: the group plus its student roster,
// with the summary derived from the same snapshot (counts can never disagree
// with the roster they came from).
func (s *Service) Onboarding(ctx context.Context, rawID string) (OnboardingReport, error) {
	id, err := uuid.Parse(strings.TrimSpace(rawID))
	if err != nil {
		return OnboardingReport{}, core.Validation("invalid group id", map[string]string{"groupId": "must be a UUID"})
	}
	g, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return OnboardingReport{}, err
	}
	roster, err := s.repo.StudentRoster(ctx, id)
	if err != nil {
		return OnboardingReport{}, err
	}
	return OnboardingReport{
		Group:    g,
		Summary:  summarizeRoster(roster),
		Students: roster,
	}, nil
}

func summarizeRoster(roster []Student) OnboardingSummary {
	joined := 0
	for _, st := range roster {
		if JoinedOnboardingStatuses[st.OnboardingStatus] {
			joined++
		}
	}
	expected := len(roster)
	return OnboardingSummary{
		ExpectedCount:        expected,
		JoinedCount:          joined,
		PendingCount:         expected - joined,
		CompletionPercentage: completionPercentage(joined, expected),
	}
}

// completionPercentage rounds to one decimal (PRD example: 21/24 → 87.5).
func completionPercentage(joined, expected int) float64 {
	if expected == 0 {
		return 0
	}
	return math.Round(float64(joined)/float64(expected)*1000) / 10
}
