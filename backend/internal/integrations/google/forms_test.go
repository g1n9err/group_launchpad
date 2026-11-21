package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"launchpad/internal/core/integrations"
)

func TestGoogleFormsCreatesUnpublishedDraftAndResumesQuestions(t *testing.T) {
	var saved []formQuestionItem
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/forms":
			if r.URL.Query().Get("unpublished") != "true" {
				t.Error("form must start unpublished")
			}
			_, _ = w.Write([]byte(`{"formId":"test-form"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/forms/test-form":
			_ = json.NewEncoder(w).Encode(googleForm{Revision: "draft-rev", Items: saved})
		case r.Method == http.MethodPost && r.URL.Path == "/forms/test-form:batchUpdate":
			var batch struct {
				WriteControl struct{ RequiredRevisionID string }
				Requests     []struct {
					CreateItem struct{ Item formQuestionItem }
				}
			}
			if err := json.NewDecoder(r.Body).Decode(&batch); err != nil || batch.WriteControl.RequiredRevisionID != "draft-rev" || len(batch.Requests) != 2 {
				t.Errorf("invalid batch: %+v (%v)", batch, err)
			}
			if saved != nil {
				t.Error("duplicate batch update")
			}
			for _, request := range batch.Requests {
				saved = append(saved, request.CreateItem.Item)
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	gateway := &FormsGateway{formsURL: server.URL + "/forms", client: server.Client()}
	id, rejected, err := gateway.CreateDraft(t.Context(), "token", "Student intake", "test draft")
	if err != nil || rejected || id != "test-form" {
		t.Fatalf("create draft: %q %t %v", id, rejected, err)
	}
	questions := []integrations.IntakeQuestion{
		{Title: "Notes", Type: "paragraph"},
		{Title: "Time", Type: "single_choice", Options: []string{"Morning", "Evening"}},
	}
	for range 2 {
		if err := gateway.EnsureQuestions(t.Context(), "token", id, questions); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGoogleFormsPublishReturnsVerifiedResponderLink(t *testing.T) {
	const link = "https://docs.google.com/forms/d/e/test-form/viewform"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing bearer token")
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/forms/test-form:setPublishSettings":
			var body struct {
				UpdateMask      string
				PublishSettings struct {
					PublishState map[string]bool
				}
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
				body.UpdateMask != "publishState" ||
				!body.PublishSettings.PublishState["isPublished"] ||
				!body.PublishSettings.PublishState["isAcceptingResponses"] {
				t.Errorf("invalid publish request: %+v (%v)", body, err)
			}
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/forms/test-form":
			_, _ = w.Write([]byte(`{"formId":"test-form","responderUri":"` + link + `","publishSettings":{"publishState":{"isPublished":true,"isAcceptingResponses":true}}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	gateway := &FormsGateway{formsURL: server.URL + "/forms", client: server.Client()}
	got, err := gateway.Publish(t.Context(), "test-token", "test-form")
	if err != nil || got != link {
		t.Fatalf("publish: %q %v", got, err)
	}
}

// EnsureQuestions appends only the missing tail to an existing draft; edits
// or reorders of existing items are rejected.
func TestEnsureQuestionsAppendsMissingTail(t *testing.T) {
	var batchCalls int
	var created []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && !strings.Contains(r.URL.Path, ":batchUpdate"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"revisionId":"r1","items":[` +
				`{"title":"Full name","questionItem":{"question":{"required":true,"textQuestion":{}}}},` +
				`{"title":"Email address","questionItem":{"question":{"required":true,"textQuestion":{}}}}]}`))
		case strings.Contains(r.URL.Path, ":batchUpdate"):
			batchCalls++
			var body struct {
				Requests []map[string]any `json:"requests"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			created = body.Requests
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	gw := &FormsGateway{formsURL: srv.URL, client: srv.Client()}

	err := gw.EnsureQuestions(context.Background(), "tok", "form1", []integrations.IntakeQuestion{
		{Title: "Full name", Type: "text", Required: true},
		{Title: "Email address", Type: "text", Required: true},
		{Title: "Phone", Type: "text"},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if batchCalls != 1 || len(created) != 1 {
		t.Fatalf("batchUpdate calls=%d requests=%d, want 1 new item", batchCalls, len(created))
	}
	item := created[0]["createItem"].(map[string]any)
	if item["location"].(map[string]any)["index"].(float64) != 2 {
		t.Errorf("new item index = %v, want 2 (after the existing pair)", item["location"])
	}
}

// Սևագիրը ազատ վերակառուցվում է․ տարբերվող դասավորության դեպքում բոլոր
// առկա տարրերը ջնջվում են և նոր շարքն է ստեղծվում՝ մեկ batchUpdate-ում։
func TestEnsureQuestionsRebuildsDraftFromScratch(t *testing.T) {
	var body struct {
		Requests []map[string]any `json:"requests"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, ":batchUpdate") {
			_ = json.NewDecoder(r.Body).Decode(&body)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"revisionId":"r1","items":[{"title":"Old question","questionItem":{"question":{"required":true,"textQuestion":{}}}}]}`))
	}))
	defer srv.Close()
	gw := &FormsGateway{formsURL: srv.URL, client: srv.Client()}
	err := gw.EnsureQuestions(context.Background(), "tok", "form1", []integrations.IntakeQuestion{
		{Title: "Full name", Type: "text", Required: true},
		{Title: "Email address", Type: "text", Required: true},
		{Title: "City", Type: "text"},
	})
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	deletes := 0
	creates := 0
	for _, request := range body.Requests {
		if _, ok := request["deleteItem"]; ok {
			deletes++
		}
		if _, ok := request["createItem"]; ok {
			creates++
		}
	}
	if deletes != 1 || creates != 3 {
		t.Fatalf("requests: %d deletes, %d creates; want 1/3 (delete old, create new set)", deletes, creates)
	}
}
