package transport_test

import (
	"net/http"
	"testing"

	"launchpad/internal/store"
	"launchpad/internal/transport"
)

func TestCoordinatorNotificationFeedAndReadActions(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "notifications")
	repo := store.New(pool)
	if err := repo.InsertNotificationOnce(t.Context(), nil, "launch_completed", "info", "Group launch completed", "", "test:launch"); err != nil {
		t.Fatal(err)
	}
	// River retries must not create a second row for the same source event.
	if err := repo.InsertNotificationOnce(t.Context(), nil, "launch_completed", "info", "Duplicate", "", "test:launch"); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertNotification(t.Context(), nil, "audit_inconsistency", "warning", "Audit found a change", "resource was unavailable"); err != nil {
		t.Fatal(err)
	}
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), nil, pool.Ping,
		transport.GroupProvisioning{Notifications: repo})
	srv := httptestNew(app, t)

	code, page := doJSON(t, srv, http.MethodGet, "/api/notifications?limit=10&offset=0", "")
	if code != http.StatusOK {
		t.Fatalf("list notifications: %d %v", code, page)
	}
	data, ok := page["data"].([]any)
	if !ok || len(data) != 2 {
		t.Fatalf("notification page=%v; want 2 records", page)
	}
	if page["unreadCount"] != float64(2) {
		t.Fatalf("unread count=%v; want 2", page["unreadCount"])
	}
	id := data[0].(map[string]any)["id"].(string)
	code, marked := doJSON(t, srv, http.MethodPost, "/api/notifications/"+id+"/read", "")
	if code != http.StatusOK || marked["status"] != "read" {
		t.Fatalf("mark notification read: %d %v", code, marked)
	}
	if code, marked = doJSON(t, srv, http.MethodPost, "/api/notifications/read-all", ""); code != http.StatusOK || marked["status"] != "all_read" {
		t.Fatalf("mark all read: %d %v", code, marked)
	}
	var unread int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM notifications WHERE read_at IS NULL`).Scan(&unread); err != nil {
		t.Fatal(err)
	}
	if unread != 0 {
		t.Fatalf("unread notification count=%d; want 0", unread)
	}
	code, page = doJSON(t, srv, http.MethodGet, "/api/notifications?limit=10&offset=0", "")
	if code != http.StatusOK || page["unreadCount"] != float64(0) {
		t.Fatalf("read notification feed: status=%d unreadCount=%v", code, page["unreadCount"])
	}
}
