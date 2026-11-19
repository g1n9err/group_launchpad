package google

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"launchpad/internal/core/integrations"
)

func TestClassroomEnsureCourseRecoversExistingAlias(t *testing.T) {
	const alias = "p:launchpad-test"
	var creates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-access-token" {
			t.Error("request missing bearer token")
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/courses":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if body["id"] != alias || body["name"] != "Go" || body["ownerId"] != "me" || body["courseState"] != "PROVISIONED" {
				t.Errorf("unexpected course payload: %v", body)
			}
			if creates.Add(1) > 1 {
				w.WriteHeader(http.StatusConflict)
				return
			}
		case r.Method == http.MethodGet && r.URL.Path == "/courses/"+alias:
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"id":"classroom-123"}`))
	}))
	defer server.Close()

	gateway := &ClassroomGateway{coursesURL: server.URL + "/courses", client: server.Client()}
	for range 2 {
		id, err := gateway.EnsureCourse(t.Context(), "test-access-token", integrations.ClassroomCourseInput{Alias: alias, Name: "Go"})
		if err != nil || id != "classroom-123" {
			t.Fatalf("ensure course: id=%q err=%v", id, err)
		}
	}
}
