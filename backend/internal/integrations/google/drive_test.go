package google

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDriveRecoversExistingFolderByReservedID(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer drive-test-token" {
			t.Error("missing access token")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/files/generateIds":
			_, _ = w.Write([]byte(`{"ids":["reserved-id"]}`))
			return
		case r.Method == http.MethodPost && r.URL.Path == "/files":
			var folder driveFile
			if err := json.NewDecoder(r.Body).Decode(&folder); err != nil {
				t.Error(err)
			}
			if folder.ID != "reserved-id" || folder.Name != "General" || folder.MIMEType != driveFolderMIME ||
				len(folder.Parents) != 1 || folder.Parents[0] != "parent-id" || r.URL.Query().Get("ignoreDefaultVisibility") != "true" {
				t.Errorf("unexpected folder request: %+v", folder)
			}
			if posts.Add(1) > 1 {
				w.WriteHeader(http.StatusConflict)
				return
			}
		case r.Method == http.MethodGet && r.URL.Path == "/files/reserved-id":
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"id":"reserved-id","mimeType":"application/vnd.google-apps.folder","parents":["parent-id"]}`))
	}))
	defer server.Close()

	drive := &DriveGateway{filesURL: server.URL + "/files", client: server.Client()}
	id, err := drive.GenerateFolderID(t.Context(), "drive-test-token")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := drive.EnsureFolder(t.Context(), "drive-test-token", id, "General", "parent-id"); err != nil {
			t.Fatal(err)
		}
	}
}
