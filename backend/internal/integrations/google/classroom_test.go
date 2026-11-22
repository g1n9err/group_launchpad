package google

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestActivateCoursePatchesStateToActive(t *testing.T) {
	var gotMethod, gotPath, gotBody, gotMask string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotMask = r.Method, r.URL.Path, r.URL.Query().Get("updateMask")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"course-1","courseState":"ACTIVE"}`))
	}))
	defer srv.Close()

	gw := &ClassroomGateway{coursesURL: srv.URL + "/v1/courses", client: srv.Client()}
	if err := gw.ActivateCourse(t.Context(), "tok", "course-1"); err != nil {
		t.Fatalf("ActivateCourse: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/v1/courses/course-1" {
		t.Errorf("request = %s %s, want PATCH /v1/courses/course-1", gotMethod, gotPath)
	}
	if gotMask != "courseState" {
		t.Errorf("updateMask query = %q, want courseState", gotMask)
	}
	if !strings.Contains(gotBody, `"courseState":"ACTIVE"`) {
		t.Errorf("body = %s, want courseState=ACTIVE", gotBody)
	}
}

func TestActivateCourseSurfacesHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	gw := &ClassroomGateway{coursesURL: srv.URL + "/v1/courses", client: srv.Client()}
	err := gw.ActivateCourse(t.Context(), "tok", "course-1")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want HTTP 403 surfaced", err)
	}
}

func TestRemoveTeacherCancelsPendingInvitationAndRemovesCourseMember(t *testing.T) {
	var invitationDeleted, memberDeleted atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing token")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/invitations":
			if r.URL.Query().Get("courseId") != "course-1" || r.URL.Query().Get("userId") != "teacher@example.test" {
				t.Errorf("unexpected invitation query: %v", r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"invitations":[{"id":"teacher-invite","userId":"teacher@example.test","courseId":"course-1","role":"TEACHER"},{"id":"student-invite","userId":"teacher@example.test","courseId":"course-1","role":"STUDENT"}]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/invitations/teacher-invite":
			invitationDeleted.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/courses/course-1/teachers/teacher@example.test":
			memberDeleted.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	gw := &ClassroomGateway{coursesURL: server.URL + "/v1/courses", client: server.Client()}
	if err := gw.RemoveTeacher(t.Context(), "token", "course-1", "teacher@example.test"); err != nil {
		t.Fatal(err)
	}
	if invitationDeleted.Load() != 1 || memberDeleted.Load() != 1 {
		t.Fatalf("deleted invitation/member = %d/%d, want 1/1", invitationDeleted.Load(), memberDeleted.Load())
	}
}

func TestClassroomRosterUsesInvitationsWhenDirectAddIsForbidden(t *testing.T) {
	for _, tc := range []struct {
		name, roster, role             string
		direct, invitation, membership int
		wantError                      bool
	}{
		{name: "student invite", roster: "students", role: "STUDENT", direct: 403, invitation: 200},
		{name: "student invite retry", roster: "students", role: "STUDENT", direct: 403, invitation: 409},
		{name: "student accepted invitation", roster: "students", role: "STUDENT", direct: 403, invitation: 400, membership: 200},
		{name: "student precondition rejected", roster: "students", role: "STUDENT", direct: 403, invitation: 400, membership: 404, wantError: true},
		{name: "teacher invite", roster: "teachers", role: "TEACHER", direct: 403, invitation: 200},
		{name: "admin adds directly", roster: "students", role: "STUDENT", direct: 200},
		{name: "already on roster", roster: "students", role: "STUDENT", direct: 409},
		{name: "Google rejects invitation", roster: "students", role: "STUDENT", direct: 403, invitation: 403, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const course = "course-1"
			const email = "person@example.test"
			var directCalls, inviteCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/v1/courses/"+course+"/"+tc.roster+"/"+email && tc.membership != 0 {
					w.WriteHeader(tc.membership)
					return
				}
				var payload map[string]string
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer token" ||
					json.NewDecoder(r.Body).Decode(&payload) != nil || payload["userId"] != email {
					t.Errorf("unexpected Classroom request: %s %s %v", r.Method, r.URL, payload)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				switch r.URL.Path {
				case "/v1/courses/" + course + "/" + tc.roster:
					directCalls.Add(1)
					if r.URL.RawQuery != "" || len(payload) != 1 {
						t.Errorf("unexpected direct-add query or payload: %s %v", r.URL.RawQuery, payload)
					}
					w.WriteHeader(tc.direct)
				case "/v1/invitations":
					inviteCalls.Add(1)
					if payload["courseId"] != course || payload["role"] != tc.role {
						t.Errorf("unexpected invitation payload: %v", payload)
					}
					if tc.invitation == 0 {
						t.Error("unexpected invitation request")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					w.WriteHeader(tc.invitation)
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			gateway := &ClassroomGateway{coursesURL: server.URL + "/v1/courses", client: server.Client()}
			add := gateway.EnrollStudent
			if tc.role == "TEACHER" {
				add = gateway.AssignTeacher
			}
			err := add(t.Context(), "token", course, email)
			if (err != nil) != tc.wantError {
				t.Fatalf("add = %v, want error %t", err, tc.wantError)
			}
			wantInvites := int32(0)
			if tc.invitation != 0 {
				wantInvites = 1
			}
			if directCalls.Load() != 1 || inviteCalls.Load() != wantInvites {
				t.Errorf("direct calls = %d, invite calls = %d, want 1 and %d", directCalls.Load(), inviteCalls.Load(), wantInvites)
			}
		})
	}
}

func TestClassroomJoinCodeRequiresActiveCourse(t *testing.T) {
	for _, state := range []string{"ACTIVE", "PROVISIONED"} {
		t.Run(state, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/courses/class-1" ||
					r.URL.Query().Get("fields") != "courseState,enrollmentCode" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"courseState": state, "enrollmentCode": "testcode"})
			}))
			defer server.Close()

			gateway := &ClassroomGateway{coursesURL: server.URL + "/v1/courses", client: server.Client()}
			code, err := gateway.JoinCode(t.Context(), "token", "class-1")
			if state == "ACTIVE" {
				if err != nil || code != "testcode" {
					t.Fatalf("JoinCode = %q, %v; want testcode", code, err)
				}
			} else if err == nil || code != "" {
				t.Fatalf("JoinCode = %q, %v; want an error for a draft course", code, err)
			}
		})
	}
}
