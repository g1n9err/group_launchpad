package transport_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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

type fixtureAuditMailer struct {
	messages chan string
}

func (f fixtureAuditMailer) SendMessage(_ context.Context, recipient, subject, body string) (bool, error) {
	f.messages <- recipient + "\n" + subject + "\n" + body
	return false, nil
}

// fixtureMemberChecker is a deterministic audit.MemberChecker: it reports the
// configured status per role and proves expected members are passed through.
type fixtureMemberChecker struct {
	statusByRole map[string]string
	seenRoles    []string
}

func (f *fixtureMemberChecker) CheckMembers(_ context.Context, channels []audit.Resource, expected []audit.ExpectedMember) []audit.Result {
	seen := map[string]bool{}
	var out []audit.Result
	for _, m := range expected {
		if seen[m.Role] {
			continue
		}
		seen[m.Role] = true
		f.seenRoles = append(f.seenRoles, m.Role)
		status := f.statusByRole[m.Role]
		if status == "" {
			status = "unavailable"
		}
		out = append(out, audit.Result{
			Resource: audit.Resource{Provider: "slack", Kind: "membership", Key: m.Role},
			Status:   status,
		})
	}
	return out
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
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), nil, pool.Ping,
		transport.GroupProvisioning{Audit: audits, AuditNotifications: repo})
	srv := httptestNew(app, t)
	code, group := doJSON(t, srv, http.MethodPost, "/api/groups", `{"name":"Audit Test","courseName":"Go","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, group)
	}
	code, _ = doJSON(t, srv, http.MethodPost, "/api/groups", `{"name":"Unused Draft","courseName":"Go","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"]}`)
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
	members := &fixtureMemberChecker{statusByRole: map[string]string{"teaching_staff": "ok", "lecturer": "missing_or_inaccessible"}}
	river.AddWorker(workers, &jobs.GroupAuditWorker{Audit: &audit.Service{Store: repo,
		Slack:   fixtureAuditChecker{"archived-channel": "archived"},
		Google:  fixtureAuditChecker{"classroom-course": "inactive", "root-folder": "ok", "child": "ok"},
		Members: members}, EmailEnabled: true})
	river.AddWorker(workers, &jobs.DailyAuditWorker{Groups: repo})
	mailer := fixtureAuditMailer{messages: make(chan string, 2)}
	river.AddWorker(workers, &jobs.AuditEmailWorker{Store: repo, Mailer: mailer, Recipient: "coordinator@example.test"})
	worker, err := jobs.NewClient(pool, workers, jobs.Config{AuditEmailEnabled: true})
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
				if len(run.Results) != 15 || len(run.Issues) != 12 || status["course"] != "inactive" ||
					status["intake"] != "unavailable" || status["audit-test-general"] != "archived" ||
					status["root/General"] != "ok" || status["root/Student Data"] != "unlinked" ||
					status["teaching_staff"] != "ok" || status["lecturer"] != "missing_or_inaccessible" ||
					status["students"] != "unlinked" {
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
	select {
	case message := <-mailer.messages:
		if !strings.HasPrefix(message, "coordinator@example.test\nGroup Launchpad resource audit\n") ||
			!strings.Contains(message, "slack / audit-test-general: archived") {
			t.Fatalf("unexpected audit email: %q", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("audit email did not run")
	}
	for {
		code, result := doJSON(t, srv, http.MethodGet, "/api/audit-notifications", "")
		if code != http.StatusOK || len(result["openIssues"].([]any)) != 1 || len(result["notifications"].([]any)) != 1 {
			t.Fatalf("audit notifications: %d %v", code, result)
		}
		if result["notifications"].([]any)[0].(map[string]any)["emailStatus"] == "sent" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("audit email outcome was not saved")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if count := countRowsSimple(t, pool, "SELECT count(*) FROM audit_logs"); count != 2 {
		t.Fatalf("checked %d snapshots; the unused draft should be skipped", count)
	}
	for _, search := range []struct {
		query string
		want  int
	}{
		{query: "%20DiT%20Te%20", want: 1},
		{query: "missing", want: 0},
		{query: "%25", want: 0},
	} {
		code, result := doJSON(t, srv, http.MethodGet, "/api/audit-notifications?groupName="+search.query, "")
		if code != http.StatusOK || len(result["openIssues"].([]any)) != search.want || len(result["notifications"].([]any)) != search.want {
			t.Errorf("audit notifications search %q: %d %v, want %d matches in each list", search.query, code, result, search.want)
		}
	}
	if count := countRowsSimple(t, pool, "SELECT count(*) FROM group_resources"); count != 4 {
		t.Fatalf("audit changed the resource registry: got %d records", count)
	}
}

func TestAuditNotificationsTrackChangesAndResolutions(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "audit_logs")
	repo := store.New(pool)
	group, err := repo.Create(t.Context(), groupsCreateParams("Audit delta", nil))
	if err != nil {
		t.Fatal(err)
	}
	resource := audit.Resource{Provider: "slack", Kind: "channel", Key: "general"}
	for i, status := range []string{"ok", "unavailable", "unavailable", "archived", "ok"} {
		item := audit.Result{Resource: resource, Status: status}
		run := audit.Snapshot{GroupID: group.ID, Type: "manual", Results: []audit.Result{item}, Issues: []audit.Result{}}
		if status != "ok" {
			run.Issues = append(run.Issues, item)
		}
		for range 2 {
			if _, err := repo.SaveAudit(t.Context(), int64(i+1), run, []audit.Resource{resource}, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	open, err := repo.CurrentAuditIssues(t.Context(), "")
	if err != nil || len(open) != 0 {
		t.Fatalf("open issues=%v err=%v", open, err)
	}
	rows, err := repo.AuditNotifications(t.Context(), "")
	if err != nil || len(rows) != 3 {
		t.Fatalf("notifications=%v err=%v; want new, changed, resolved", rows, err)
	}
	for i, expected := range [][2]string{{"archived", "ok"}, {"unavailable", "archived"}, {"ok", "unavailable"}} {
		if len(rows[i].Changes) != 1 || rows[i].Changes[0].Before != expected[0] || rows[i].Changes[0].After != expected[1] ||
			rows[i].EmailStatus != "disabled" {
			t.Fatalf("unexpected notification: %+v", rows[i])
		}
	}
	var persistentCount int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM notifications
		WHERE group_id=$1 AND kind='audit_inconsistency'`, group.ID).Scan(&persistentCount); err != nil {
		t.Fatal(err)
	}
	if persistentCount != 3 {
		t.Fatalf("persistent audit notifications=%d, want 3 changed snapshots", persistentCount)
	}
}
