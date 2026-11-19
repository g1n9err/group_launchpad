package integrations

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"launchpad/internal/core"
)

type IntakeQuestion struct {
	Title    string   `json:"title"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
}

type IntakeForm struct {
	ID, Status, ResponderURL string
	Questions                []IntakeQuestion
	VerifiedSince            *time.Time
}

type FormsGateway interface {
	EnableVerifiedEmail(context.Context, string, string) error
	VerifiedEmail(context.Context, string, string) (bool, error)
	ListResponses(context.Context, string, string, time.Time, string) ([]IntakeResponse, string, error)
	Publish(ctx context.Context, token, id string) (string, error)
	CreateDraft(ctx context.Context, token, title, documentTitle string) (id string, rejected bool, err error)
	FindDraft(ctx context.Context, token, documentTitle string) (id string, err error)
	EnsureQuestions(ctx context.Context, token, id string, questions []IntakeQuestion) error
}

type IntakeFormStore interface {
	MarkIntakeVerified(context.Context, uuid.UUID, string) error
	SaveIntakeResponse(context.Context, uuid.UUID, string, IntakeResponse) (bool, bool, error)
	GetIntakeForm(context.Context, uuid.UUID) (IntakeForm, error)
	SaveIntakeResponderURL(context.Context, uuid.UUID, string, string) (string, error)
	ClaimIntakeForm(context.Context, uuid.UUID, []IntakeQuestion) (IntakeForm, bool, error)
	ReleaseIntakeForm(context.Context, uuid.UUID) error
	SaveIntakeFormID(context.Context, uuid.UUID, string) error
	CompleteIntakeForm(context.Context, uuid.UUID, string) error
}

type FormsService struct {
	Tokens     GoogleAccessTokenSource
	Gateway    FormsGateway
	Store      IntakeFormStore
	Operations ExternalOperationRecorder
}

func (s *FormsService) State(ctx context.Context, groupID uuid.UUID) (IntakeForm, error) {
	return s.Store.GetIntakeForm(ctx, groupID)
}

func (s *FormsService) Provision(ctx context.Context, groupID uuid.UUID, name string, input []IntakeQuestion) (string, error) {
	questions, err := normalizeIntakeQuestions(input)
	if err != nil {
		return "", err
	}
	token, err := s.Tokens.AccessToken(ctx)
	if err != nil {
		return "", err
	}
	form, claimed, err := s.Store.ClaimIntakeForm(ctx, groupID, questions)
	if err != nil {
		return "", err
	}
	if !reflect.DeepEqual(form.Questions, questions) {
		return "", &core.AppError{Code: core.CodeConflict, Message: "the group already has an intake form with different questions"}
	}
	if form.ID != "" && (form.Status == "created" || form.Status == "distributed" || form.Status == "closed") {
		return form.ID, nil
	}

	id := form.ID
	if id == "" {
		documentTitle := "Launchpad intake " + groupID.String()
		started := time.Now()
		if claimed {
			var rejected bool
			id, rejected, err = s.Gateway.CreateDraft(ctx, token, name+" — Student intake", documentTitle)
			if rejected {
				if releaseErr := s.Store.ReleaseIntakeForm(ctx, groupID); releaseErr != nil {
					return "", releaseErr
				}
			}
			err = s.record(ctx, groupID, "forms.create_draft", started, err)
		} else {
			id, err = s.Gateway.FindDraft(ctx, token, documentTitle)
			err = s.record(ctx, groupID, "forms.find_draft", started, err)
		}
		if err != nil {
			return "", err
		}
		if id == "" {
			return "", &core.AppError{Code: core.CodeConflict, Message: "intake form creation is still pending or its outcome is unknown; retry to recover the existing form"}
		}
		if err := s.Store.SaveIntakeFormID(ctx, groupID, id); err != nil {
			return "", err
		}
	}

	started := time.Now()
	err = s.Gateway.EnsureQuestions(ctx, token, id, questions)
	if err := s.record(ctx, groupID, "forms.ensure_questions", started, err); err != nil {
		return "", err
	}
	if err := s.Store.CompleteIntakeForm(ctx, groupID, id); err != nil {
		return "", err
	}
	return id, nil
}

// Publish opens a completed draft for responses and saves the responder link.
func (s *FormsService) Publish(ctx context.Context, groupID uuid.UUID) (IntakeForm, error) {
	form, err := s.Store.GetIntakeForm(ctx, groupID)
	if err != nil {
		return IntakeForm{}, err
	}
	if form.ID == "" || (form.Status != "created" && form.Status != "distributed") {
		return IntakeForm{}, &core.AppError{Code: core.CodeConflict, Message: "intake form must be a completed draft before it can be published"}
	}
	if form.ResponderURL != "" && form.VerifiedSince != nil {
		return form, nil
	}

	token, err := s.Tokens.AccessToken(ctx)
	if err != nil {
		return IntakeForm{}, err
	}
	if form.VerifiedSince == nil {
		started := time.Now()
		err := s.Gateway.EnableVerifiedEmail(ctx, token, form.ID)
		if err := s.record(ctx, groupID, "forms.enable_verified_email", started, err); err != nil {
			return IntakeForm{}, err
		}
		if err := s.Store.MarkIntakeVerified(ctx, groupID, form.ID); err != nil {
			return IntakeForm{}, err
		}
	}
	started := time.Now()
	link, err := s.Gateway.Publish(ctx, token, form.ID)
	if err := s.record(ctx, groupID, "forms.publish", started, err); err != nil {
		return IntakeForm{}, err
	}
	form.ResponderURL, err = s.Store.SaveIntakeResponderURL(ctx, groupID, form.ID, link)
	return form, err
}

func (s *FormsService) record(ctx context.Context, groupID uuid.UUID, operation string, started time.Time, err error) error {
	op := ExternalOperation{GroupID: &groupID, Provider: "google_forms", Operation: operation, DurationMS: time.Since(started).Milliseconds()}
	if err != nil {
		op.Error = err.Error()
	}
	_ = s.Operations.RecordExternalOperation(ctx, op)
	if err != nil {
		return &core.AppError{Code: core.CodeUpstream, Message: err.Error()}
	}
	return nil
}

func normalizeIntakeQuestions(input []IntakeQuestion) ([]IntakeQuestion, error) {
	if input == nil {
		input = []IntakeQuestion{
			{Title: "Full name", Type: "text", Required: true},
			{Title: "Email address", Type: "text", Required: true},
		}
	}
	if len(input) == 0 || len(input) > 50 {
		return nil, core.Validation("provide between 1 and 50 questions", nil)
	}
	seen := make(map[string]bool, len(input))
	normalized := make([]IntakeQuestion, 0, len(input))
	for i, question := range input {
		question.Title = strings.TrimSpace(question.Title)
		if err := validateIntakeQuestion(&question, seen); err != nil {
			return nil, core.Validation("invalid intake questions", map[string]string{fmt.Sprintf("questions[%d]", i): err.Error()})
		}
		seen[strings.ToLower(question.Title)] = true
		normalized = append(normalized, question)
	}
	return normalized, nil
}

func validateIntakeQuestion(question *IntakeQuestion, seen map[string]bool) error {
	if question.Title == "" || utf8.RuneCountInString(question.Title) > 300 || seen[strings.ToLower(question.Title)] {
		return errors.New("title must be unique and contain between 1 and 300 characters")
	}
	switch question.Type {
	case "text", "paragraph":
		if len(question.Options) != 0 {
			return errors.New("text questions cannot have options")
		}
		question.Options = nil
	case "single_choice":
		if len(question.Options) < 2 || len(question.Options) > 20 {
			return errors.New("single-choice questions require 2 to 20 options")
		}
		options := make([]string, 0, len(question.Options))
		optionSeen := make(map[string]bool, len(question.Options))
		for _, raw := range question.Options {
			option := strings.TrimSpace(raw)
			key := strings.ToLower(option)
			if option == "" || utf8.RuneCountInString(option) > 300 || optionSeen[key] {
				return errors.New("options must be unique and contain between 1 and 300 characters")
			}
			optionSeen[key] = true
			options = append(options, option)
		}
		question.Options = options
	default:
		return errors.New("type must be text, paragraph, or single_choice")
	}
	return nil
}
