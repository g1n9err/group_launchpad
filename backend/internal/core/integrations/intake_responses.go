package integrations

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"launchpad/internal/core"
)

type IntakeResponse struct {
	ID          string          `json:"responseId"`
	Email       string          `json:"respondentEmail"`
	CreatedAt   time.Time       `json:"createTime"`
	SubmittedAt time.Time       `json:"lastSubmittedTime"`
	Answers     json.RawMessage `json:"answers"`
}

type IntakeResponseSummary struct {
	Saved     int
	Unchanged int
	Ignored   int
}

func (s *FormsService) ImportResponses(ctx context.Context, groupID uuid.UUID) (IntakeResponseSummary, error) {
	var total IntakeResponseSummary
	form, err := s.Store.GetIntakeForm(ctx, groupID)
	if err != nil {
		return total, err
	}
	if form.ID == "" || form.ResponderURL == "" || form.VerifiedSince == nil {
		return total, &core.AppError{Code: core.CodeConflict, Message: "publish the intake form with verified email collection before importing responses"}
	}
	token, err := s.Tokens.AccessToken(ctx)
	if err != nil {
		return total, err
	}
	started := time.Now()
	verified, err := s.Gateway.VerifiedEmail(ctx, token, form.ID)
	if err := s.record(ctx, groupID, "forms.check_email_collection", started, err); err != nil {
		return total, err
	}
	if !verified {
		return total, &core.AppError{Code: core.CodeConflict, Message: "the intake form is no longer collecting verified email addresses"}
	}

	seenPages := map[string]bool{}
	page := ""
	for {
		started = time.Now()
		responses, next, err := s.Gateway.ListResponses(ctx, token, form.ID, *form.VerifiedSince, page)
		if err := s.record(ctx, groupID, "forms.list_responses", started, err); err != nil {
			return total, err
		}
		for _, response := range responses {
			response.Email = NormalizeEmail(response.Email)
			if response.ID == "" || response.Email == "" || response.CreatedAt.Before(*form.VerifiedSince) ||
				response.SubmittedAt.Before(response.CreatedAt) {
				total.Ignored++
				continue
			}
			if len(response.Answers) == 0 || string(response.Answers) == "null" {
				response.Answers = json.RawMessage("{}")
			}
			matched, changed, err := s.Store.SaveIntakeResponse(ctx, groupID, form.ID, response)
			if err != nil {
				return total, err
			}
			switch {
			case !matched:
				total.Ignored++
			case changed:
				total.Saved++
			default:
				total.Unchanged++
			}
		}
		if next == "" {
			return total, nil
		}
		if seenPages[next] {
			return total, &core.AppError{Code: core.CodeUpstream, Message: "Google Forms repeated a response page token"}
		}
		seenPages[next] = true
		page = next
	}
}
