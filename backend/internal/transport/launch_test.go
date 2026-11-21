package transport_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"launchpad/internal/core/integrations"
	"launchpad/internal/jobs"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

type launchTestTokenSource struct{}

func (launchTestTokenSource) AccessToken(context.Context) (string, error) {
	return "fixture-token", nil
}

type launchTestClassroomGateway struct{}

func (launchTestClassroomGateway) EnsureCourse(context.Context, string, integrations.ClassroomCourseInput) (string, error) {
	return "fixture-course", nil
}
func (launchTestClassroomGateway) ActivateCourse(context.Context, string, string) error { return nil }
func (launchTestClassroomGateway) EnrollStudent(context.Context, string, string, string) error {
	return nil
}
func (launchTestClassroomGateway) AssignTeacher(context.Context, string, string, string) error {
	return nil
}

type launchTestDriveGateway struct{ next int }

func (g *launchTestDriveGateway) GenerateFolderID(context.Context, string) (string, error) {
	g.next++
	return "fixture-folder-" + string(rune('0'+g.next)), nil
}
func (*launchTestDriveGateway) EnsureFolder(context.Context, string, string, string, string) error {
	return nil
}
func (*launchTestDriveGateway) GrantAccess(context.Context, string, string, string, string) error {
	return nil
}

type launchTestFormsGateway struct{}

func (launchTestFormsGateway) EnableVerifiedEmail(context.Context, string, string) error { return nil }
func (launchTestFormsGateway) VerifiedEmail(context.Context, string, string) (bool, error) {
	return true, nil
}
func (launchTestFormsGateway) ListResponses(context.Context, string, string, time.Time, string) ([]integrations.IntakeResponse, string, error) {
	return nil, "", nil
}
func (launchTestFormsGateway) Publish(context.Context, string, string) (string, error) {
	return "https://forms.example/fixture", nil
}
func (launchTestFormsGateway) CreateDraft(context.Context, string, string, string) (string, bool, error) {
	return "fixture-form", false, nil
}
func (launchTestFormsGateway) FindDraft(context.Context, string, string) (string, error) {
	return "fixture-form", nil
}
func (launchTestFormsGateway) EnsureQuestions(context.Context, string, string, []integrations.IntakeQuestion) error {
	return nil
}

// TestHTTPLaunchAndRiverWorkerOrchestration covers the API-to-worker path
// against PostgreSQL and River. Every external platform is replaced by a
// local fake gateway; it never calls Slack or Google.
func TestHTTPLaunchAndRiverWorkerOrchestration(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users", "river_job", "launch_results")
	repo := store.New(pool)
	groupService := newGroupsService(pool)

	slackGateway := newFakeSlackState()
	slackGateway.emails["staff@test.example"] = "U3"
	slackGateway.emails["lecturer@test.example"] = "U4"
	slackGateway.emails["student@x.example"] = "U2"
	slackChannels := integrations.NewSlackChannelsService(slackGateway, repo, repo, true)
	classroom := &integrations.ClassroomService{Tokens: launchTestTokenSource{}, Gateway: launchTestClassroomGateway{}, Store: repo, Operations: repo}
	drive := &integrations.DriveService{Tokens: launchTestTokenSource{}, Gateway: &launchTestDriveGateway{}, Store: repo, Operations: repo}
	forms := &integrations.FormsService{Tokens: launchTestTokenSource{}, Gateway: launchTestFormsGateway{}, Store: repo, Operations: repo}

	workers := jobs.NewLaunchWorkers(discardLogger(), slackChannels, true, classroom, drive, forms, groupService, []string{"fallback@x.example"}, repo, repo, repo)
	client, err := jobs.NewClient(pool, workers, jobs.Config{})
	if err != nil {
		t.Fatalf("River client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("start River: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := client.StopAndCancel(stopCtx); err != nil {
			t.Errorf("stop River: %v", err)
		}
	})

	launcher := &jobs.RiverLauncher{Client: client, ResultStore: repo}
	app := transport.New(testConfig(), discardLogger(), groupService, googleServiceForTest(pool),
		integrations.NewSlackAuthService(repo, slackGateway, true), slackChannels, slackEventsServiceForTest(pool), nil, pool.Ping,
		transport.GroupProvisioning{GroupLauncher: launcher, LaunchStatus: repo, Classroom: classroom, Drive: drive, Forms: forms})
	srv := httptestNew(app, t)

	code, group := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"HTTP E2E Group","courseName":"HTTP E2E Course","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"],"studentEmails":["student@x.example"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, group)
	}
	groupID, _ := group["id"].(string)
	if groupID == "" {
		t.Fatalf("group response has no id: %v", group)
	}
	launchGroupID := uuid.MustParse(groupID)
	path := "/api/groups/" + groupID
	code, queued := doJSON(t, srv, http.MethodPost, path+"/launch", "")
	if code != http.StatusAccepted || queued["status"] != "queued" {
		t.Fatalf("launch response: %d %v", code, queued)
	}

	deadline := time.Now().Add(20 * time.Second)
	var status map[string]any
	for time.Now().Before(deadline) {
		code, status = doJSON(t, srv, http.MethodGet, path+"/status", "")
		if code != http.StatusOK {
			t.Fatalf("read launch status: %d %v", code, status)
		}
		if status["status"] == "completed" || status["status"] == "partially_failed" || status["status"] == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status["status"] != "completed" {
		t.Fatalf("unified launch did not complete successfully: %v", status)
	}
	steps, ok := status["steps"].([]any)
	if !ok || len(steps) != 4 {
		t.Fatalf("launch steps = %T %v, want four platform results", status["steps"], status["steps"])
	}
	for _, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok || step["status"] != "succeeded" {
			t.Errorf("platform step did not succeed: %v", raw)
		}
	}
	if got := len(slackGateway.posts); got != len(integrations.DefaultChannelSuffixes) {
		t.Errorf("Slack welcome messages = %d, want %d", got, len(integrations.DefaultChannelSuffixes))
	}
	if got := len(slackGateway.members); got != len(integrations.DefaultChannelSuffixes)*3 {
		t.Errorf("Slack invitations = %d, want per-group staff + lecturer + student in each channel (%d)", got, len(integrations.DefaultChannelSuffixes)*3)
	}
	// The notification is written right after the persisted result, so poll
	// briefly instead of asserting on the exact instant the status flips.
	var notificationCount int
	notificationDeadline := time.Now().Add(5 * time.Second)
	for {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE group_id = $1 AND kind = 'launch_completed'`, groupID).Scan(&notificationCount); err != nil {
			t.Fatal("read launch notifications:", err)
		}
		if notificationCount == 1 || time.Now().After(notificationDeadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if notificationCount != 1 {
		t.Errorf("launch notifications = %d, want 1", notificationCount)
	}
	// River retries must not duplicate the notification: re-inserting with
	// the same dedupe key is a no-op.
	retryKey := "group_launch:retry-probe"
	if err := repo.InsertNotificationOnce(ctx, &launchGroupID, "launch_completed", "info", "first", "", retryKey); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertNotificationOnce(ctx, &launchGroupID, "launch_completed", "info", "second", "", retryKey); err != nil {
		t.Fatal(err)
	}
	var probeCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE dedupe_key = $1`, retryKey).Scan(&probeCount); err != nil {
		t.Fatal(err)
	}
	if probeCount != 1 {
		t.Errorf("deduped notification rows = %d, want exactly 1", probeCount)
	}
}

func TestSlackLaunchQueuesAndProvisionsOnce(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users", "river_job")
	repo := store.New(pool)
	workspace := newFakeSlackState()
	slack := integrations.NewSlackChannelsService(workspace, repo, repo, true)
	producer, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	launch := &jobs.SlackLaunchService{Queue: repo, River: producer}
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slack, slackEventsServiceForTest(pool), nil, pool.Ping, transport.GroupProvisioning{Launch: launch})
	srv := httptestNew(app, t)

	code, group := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Queued Slack Group","courseName":"Go","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"],"studentEmails":["staff@x.example"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, group)
	}
	path := "/api/groups/" + group["id"].(string)
	if code, _ := doJSON(t, srv, http.MethodGet, path+"/status", ""); code != http.StatusNotFound {
		t.Fatalf("status before launch: %d", code)
	}
	code, first := doJSON(t, srv, http.MethodPost, path+"/launch", "")
	if code != http.StatusAccepted || first["status"] != "queued" {
		t.Fatalf("queue launch: %d %v", code, first)
	}
	code, second := doJSON(t, srv, http.MethodPost, path+"/launch", "")
	if code != http.StatusAccepted {
		t.Fatalf("duplicate launch: %d %v", code, second)
	}
	if total := countRowsSimple(t, pool, "SELECT count(*) FROM river_job WHERE kind='slack_launch'"); total != 1 {
		t.Fatalf("queued %d jobs, want 1", total)
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &jobs.SlackLaunchWorker{Store: repo, Slack: slack})
	worker, err := jobs.NewClient(pool, workers, jobs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := worker.StopAndCancel(ctx); err != nil {
			t.Errorf("stop worker: %v", err)
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		code, current := doJSON(t, srv, http.MethodGet, path+"/status", "")
		if code != http.StatusOK {
			t.Fatalf("read launch status: %d %v", code, current)
		}
		if current["status"] == "succeeded" {
			if current["completedAt"] == nil || current["attempts"] != float64(1) {
				t.Fatalf("unexpected launch result: %v", current)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("launch did not succeed: %v", current)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if channels := countRowsSimple(t, pool, "SELECT count(*) FROM group_resources WHERE resource_type='channel'"); channels != 4 {
		t.Fatalf("created %d Slack channels, want 4", channels)
	}
}
