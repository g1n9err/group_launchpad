package transport_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"launchpad/internal/core/integrations"
	"launchpad/internal/jobs"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

type fakeIntakeMailer struct {
	mu    sync.Mutex
	calls map[string]int
}

func (m *fakeIntakeMailer) Send(_ context.Context, email integrations.IntakeEmail) (bool, error) {
	if email.ResponderURL != testResponderURL {
		return false, errors.New("unexpected responder URL")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls[email.Recipient]++
	switch {
	case email.Recipient == "unknown@example.test":
		return true, errors.New("acknowledgement lost")
	case email.Recipient == "retry@example.test" && m.calls[email.Recipient] == 1:
		return false, errors.New("recipient rejected")
	default:
		return false, nil
	}
}

func TestIntakeDeliveryQueuesRosterAndRetriesOnlyRejectedMail(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users", "river_job")
	repo := store.New(pool)
	producer, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	delivery := &jobs.IntakeDeliveryService{Queue: repo, River: producer}
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), nil, pool.Ping,
		transport.GroupProvisioning{Delivery: delivery, DeliveryEnabled: true})
	srv := httptestNew(app, t)

	code, group := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Delivery Group","courseName":"Go","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"],"studentEmails":["sent@example.test","retry@example.test","unknown@example.test"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, group)
	}
	groupID := group["id"].(string)
	path := "/api/groups/" + groupID + "/forms"
	if _, err := pool.Exec(t.Context(), "INSERT INTO intake_forms (group_id, status) VALUES ($1,'created')", groupID); err != nil {
		t.Fatal(err)
	}
	if code, _ := doJSON(t, srv, http.MethodPost, path+"/distribute", ""); code != http.StatusConflict {
		t.Fatalf("distribution before publishing: %d, want 409", code)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE intake_forms SET external_form_id='test-form', form_url=$2 WHERE group_id=$1", groupID, testResponderURL); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		code, result := doJSON(t, srv, http.MethodPost, path+"/distribute", "")
		if code != http.StatusAccepted || len(result["deliveries"].([]any)) != 3 {
			t.Fatalf("queue: %d %v", code, result)
		}
	}
	if count := countRowsSimple(t, pool, "SELECT count(*) FROM river_job WHERE kind='intake_delivery' AND queue='intake_email'"); count != 3 {
		t.Fatalf("queued %d jobs, want 3", count)
	}

	mailer := &fakeIntakeMailer{calls: make(map[string]int)}
	workers := river.NewWorkers()
	river.AddWorker(workers, &jobs.IntakeDeliveryWorker{Store: repo, Mailer: mailer})
	worker, err := jobs.NewClient(pool, workers, jobs.Config{IntakeEmailWorkers: 2})
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

	waitFor := func(retryStatus string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			code, response := doJSON(t, srv, http.MethodGet, path+"/deliveries", "")
			if code != http.StatusOK {
				t.Fatalf("delivery status: %d %v", code, response)
			}
			status := make(map[string]string)
			for _, item := range response["deliveries"].([]any) {
				row := item.(map[string]any)
				status[row["recipient"].(string)] = row["status"].(string)
			}
			if status["sent@example.test"] == "sent" && status["retry@example.test"] == retryStatus && status["unknown@example.test"] == "unknown" {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("delivery status: %v, want retry recipient %s", status, retryStatus)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitFor("failed")
	if code, result := doJSON(t, srv, http.MethodPost, path+"/distribute", ""); code != http.StatusAccepted {
		t.Fatalf("requeue: %d %v", code, result)
	}
	waitFor("sent")
	mailer.mu.Lock()
	defer mailer.mu.Unlock()
	if mailer.calls["sent@example.test"] != 1 || mailer.calls["retry@example.test"] != 2 || mailer.calls["unknown@example.test"] != 1 {
		t.Fatalf("unexpected relay attempts: %v", mailer.calls)
	}
}
