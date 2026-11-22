package transport_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
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

type failingTemplateStore struct{ err error }

func (s failingTemplateStore) CreateTemplate(context.Context, string, string, string) (transport.TemplateRow, error) {
	return transport.TemplateRow{}, s.err
}
func (s failingTemplateStore) ListTemplates(context.Context) ([]transport.TemplateRow, error) {
	return nil, s.err
}
func (s failingTemplateStore) GetTemplate(context.Context, uuid.UUID) (transport.TemplateRow, error) {
	return transport.TemplateRow{}, s.err
}
func (s failingTemplateStore) UpdateTemplate(context.Context, uuid.UUID, string, string, string) error {
	return s.err
}
func (s failingTemplateStore) DeleteTemplate(context.Context, uuid.UUID) (bool, error) {
	return false, s.err
}

func TestScheduleAnnouncementDistinguishesMissingTemplateFromStoreFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "missing template", err: transport.ErrTemplateNotFound, want: http.StatusNotFound},
		{name: "store failure", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			truncate(t, pool, "groups", "river_job")
			repo := store.New(pool)
			producer, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
			if err != nil {
				t.Fatal(err)
			}
			app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
				slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), nil, pool.Ping,
				transport.GroupProvisioning{
					Announcements: &jobs.AnnouncementService{Store: repo, River: producer},
					Templates:     failingTemplateStore{err: tc.err},
				})
			srv := httptestNew(app, t)
			code, group := doJSON(t, srv, http.MethodPost, "/api/groups",
				`{"name":"Template Error Group","courseName":"Go","startDate":"2026-12-01","staffEmails":["staff@test.example"],"lecturerEmails":["lecturer@test.example"]}`)
			if code != http.StatusCreated {
				t.Fatalf("create group: %d %v", code, group)
			}
			body := fmt.Sprintf(`{"requestId":%q,"channelName":"announcements","type":"general","templateId":%q,"scheduledAt":%q}`,
				uuid.NewString(), uuid.NewString(), time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
			code, response := doJSON(t, srv, http.MethodPost, "/api/groups/"+group["id"].(string)+"/announcements", body)
			if code != tc.want {
				t.Fatalf("schedule status=%d body=%v; want %d", code, response, tc.want)
			}
		})
	}
}

type fixtureAnnouncementPublisher struct {
	mu       sync.Mutex
	times    map[string][]time.Time
	channels map[string]int
}

func (f *fixtureAnnouncementPublisher) PostAnnouncement(_ context.Context, channel, message string) (string, bool, error) {
	if !slices.Contains([]string{"C-scheduled-test", "C-general", "C-questions", "C-resources"}, channel) {
		return "", false, fmt.Errorf("unexpected test channel")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.times[message] = append(f.times[message], time.Now())
	f.channels[message+"|"+channel]++
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
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), nil, pool.Ping,
		transport.GroupProvisioning{Announcements: &jobs.AnnouncementService{Store: repo, River: producer}})
	srv := httptestNew(app, t)
	code, group := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Announcements Group","courseName":"Go","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"]}`)
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
	generalBody := fmt.Sprintf(`{"requestId":%q,"channelName":"ignored-selection","type":"general","message":"All group channels","scheduledAt":%q}`, uuid.New(), when.Format(time.RFC3339Nano))

	if code, _ := doJSON(t, srv, http.MethodPost, route, body(requestID, "Test scheduled announcement")); code != http.StatusConflict {
		t.Fatalf("unlinked channel got %d, want 409", code)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO group_resources (group_id,provider,resource_type,resource_name,external_resource_id)
		VALUES ($1,'slack','channel','announcements-group-announcements','C-scheduled-test'),
		($1,'slack','channel','announcements-group-general','C-general'),
		($1,'slack','channel','announcements-group-questions','C-questions'),
		($1,'slack','channel','announcements-group-resources','C-resources')`, id); err != nil {
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
	code, allChannels := doJSON(t, srv, http.MethodPost, route, generalBody)
	if code != http.StatusAccepted || allChannels["schedule"].(map[string]any)["channelName"] != "all" {
		t.Fatalf("schedule general announcement: %d %v", code, allChannels)
	}
	if jobs := countRowsSimple(t, pool, "SELECT count(*) FROM river_job WHERE queue='slack_announcements'"); jobs != 3 {
		t.Fatalf("queued %d announcements, want 3", jobs)
	}

	publisher := &fixtureAnnouncementPublisher{times: make(map[string][]time.Time), channels: make(map[string]int)}
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

	for _, row := range []map[string]any{first, ambiguous, allChannels} {
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
		if completed == 6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d job executions finished", completed)
		}
		time.Sleep(100 * time.Millisecond)
	}
	items, err := repo.Announcements(t.Context(), mustUUID(t, id))
	if err != nil || len(items) != 3 {
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
		case "All group channels":
			if item.Status != "published" || len(item.ChannelIDs) != 4 {
				t.Fatalf("general fanout: %+v", item)
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
	if len(publisher.times["All group channels"]) != 4 {
		t.Fatalf("general announcement posts = %d, want 4", len(publisher.times["All group channels"]))
	}
	var delivered, failed int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE kind='announcement_delivered'),
		count(*) FILTER (WHERE kind='announcement_failed') FROM notifications`).Scan(&delivered, &failed); err != nil {
		t.Fatal("read announcement notifications:", err)
	}
	if delivered != 2 || failed != 1 {
		t.Fatalf("announcement notifications: delivered=%d failed=%d; want two delivered and one failed", delivered, failed)
	}

	code, other := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Other Announcements","courseName":"Go","startDate":"2026-12-01","staffEmails":["staff@test.example"],"lecturerEmails":["lecturer@test.example"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create other group: %d %v", code, other)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO scheduled_announcements
		(group_id,request_id,channel_id,channel_name,message_type,message_content,scheduled_at,status)
		VALUES ($1,gen_random_uuid(),'C-first','scheduled-channel','reminder','Keep scheduled',now()+interval '1 hour','scheduled'),
		       ($1,gen_random_uuid(),'C-first','scheduled-channel','general','Keep publishing',now(),'publishing'),
		       ($2,gen_random_uuid(),'C-other','other-channel','general','Keep other group',now(),'published')`, id, other["id"]); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if code, _ := doJSON(t, srv, http.MethodDelete, route+"/history", ""); code != http.StatusNoContent {
			t.Fatalf("clear history: %d, want 204", code)
		}
	}
	items, err = repo.Announcements(t.Context(), mustUUID(t, id))
	if err != nil || len(items) != 2 || items[0].Status != "scheduled" || items[1].Status != "publishing" {
		t.Fatalf("history after clear: %v, err=%v", items, err)
	}
	otherItems, err := repo.Announcements(t.Context(), mustUUID(t, other["id"].(string)))
	if err != nil || len(otherItems) != 1 || otherItems[0].Message != "Keep other group" {
		t.Fatalf("other group's history after clear: %v, err=%v", otherItems, err)
	}
	code, repeated = doJSON(t, srv, http.MethodPost, route, body(requestID, "Test scheduled announcement"))
	if code != http.StatusAccepted || repeated["id"] != first["id"] {
		t.Fatalf("old schedule idempotency after clear: %d %v", code, repeated)
	}
}
