package transport_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"launchpad/internal/core/integrations"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

type fakeForms struct {
	creates      atomic.Int32
	updates      atomic.Int32
	publishes    atomic.Int32
	rejectUpdate atomic.Bool
	responses    []integrations.IntakeResponse
	lastSet      []integrations.IntakeQuestion
}

func (*fakeForms) AccessToken(context.Context) (string, error) { return "test-token", nil }

func (*fakeForms) EnableVerifiedEmail(context.Context, string, string) error   { return nil }
func (*fakeForms) VerifiedEmail(context.Context, string, string) (bool, error) { return true, nil }
func (f *fakeForms) ListResponses(context.Context, string, string, time.Time, string) ([]integrations.IntakeResponse, string, error) {
	return f.responses, "", nil
}

const testResponderURL = "https://docs.google.com/forms/d/e/test-form/viewform"

func (f *fakeForms) Publish(context.Context, string, string) (string, error) {
	f.publishes.Add(1)
	return testResponderURL, nil
}

func (f *fakeForms) CreateDraft(context.Context, string, string, string) (string, bool, error) {
	f.creates.Add(1)
	return "", false, errors.New("response lost after create")
}

func (f *fakeForms) FindDraft(context.Context, string, string) (string, error) {
	return "recovered-form-id", nil
}

func (f *fakeForms) EnsureQuestions(_ context.Context, _, id string, questions []integrations.IntakeQuestion, _ bool) error {
	if id != "recovered-form-id" || len(questions) < 2 ||
		questions[0].Title != "Full name" || !questions[0].Required ||
		questions[1].Title != "Email address" || !questions[1].Required {
		return errors.New("unexpected question payload")
	}
	if f.rejectUpdate.Swap(false) {
		return errors.New("temporary Google Forms error")
	}
	f.lastSet = questions
	f.updates.Add(1)
	return nil
}

func TestFormsProvisionRecoversAndReusesDraft(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups")
	repo := store.New(pool)
	gateway := &fakeForms{}
	service := &integrations.FormsService{Tokens: gateway, Gateway: gateway, Store: repo, Operations: repo}
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), nil, pool.Ping,
		transport.GroupProvisioning{Forms: service})
	srv := httptestNew(app, t)

	code, group := doJSON(t, srv, http.MethodPost, "/api/groups", `{"name":"Forms Provision","courseName":"Go","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, group)
	}
	formsPath := "/api/groups/" + group["id"].(string) + "/forms"
	path := formsPath + "/provision"
	publishPath := formsPath + "/publish"
	if code, _ := doJSON(t, srv, http.MethodGet, formsPath, ""); code != http.StatusNotFound {
		t.Fatalf("form state before provisioning: %d, want 404", code)
	}
	if code, _ := doJSON(t, srv, http.MethodPost, publishPath, ""); code != http.StatusNotFound {
		t.Fatalf("publish before provisioning: %d, want 404", code)
	}
	// Դատարկ ցուցակն այժմ վավեր է (ավտոմատ ավելանում են պարտադիր Անուն+Email)․
	// 422-ն այժմ ստուգվում է անվավեր հարցով։
	if code, _ := doJSON(t, srv, http.MethodPost, path, `{"questions":[{"title":"Time","type":"single_choice","options":["Only"]}]}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid question: %d, want 422", code)
	}
	const questions = `{"questions":[{"title":" Name ","type":"text","required":true},
		{"title":"Time","type":"single_choice","options":["Morning"," Evening "]}]}`
	if code, _ := doJSON(t, srv, http.MethodPost, path, questions); code != http.StatusBadGateway {
		t.Fatalf("interrupted create: %d, want 502", code)
	}
	if code, _ := doJSON(t, srv, http.MethodPost, publishPath, ""); code != http.StatusConflict {
		t.Fatalf("publish before draft completion: %d, want 409", code)
	}
	for range 2 {
		code, result := doJSON(t, srv, http.MethodPost, path, questions)
		if code != http.StatusOK || result["formId"] != "recovered-form-id" || result["editUrl"] == nil {
			t.Fatalf("draft: %d %v", code, result)
		}
	}
	// ՄԻՆՉ հրապարակումը՝ սևագրի հարցերը ազատ փոխվում են (լրիվ փոխարինում)։
	if code, _ := doJSON(t, srv, http.MethodPost, path, `{"questions":[{"title":"Different","type":"paragraph"}]}`); code != http.StatusOK {
		t.Fatalf("changing draft questions before publish: %d, want 200", code)
	}
	if len(gateway.lastSet) != 3 || gateway.lastSet[2].Title != "Different" {
		t.Fatalf("draft rebuild payload = %+v", gateway.lastSet)
	}
	for range 2 {
		code, result := doJSON(t, srv, http.MethodPost, publishPath, "")
		if code != http.StatusOK || result["formId"] != "recovered-form-id" || result["responderUrl"] != testResponderURL {
			t.Fatalf("publish: %d %v", code, result)
		}
	}
	// ՀՐԱՊԱՐԱԿՈՒՄԻՑ ՀԵՏՈ՝ հարցերը կողպվում են (պատասխանների ամբողջականություն)։
	if code, _ := doJSON(t, srv, http.MethodPost, path, `{"questions":[{"title":"Another","type":"text"}]}`); code != http.StatusConflict {
		t.Fatalf("changing questions after publish: %d, want 409", code)
	}
	code, state := doJSON(t, srv, http.MethodGet, formsPath, "")
	if code != http.StatusOK || state["status"] != "created" || state["verifiedEmail"] != true ||
		state["responderUrl"] != testResponderURL || state["editUrl"] == nil || len(state["questions"].([]any)) != 3 {
		t.Fatalf("saved intake form state: %d %v", code, state)
	}
	if gateway.publishes.Load() != 1 {
		t.Fatalf("publish calls=%d, want one", gateway.publishes.Load())
	}
	if rows := countRowsSimple(t, pool, "SELECT count(*) FROM intake_forms WHERE form_url='https://docs.google.com/forms/d/e/test-form/viewform'"); rows != 1 {
		t.Fatalf("saved %d published URLs, want 1", rows)
	}
	if gateway.creates.Load() != 1 || gateway.updates.Load() < 2 {
		t.Fatalf("create calls=%d update calls=%d, want 1 create and >=2 updates", gateway.creates.Load(), gateway.updates.Load())
	}
	if rows := countRowsSimple(t, pool, "SELECT count(*) FROM intake_forms WHERE external_form_id='recovered-form-id' AND status='created' AND jsonb_array_length(questions)=3"); rows != 1 {
		t.Fatalf("saved %d intake forms, want 1", rows)
	}

	const extended = `{"questions":[{"title":"Different","type":"paragraph"},{"title":"City","type":"text"}]}`
	gateway.rejectUpdate.Store(true)
	if code, _ := doJSON(t, srv, http.MethodPost, path, extended); code != http.StatusBadGateway {
		t.Fatalf("interrupted append: %d, want 502", code)
	}
	if code, state := doJSON(t, srv, http.MethodGet, formsPath, ""); code != http.StatusOK || len(state["questions"].([]any)) != 3 {
		t.Fatalf("questions after failed append: %d %v", code, state)
	}
	for _, payload := range []string{extended, ""} {
		if code, _ := doJSON(t, srv, http.MethodPost, path, payload); code != http.StatusOK {
			t.Fatalf("retry or launch without questions: %d, want 200", code)
		}
	}
	if len(gateway.lastSet) != 4 || gateway.lastSet[3].Title != "City" {
		t.Fatalf("Google questions after retry and launch: %+v", gateway.lastSet)
	}
	if code, state := doJSON(t, srv, http.MethodGet, formsPath, ""); code != http.StatusOK || len(state["questions"].([]any)) != 4 || state["responderUrl"] != testResponderURL {
		t.Fatalf("published form after retry: %d %v", code, state)
	}
}

func TestImportFormsResponsesMatchesVerifiedRosterOnce(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users")
	repo := store.New(pool)
	created := time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC)
	response := func(id, email string, instant time.Time) integrations.IntakeResponse {
		return integrations.IntakeResponse{ID: id, Email: email, CreatedAt: instant, SubmittedAt: instant, Answers: []byte(`{"q":{"textAnswers":{"answers":[{"value":"test"}]}}}`)}
	}
	gateway := &fakeForms{responses: []integrations.IntakeResponse{
		response("pending-form", " FIRST@EXAMPLE.TEST ", created),
		response("joined-form", "second@example.test", created),
		response("outside", "outsider@example.test", created),
		response("old", "third@example.test", created.Add(-365*24*time.Hour)),
		response("none", "", created),
	}}
	service := &integrations.FormsService{Tokens: gateway, Gateway: gateway, Store: repo, Operations: repo}
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), nil, pool.Ping,
		transport.GroupProvisioning{Forms: service})
	srv := httptestNew(app, t)
	id := createGroupWithRoster(t, srv, "Forms Response Roster", []string{"first@example.test", "second@example.test", "third@example.test"})
	if _, err := pool.Exec(t.Context(), `INSERT INTO intake_forms (group_id,external_form_id,form_url,status,verified_responses_since)
		VALUES ($1,'test-form',$2,'created','2026-01-01 UTC')`, id, testResponderURL); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE group_members SET onboarding_status='joined_slack'
		WHERE group_id=$1 AND user_id=(SELECT id FROM users WHERE email='second@example.test')`, id); err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/groups/" + id + "/forms/responses/import"
	for attempt := range 2 {
		code, result := doJSON(t, srv, http.MethodPost, endpoint, "")
		if code != http.StatusOK || result["saved"] != float64(2-2*attempt) ||
			result["unchanged"] != float64(2*attempt) || result["ignored"] != float64(3) {
			t.Fatalf("import %d: %d %v", attempt, code, result)
		}
	}
	var memberID string
	var status string
	if err := pool.QueryRow(t.Context(), `SELECT gm.id::text, gm.onboarding_status FROM group_members gm JOIN users u ON u.id=gm.user_id
		WHERE gm.group_id=$1 AND u.email='first@example.test'`, id).Scan(&memberID, &status); err != nil || status != "form_submitted" {
		t.Fatalf("form-only student: status=%q err=%v", status, err)
	}
	code, report := doJSON(t, srv, http.MethodGet, "/api/groups/"+id+"/onboarding", "")
	if code != http.StatusOK || report["summary"].(map[string]any)["joinedCount"] != float64(1) {
		t.Fatalf("form submission must not count as Slack join: %d %v", code, report)
	}
	if err := repo.MarkGroupMemberJoinedSlack(t.Context(), mustUUID(t, memberID)); err != nil {
		t.Fatal(err)
	}
	if count := countRowsSimple(t, pool, "SELECT count(*) FROM group_members WHERE onboarding_status='complete'"); count != 2 {
		t.Fatalf("completed %d students, want 2", count)
	}
	if count := countRowsSimple(t, pool, "SELECT count(*) FROM intake_form_responses"); count != 2 {
		t.Fatalf("saved %d responses, want 2", count)
	}
}
