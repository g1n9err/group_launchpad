package transport_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"launchpad/internal/core/audit"
	"launchpad/internal/jobs"
	"launchpad/internal/store"
	"launchpad/internal/transport"
	gen "launchpad/internal/transport/gen"
)

type fixtureAuditChecker map[string]string

func (f fixtureAuditChecker) CheckResource(_ context.Context, resource audit.Resource) string {
	if resource.ID == "child" && resource.ParentID != "root-folder" {
		return "misconfigured"
	}
	if status := f[resource.ID]; status != "" {
		return status
	}
	return "unavailable"
}

func TestGroupAuditQueuesManualAndScheduledReadOnlySnapshots(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "river_job")
	repo := store.New(pool)
	producer, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	audits := &jobs.AuditService{Store: repo, River: producer}
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), pool.Ping,
		transport.GroupProvisioning{Audit: audits})
	srv := httptestNew(app, t)
	code, group := doJSON(t, srv, http.MethodPost, "/api/groups", `{"name":"Audit Test","courseName":"Go","startDate":"2026-12-01"}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, group)
	}
	code, _ = doJSON(t, srv, http.MethodPost, "/api/groups", `{"name":"Unused Draft","courseName":"Go","startDate":"2026-12-01"}`)
	if code != http.StatusCreated {
		t.Fatalf("create draft: %d", code)
	}
	id := group["id"].(string)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO group_resources (group_id,provider,resource_type,resource_name,external_resource_id)
		VALUES ($1,'slack','channel','audit-test-general','archived-channel'),
		       ($1,'google_classroom','course','test-alias','classroom-course'),
		       ($1,'google_drive','folder','root','root-folder'),
		       ($1,'google_drive','folder','root/General','child')
	`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO intake_forms (group_id,external_form_id,status) VALUES ($1,'unverified-form','created')
	`, id); err != nil {
		t.Fatal(err)
	}
	route := "/api/groups/" + id + "/audits"
	code, first := doJSON(t, srv, http.MethodPost, route, "")
	if code != http.StatusAccepted || first["jobId"] == nil {
		t.Fatalf("queue manual: %d %v", code, first)
	}
	code, second := doJSON(t, srv, http.MethodPost, route, "")
	if code != http.StatusAccepted || second["jobId"] != first["jobId"] {
		t.Fatalf("queue repeated manual: %d %v", code, second)
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &jobs.GroupAuditWorker{Audit: &audit.Service{Store: repo,
		Slack:  fixtureAuditChecker{"archived-channel": "archived"},
		Google: fixtureAuditChecker{"classroom-course": "inactive", "root-folder": "ok", "child": "ok"}}})
	river.AddWorker(workers, &jobs.DailyAuditWorker{Groups: repo})
	worker, err := jobs.NewClient(pool, workers, jobs.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.PeriodicJobs().AddSafely(jobs.DailyAuditSchedule()); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := worker.StopAndCancel(ctx); err != nil {
			t.Error(err)
		}
	})
	getAudits := func() []gen.GroupAudit {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+route, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("coordinator", testCoordinatorPassword)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var history []gen.GroupAudit
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("audit list got status %d", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(&history); err != nil {
			t.Fatal(err)
		}
		return history
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		history := getAudits()
		if len(history) == 2 {
			types := map[gen.GroupAuditType]bool{}
			for _, run := range history {
				types[run.Type] = true
				status := map[string]gen.GroupAuditResourceStatus{}
				for _, result := range run.Results {
					status[result.Key] = result.Status
				}
				if len(run.Results) != 12 || len(run.Issues) != 10 || status["course"] != "inactive" ||
					status["intake"] != "unavailable" || status["audit-test-general"] != "archived" ||
					status["root/General"] != "ok" || status["root/Student Data"] != "unlinked" {
					t.Fatalf("unexpected audit results: %+v", run)
				}
			}
			if !types["manual"] || !types["daily_sync"] {
				t.Fatalf("missing manual or daily audit: %v", types)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected manual and daily snapshots, got %+v", history)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if count := countRowsSimple(t, pool, "SELECT count(*) FROM audit_logs"); count != 2 {
		t.Fatalf("checked %d snapshots; the unused draft should be skipped", count)
	}
	if count := countRowsSimple(t, pool, "SELECT count(*) FROM group_resources"); count != 4 {
		t.Fatalf("audit changed the resource registry: got %d records", count)
	}
}
