package transport_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"launchpad/internal/jobs"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

type fixtureAnnouncementPublisher struct {
	mu    sync.Mutex
	times map[string][]time.Time
}

func (f *fixtureAnnouncementPublisher) PostAnnouncement(_ context.Context, channel, message string) (string, bool, error) {
	if channel != "C-scheduled-test" {
		return "", false, fmt.Errorf("unexpected test channel")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.times[message] = append(f.times[message], time.Now())
	if message == "Ambiguous post" {
		return "", true, errors.New("Slack did not confirm publication")
	}
	return "1700000000.000001", false, nil
}

func TestScheduledAnnouncementsDelayAndDeduplicateSlackPosts(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "river_job")
	repo := store.New(pool)
	producer, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), pool.Ping,
		transport.GroupProvisioning{Announcements: &jobs.AnnouncementService{Store: repo, River: producer}})
	srv := httptestNew(app, t)
	code, group := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Announcements Group","courseName":"Go","startDate":"2026-12-01"}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, group)
	}
	id := group["id"].(string)
	route := "/api/groups/" + id + "/announcements"
	when := time.Now().UTC().Truncate(time.Microsecond).Add(3 * time.Second)
	requestID := uuid.New()
	body := func(key uuid.UUID, message string) string {
		return fmt.Sprintf(`{"requestId":%q,"channelName":"announcements-group-announcements","type":"reminder","message":%q,"scheduledAt":%q}`,
			key, message, when.Format(time.RFC3339Nano))
	}

	if code, _ := doJSON(t, srv, http.MethodPost, route, body(requestID, "Test scheduled announcement")); code != http.StatusConflict {
		t.Fatalf("unlinked channel got %d, want 409", code)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO group_resources (group_id,provider,resource_type,resource_name,external_resource_id)
		VALUES ($1,'slack','channel','announcements-group-announcements','C-scheduled-test')`, id); err != nil {
		t.Fatal(err)
	}
	code, first := doJSON(t, srv, http.MethodPost, route, body(requestID, "Test scheduled announcement"))
	if code != http.StatusAccepted || first["status"] != "scheduled" {
		t.Fatalf("schedule: %d %v", code, first)
	}
	code, repeated := doJSON(t, srv, http.MethodPost, route, body(requestID, "Test scheduled announcement"))
	if code != http.StatusAccepted || repeated["id"] != first["id"] {
		t.Fatalf("schedule retry: %d %v", code, repeated)
	}
	if code, _ := doJSON(t, srv, http.MethodPost, route, body(requestID, "Different text")); code != http.StatusConflict {
		t.Fatalf("conflicting request ID got %d, want 409", code)
	}
	code, ambiguous := doJSON(t, srv, http.MethodPost, route, body(uuid.New(), "Ambiguous post"))
	if code != http.StatusAccepted {
		t.Fatalf("schedule ambiguous test: %d %v", code, ambiguous)
	}
	if jobs := countRowsSimple(t, pool, "SELECT count(*) FROM river_job WHERE queue='slack_announcements'"); jobs != 2 {
		t.Fatalf("queued %d announcements, want 2", jobs)
	}

	publisher := &fixtureAnnouncementPublisher{times: make(map[string][]time.Time)}
	workers := river.NewWorkers()
	river.AddWorker(workers, &jobs.AnnouncementWorker{Store: repo, Slack: publisher})
	worker, err := jobs.NewClient(pool, workers, jobs.Config{SlackAnnouncementsEnabled: true})
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
			t.Error(err)
		}
	})

	for _, row := range []map[string]any{first, ambiguous} {
		announcementID, err := uuid.Parse(row["id"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := producer.Insert(t.Context(), jobs.AnnouncementArgs{ID: announcementID},
			&river.InsertOpts{Queue: jobs.SlackAnnouncementsQueue, ScheduledAt: when.Add(time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(12 * time.Second)
	for {
		var completed int
		err := pool.QueryRow(t.Context(), `SELECT count(*) FROM river_job WHERE queue='slack_announcements' AND state='completed'`).Scan(&completed)
		if err != nil {
			t.Fatal(err)
		}
		if completed == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d job executions finished", completed)
		}
		time.Sleep(100 * time.Millisecond)
	}
	items, err := repo.Announcements(t.Context(), mustUUID(t, id))
	if err != nil || len(items) != 2 {
		t.Fatalf("list announcements: %v %v", items, err)
	}
	for _, item := range items {
		switch item.Message {
		case "Test scheduled announcement":
			if item.Status != "published" || item.ExternalMessageID != "1700000000.000001" ||
				item.PublishedAt == nil || item.PublishedAt.Before(when) {
				t.Fatalf("published announcement: %+v", item)
			}
		case "Ambiguous post":
			if item.Status != "unknown" || item.Error == "" {
				t.Fatalf("ambiguous announcement: %+v", item)
			}
		}
	}
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	for _, message := range []string{"Test scheduled announcement", "Ambiguous post"} {
		calls := publisher.times[message]
		if len(calls) != 1 || calls[0].Before(when) {
			t.Fatalf("message %q posted at %v, want one post at or after %v", message, calls, when)
		}
	}
}
