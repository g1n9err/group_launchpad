package transport_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"launchpad/internal/core/integrations"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

type fakeDrive struct {
	mu        sync.Mutex
	generated int
	created   map[string]string
}

func (*fakeDrive) AccessToken(context.Context) (string, error) { return "drive-test-token", nil }

func (f *fakeDrive) GenerateFolderID(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generated++
	return fmt.Sprintf("drive-folder-%d", f.generated), nil
}

func (f *fakeDrive) EnsureFolder(_ context.Context, _, id, name, parent string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, existed := f.created[id]
	f.created[id] = parent
	if name == "Lectures" && !existed {
		return errors.New("response lost after creation")
	}
	return nil
}

func (f *fakeDrive) GrantAccess(context.Context, string, string, string, string) error {
	return nil // transport tests don't assert permissions
}

func (f *fakeDrive) RevokeAccess(context.Context, string, string, string) error { return nil }

func TestDriveProvisionResumesAfterInterruptedCreate(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups")
	repo := store.New(pool)
	fake := &fakeDrive{created: make(map[string]string)}
	service := &integrations.DriveService{Tokens: fake, Gateway: fake, Store: repo, Operations: repo}
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), nil, pool.Ping,
		transport.GroupProvisioning{Drive: service})
	srv := httptestNew(app, t)

	code, group := doJSON(t, srv, http.MethodPost, "/api/groups", `{"name":"Drive Provision","courseName":"Go","startDate":"2026-12-01", "staffEmails": ["staff@test.example"], "lecturerEmails": ["lecturer@test.example"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, group)
	}
	path := "/api/groups/" + group["id"].(string) + "/drive/provision"
	if code, response := doJSON(t, srv, http.MethodPost, path, ""); code != http.StatusBadGateway {
		t.Fatalf("interrupted provisioning: %d %v", code, response)
	}
	code, first := doJSON(t, srv, http.MethodPost, path, "")
	if code != http.StatusOK {
		t.Fatalf("resume: %d %v", code, first)
	}
	code, second := doJSON(t, srv, http.MethodPost, path, "")
	if code != http.StatusOK || !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated provisioning returned %d %v, want %v", code, second, first)
	}
	root := first["root"].(map[string]any)["id"]
	subfolders := first["subfolders"].([]any)
	if len(subfolders) != 5 {
		t.Fatalf("got %d subfolders, want 5", len(subfolders))
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.created) != 6 || fake.generated != 6 {
		t.Fatalf("created %d folders and generated %d IDs, want 6 each", len(fake.created), fake.generated)
	}
	for _, entry := range subfolders {
		folder := entry.(map[string]any)
		if parent := fake.created[folder["id"].(string)]; parent != root {
			t.Fatalf("folder %v has parent %s, want %v", folder, parent, root)
		}
	}
	if rows := countRowsSimple(t, pool, "SELECT count(*) FROM group_resources WHERE provider='google_drive' AND status='active'"); rows != 6 {
		t.Fatalf("saved %d active folder resources, want 6", rows)
	}
}
