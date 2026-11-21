package integrations

// Provision's append-only question extension: a DRAFT form may gain new
// questions; published forms and edits/removals of existing questions are
// rejected. All fakes — no Google calls.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

type appendFormsStore struct {
	form     IntakeForm
	claimed  bool
	updated  []IntakeQuestion
	updateOK bool
}

func (s *appendFormsStore) MarkIntakeVerified(context.Context, uuid.UUID, string) error { return nil }
func (s *appendFormsStore) SaveIntakeResponse(context.Context, uuid.UUID, string, IntakeResponse) (bool, bool, error) {
	return false, false, nil
}
func (s *appendFormsStore) GetIntakeForm(_ context.Context, _ uuid.UUID) (IntakeForm, error) {
	return s.form, nil
}
func (s *appendFormsStore) SaveIntakeResponderURL(_ context.Context, _ uuid.UUID, _, _ string) (string, error) {
	return "", nil
}
func (s *appendFormsStore) ClaimIntakeForm(_ context.Context, _ uuid.UUID, _ []IntakeQuestion) (IntakeForm, bool, error) {
	return s.form, s.claimed, nil
}
func (s *appendFormsStore) ReleaseIntakeForm(context.Context, uuid.UUID) error        { return nil }
func (s *appendFormsStore) SaveIntakeFormID(context.Context, uuid.UUID, string) error { return nil }
func (s *appendFormsStore) CompleteIntakeForm(context.Context, uuid.UUID, string) error {
	s.updateOK = true
	return nil
}
func (s *appendFormsStore) UpdateIntakeQuestions(_ context.Context, _ uuid.UUID, questions []IntakeQuestion) error {
	s.updated = questions
	return nil
}

type appendFormsGW struct {
	ensured [][]IntakeQuestion
}

func (g *appendFormsGW) EnableVerifiedEmail(context.Context, string, string) error { return nil }
func (g *appendFormsGW) VerifiedEmail(context.Context, string, string) (bool, error) {
	return true, nil
}
func (g *appendFormsGW) ListResponses(context.Context, string, string, time.Time, string) ([]IntakeResponse, string, error) {
	return nil, "", nil
}
func (g *appendFormsGW) Publish(context.Context, string, string) (string, error) { return "", nil }
func (g *appendFormsGW) CreateDraft(context.Context, string, string, string) (string, bool, error) {
	return "form-1", false, nil
}
func (g *appendFormsGW) FindDraft(context.Context, string, string) (string, error) {
	return "form-1", nil
}
func (g *appendFormsGW) EnsureQuestions(_ context.Context, _ string, _ string, questions []IntakeQuestion) error {
	g.ensured = append(g.ensured, questions)
	return nil
}

func draftForm() IntakeForm {
	return IntakeForm{
		ID: "form-1", Status: "created",
		Questions: []IntakeQuestion{
			{Title: "Full name", Type: "text", Required: true},
			{Title: "Email address", Type: "text", Required: true},
		},
	}
}

func TestProvisionAppendsQuestionsToDraft(t *testing.T) {
	store := &appendFormsStore{form: draftForm()}
	gw := &appendFormsGW{}
	svc := &FormsService{Tokens: stubFormsToken{}, Gateway: gw, Store: store, Operations: noopRecorder{}}

	next := append(append([]IntakeQuestion(nil), store.form.Questions...),
		IntakeQuestion{Title: "Phone number", Type: "text"})
	if _, err := svc.Provision(context.Background(), uuid.New(), "Group", next); err != nil {
		t.Fatalf("Provision append: %v", err)
	}
	if len(store.updated) != 3 || store.updated[2].Title != "Phone number" {
		t.Fatalf("stored questions = %+v, want 3 with Phone number appended", store.updated)
	}
	if len(gw.ensured) != 1 || len(gw.ensured[0]) != 3 {
		t.Fatalf("EnsureQuestions calls = %+v, want the extended set", gw.ensured)
	}
}

// Սևագրի հարցերը ԿԱՐՈՂ ԵՆ ազատ փոխվել (ջնջում/անվանափոխում/փոխարինում)՝
// մինչև հրապարակումը. ոչ մի պատասխան դեռ չկա։
func TestProvisionAllowsFullQuestionReplacementOnDraft(t *testing.T) {
	withPhone := draftForm()
	withPhone.Questions = append(withPhone.Questions, IntakeQuestion{Title: "Phone number", Type: "text"})
	store := &appendFormsStore{form: withPhone}
	gw := &appendFormsGW{}
	svc := &FormsService{Tokens: stubFormsToken{}, Gateway: gw, Store: store, Operations: noopRecorder{}}

	// Full replacement: "Phone number" dropped, "City" added instead.
	if _, err := svc.Provision(context.Background(), uuid.New(), "Group", []IntakeQuestion{
		{Title: "Full name", Type: "text", Required: true},
		{Title: "Email address", Type: "text", Required: true},
		{Title: "City", Type: "text"},
	}); err != nil {
		t.Fatalf("replacement rejected: %v", err)
	}
	if len(store.updated) != 3 || store.updated[2].Title != "City" {
		t.Fatalf("stored = %+v, want City replacing Phone number", store.updated)
	}
	if len(gw.ensured) != 1 || gw.ensured[0][2].Title != "City" {
		t.Fatalf("gateway set = %+v", gw.ensured)
	}
}

// ՈՒՂԱՐԿՎԱԾ ֆորմային ՆՈՐ հարց ավելացնելը թույլատրված է (պատասխանները
// չեն խանգարվում)․ փոխել/ջնջել առկաները՝ միայն 409։
func TestProvisionPublishedFormAppendAndRejectEdit(t *testing.T) {
	published := draftForm()
	published.Status = "distributed"
	published.ResponderURL = "https://docs.google.com/forms/d/x/viewform"
	published.Questions = append(published.Questions, IntakeQuestion{Title: "City", Type: "text"}) // 3 հարց
	store := &appendFormsStore{form: published}
	gw := &appendFormsGW{}
	svc := &FormsService{Tokens: stubFormsToken{}, Gateway: gw, Store: store, Operations: noopRecorder{}}

	// ԱՎԵԼԱՑՈՒՄ՝ հաջող։
	appended := append(append([]IntakeQuestion(nil), published.Questions...),
		IntakeQuestion{Title: "Phone number", Type: "text"})
	if _, err := svc.Provision(context.Background(), uuid.New(), "Group", appended); err != nil {
		t.Fatalf("published append rejected: %v", err)
	}
	if len(store.updated) != 4 || store.updated[3].Title != "Phone number" {
		t.Fatalf("stored = %+v, want appended set", store.updated)
	}
	if len(gw.ensured) != 1 || len(gw.ensured[0]) != 4 {
		t.Fatalf("gateway = %+v, want appended set sent to Google", gw.ensured)
	}

	// ՓՈԽՈՒՄ (ջնջում)՝ մերժվում է, պահեստը (store) չի փոխվում։
	// (ջնջումը պետք է վերապրի normalize-ը․ [Full name, Email, City]-ից հանում ենք City-ն)
	store2 := &appendFormsStore{form: published}
	svc2 := &FormsService{Tokens: stubFormsToken{}, Gateway: &appendFormsGW{}, Store: store2, Operations: noopRecorder{}}
	if _, err := svc2.Provision(context.Background(), uuid.New(), "Group", published.Questions[:2]); err == nil {
		t.Fatal("published removal accepted")
	}
	renamed := append([]IntakeQuestion(nil), published.Questions...)
	renamed[2].Title = "Renamed" // City → Renamed (non-alias, stays custom)
	if _, err := svc2.Provision(context.Background(), uuid.New(), "Group", renamed); err == nil {
		t.Fatal("published rename accepted")
	}
	if len(store2.updated) != 0 {
		t.Fatalf("store changed despite rejection: %+v", store2.updated)
	}
}

type stubFormsToken struct{}

func (stubFormsToken) AccessToken(context.Context) (string, error) { return "token", nil }

type noopRecorder struct{}

func (noopRecorder) RecordExternalOperation(context.Context, ExternalOperation) error { return nil }
