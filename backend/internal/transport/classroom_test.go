package transport_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"launchpad/internal/core/integrations"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

type fakeClassroom struct {
	requests atomic.Int32
}

func (*fakeClassroom) AccessToken(context.Context) (string, error) {
	return "classroom-test-token", nil
}

func (f *fakeClassroom) ActivateCourse(_ context.Context, _ string, _ string) error { return nil }
func (f *fakeClassroom) EnsureCourse(_ context.Context, _ string, _ integrations.ClassroomCourseInput) (string, error) {
	f.requests.Add(1)
	return "classroom-123", nil
}

func (*fakeClassroom) EnrollStudent(context.Context, string, string, string) error {
	return nil // transport tests don't assert enrollment
}

func (*fakeClassroom) AssignTeacher(context.Context, string, string, string) error {
	return nil // transport tests don't assert teacher assignment
}
func (*fakeClassroom) RemoveStudent(context.Context, string, string, string) error { return nil }
func (*fakeClassroom) RemoveTeacher(context.Context, string, string, string) error { return nil }

func TestClassroomProvisionReusesPersistedCourse(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups")
	repo := store.New(pool)
	fake := &fakeClassroom{}
	service := &integrations.ClassroomService{Tokens: fake, Gateway: fake, Store: repo, Operations: repo}
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), nil, pool.Ping,
		transport.GroupProvisioning{Classroom: service})
	srv := httptestNew(app, t)

	code, group := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Classroom Provision","courseName":"Go","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, group)
	}
	path := "/api/groups/" + group["id"].(string) + "/classroom/provision"
	for range 2 {
		code, response := doJSON(t, srv, http.MethodPost, path, "")
		if code != http.StatusOK || response["courseId"] != "classroom-123" {
			t.Fatalf("provision classroom: %d %v", code, response)
		}
	}
	if calls := fake.requests.Load(); calls != 1 {
		t.Fatalf("ensure course got %d calls, want 1", calls)
	}
	if rows := countRowsSimple(t, pool, "SELECT count(*) FROM group_resources WHERE provider='google_classroom' AND external_resource_id='classroom-123'"); rows != 1 {
		t.Fatalf("saved %d course resources, want 1", rows)
	}
}
