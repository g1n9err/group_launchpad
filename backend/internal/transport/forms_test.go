package transport_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"launchpad/internal/core/integrations"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

type fakeForms struct {
	creates atomic.Int32
	updates atomic.Int32
}

func (*fakeForms) AccessToken(context.Context) (string, error) { return "test-token", nil }

func (f *fakeForms) CreateDraft(context.Context, string, string, string) (string, bool, error) {
	f.creates.Add(1)
	return "", false, errors.New("response lost after create")
}

func (f *fakeForms) FindDraft(context.Context, string, string) (string, error) {
	return "recovered-form-id", nil
}

func (f *fakeForms) EnsureQuestions(_ context.Context, _, id string, questions []integrations.IntakeQuestion) error {
	if id != "recovered-form-id" || len(questions) != 2 || questions[0].Title != "Name" ||
		!questions[0].Required || questions[1].Type != "single_choice" ||
		strings.Join(questions[1].Options, ",") != "Morning,Evening" {
		return errors.New("unexpected question payload")
	}
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
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), pool.Ping,
		transport.GroupProvisioning{Forms: service})
	srv := httptestNew(app, t)

	code, group := doJSON(t, srv, http.MethodPost, "/api/groups", `{"name":"Forms Provision","courseName":"Go","startDate":"2026-12-01"}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, group)
	}
	path := "/api/groups/" + group["id"].(string) + "/forms/provision"
	if code, _ := doJSON(t, srv, http.MethodPost, path, `{"questions":[]}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("empty question list: %d, want 422", code)
	}
	const questions = `{"questions":[{"title":" Name ","type":"text","required":true},
		{"title":"Time","type":"single_choice","options":["Morning"," Evening "]}]}`
	if code, _ := doJSON(t, srv, http.MethodPost, path, questions); code != http.StatusBadGateway {
		t.Fatalf("interrupted create: %d, want 502", code)
	}
	for range 2 {
		code, result := doJSON(t, srv, http.MethodPost, path, questions)
		if code != http.StatusOK || result["formId"] != "recovered-form-id" || result["editUrl"] == nil {
			t.Fatalf("draft: %d %v", code, result)
		}
	}
	if code, _ := doJSON(t, srv, http.MethodPost, path, `{"questions":[{"title":"Different","type":"paragraph"}]}`); code != http.StatusConflict {
		t.Fatalf("changing saved questions: %d, want 409", code)
	}
	if gateway.creates.Load() != 1 || gateway.updates.Load() != 1 {
		t.Fatalf("create calls=%d update calls=%d, want one each", gateway.creates.Load(), gateway.updates.Load())
	}
	if rows := countRowsSimple(t, pool, "SELECT count(*) FROM intake_forms WHERE external_form_id='recovered-form-id' AND status='created' AND jsonb_array_length(questions)=2"); rows != 1 {
		t.Fatalf("saved %d intake forms, want 1", rows)
	}
}
