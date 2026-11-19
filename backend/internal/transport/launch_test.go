package transport_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"launchpad/internal/core/integrations"
	"launchpad/internal/jobs"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

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
		slackServiceForTest(pool), slack, slackEventsServiceForTest(pool), pool.Ping, transport.GroupProvisioning{Launch: launch})
	srv := httptestNew(app, t)

	code, group := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Queued Slack Group","courseName":"Go","startDate":"2026-12-01","studentEmails":["staff@x.example"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, group)
	}
	path := "/api/groups/" + group["id"].(string)
	if code, _ := doJSON(t, srv, http.MethodGet, path+"/status", ""); code != http.StatusNotFound {
		t.Fatalf("status before launch: %d", code)
	}
	code, first := doJSON(t, srv, http.MethodPost, path+"/launch", "")
	if code != http.StatusAccepted || first["status"] != "pending" || first["id"] == nil {
		t.Fatalf("queue launch: %d %v", code, first)
	}
	code, second := doJSON(t, srv, http.MethodPost, path+"/launch", "")
	if code != http.StatusAccepted || second["id"] != first["id"] {
		t.Fatalf("duplicate launch: %d %v, want run %v", code, second, first["id"])
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
