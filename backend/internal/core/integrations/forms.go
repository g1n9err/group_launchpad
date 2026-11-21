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
	// UpdateIntakeQuestions persists an append-only extension of a draft
	// form's questions; it must refuse published forms.
	UpdateIntakeQuestions(context.Context, uuid.UUID, []IntakeQuestion) error
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
	extended := false
	if !reflect.DeepEqual(form.Questions, questions) {
		if err := allowQuestionChange(form, questions); err != nil {
			return "", err
		}
		// The form is an unpublished draft: persist the new question set
		// before touching Google, so a crash mid-way never loses it.
		if err := s.Store.UpdateIntakeQuestions(ctx, groupID, questions); err != nil {
			return "", err
		}
		form.Questions = questions
		extended = true
	}
	// Fast path: an already-created form with the SAME questions needs no
	// Google call; an extended draft must continue to append the new items.
	if form.ID != "" && !extended && (form.Status == "created" || form.Status == "distributed" || form.Status == "closed") {
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

// allowQuestionChange governs question edits by publication state:
//
//   - DRAFT (never sent): any change — replace, edit, add, remove.
//   - PUBLISHED (sent to students): APPEND-ONLY. Adding a question is safe
//     (existing responses simply lack the new answer; Google keeps mapping
//     by question), but changing or removing existing questions would
//     orphan already-collected responses.
func allowQuestionChange(form IntakeForm, next []IntakeQuestion) error {
	published := form.ResponderURL != "" || form.VerifiedSince != nil ||
		form.Status == "distributed" || form.Status == "closed"
	if !published {
		return nil
	}
	if len(next) < len(form.Questions) || !reflect.DeepEqual(next[:len(form.Questions)], form.Questions) {
		return &core.AppError{Code: core.CodeConflict, Message: "the form was already sent to students: new questions can be added, but existing ones cannot be changed or removed"}
	}
	return nil
}

// ── Mandatory identity questions ───────────────────────────────────────────
// Every intake form MUST collect the respondent's name and email (PRD §4.5
// proposed fields; the response importer keys on them). normalizeIntakeQuestions
// guarantees their presence, order, and required flag no matter what the
// coordinator submits.

const (
	QuestionFullName = "Full name"
	QuestionEmail    = "Email address"
)

// fullNameAliases / emailAliases map common (and Armenian) spellings onto
// the canonical identity questions instead of creating duplicates.
var fullNameAliases = map[string]bool{"full name": true, "name": true, "full_name": true, "անուն": true, "անուն ազգանուն": true}
var emailAliases = map[string]bool{"email address": true, "email": true, "e-mail": true, "mail": true, "էլ. փոստ": true, "էլ փոստ": true, "էլ.հասցե": true}

// canonicalize returns (canonicalTitle, isIdentity) for a question title.
func canonicalize(title string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(title))
	switch {
	case fullNameAliases[key]:
		return QuestionFullName, true
	case emailAliases[key]:
		return QuestionEmail, true
	}
	return "", false
}

func normalizeIntakeQuestions(input []IntakeQuestion) ([]IntakeQuestion, error) {
	seenFullName, seenEmail := false, false
	custom := make([]IntakeQuestion, 0, len(input))
	for i, question := range input {
		question.Title = strings.TrimSpace(question.Title)
		if canonical, identity := canonicalize(question.Title); identity {
			// Identity questions are canonicalized: fixed title, plain
			// text, always required — and pinned to the top of the form.
			question.Title = canonical
			question.Type = "text"
			question.Required = true
			question.Options = nil
			if canonical == QuestionFullName {
				if seenFullName {
					return nil, core.Validation("invalid intake questions", map[string]string{fmt.Sprintf("questions[%d]", i): "the full-name question may appear only once"})
				}
				seenFullName = true
			} else {
				if seenEmail {
					return nil, core.Validation("invalid intake questions", map[string]string{fmt.Sprintf("questions[%d]", i): "the email question may appear only once"})
				}
				seenEmail = true
			}
			continue
		}
		if err := validateIntakeQuestion(&question, map[string]bool{}); err != nil {
			return nil, core.Validation("invalid intake questions", map[string]string{fmt.Sprintf("questions[%d]", i): err.Error()})
		}
		custom = append(custom, question)
	}
	// The identity pair ALWAYS leads the form, in a fixed order.
	normalized := make([]IntakeQuestion, 0, len(custom)+2)
	normalized = append(normalized,
		IntakeQuestion{Title: QuestionFullName, Type: "text", Required: true},
		IntakeQuestion{Title: QuestionEmail, Type: "text", Required: true},
	)
	normalized = append(normalized, custom...)
	if len(normalized) < 2 || len(normalized) > 50 {
		return nil, core.Validation("provide between 1 and 50 questions", nil)
	}
	seen := make(map[string]bool, len(normalized))
	for i := range normalized {
		key := strings.ToLower(normalized[i].Title)
		if seen[key] {
			return nil, core.Validation("invalid intake questions", map[string]string{fmt.Sprintf("questions[%d]", i): "question titles must be unique"})
		}
		seen[key] = true
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
