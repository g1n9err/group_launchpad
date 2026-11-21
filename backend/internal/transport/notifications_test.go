package transport_test

import (
	"encoding/json"
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

func TestCoordinatorNotificationDelete(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "notifications")
	repo := store.New(pool)
	if err := repo.InsertNotificationOnce(t.Context(), nil, "launch_completed", "info", "T", "", "del:1"); err != nil {
		t.Fatal(err)
	}
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), nil, pool.Ping,
		transport.GroupProvisioning{Notifications: repo})
	srv := httptestNew(app, t)

	code, page := doJSON(t, srv, http.MethodGet, "/api/notifications?limit=10&offset=0", "")
	if code != http.StatusOK || len(page["data"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, page)
	}
	id := page["data"].([]any)[0].(map[string]any)["id"].(string)

	// Ջնջում՝ 204, երկրորդ անգամ՝ 404։
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/notifications/"+id, nil)
	req.SetBasicAuth("coordinator", testCoordinatorPassword)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d, want 204", resp.StatusCode)
	}
	req2, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/notifications/"+id, nil)
	req2.SetBasicAuth("coordinator", testCoordinatorPassword)
	resp2, err := srv.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("re-delete: %d, want 404", resp2.StatusCode)
	}

	// DELETE ամբողջ հավաքածուի վրա՝ մնացածը ջնջում է ու վերադարձնում քանակը։
	if err := repo.InsertNotification(t.Context(), nil, "audit_inconsistency", "warning", "T2", "d"); err != nil {
		t.Fatal(err)
	}
	req3, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/notifications", nil)
	req3.SetBasicAuth("coordinator", testCoordinatorPassword)
	resp3, err := srv.Client().Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("delete all: %d, want 200", resp3.StatusCode)
	}
	var out map[string]any
	_ = json.NewDecoder(resp3.Body).Decode(&out)
	if out["deleted"] != float64(1) {
		t.Fatalf("deleted = %v, want 1", out["deleted"])
	}
}
