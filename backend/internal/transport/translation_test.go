package transport_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"launchpad/internal/core/auth"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

func TestDraftTranslationRequiresStaffAndReturnsPreview(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "users")
	svc := auth.NewService(store.New(pool), time.Hour)
	ctx := context.Background()
	createSession := func(email, role string) string {
		t.Helper()
		const password = "safe-password-test"
		if _, err := svc.CreateUserWithPassword(ctx, email, "Member", role, password); err != nil {
			t.Fatal(err)
		}
		session, err := svc.Login(ctx, email, password)
		if err != nil {
			t.Fatal(err)
		}
		return session.Token
	}
	staff := createSession("staff@translation.test", auth.RoleCoordinator)
	student := createSession("student@translation.test", auth.RoleStudent)
	app := transport.New(testConfig(), discardLogger(), nil, nil, nil, nil, nil, svc, pool.Ping,
		transport.GroupProvisioning{TranslateText: func(_ context.Context, text, target string) (string, error) {
			if text != "Hello" || target != "hy" {
				return "", fmt.Errorf("unexpected translation input")
			}
			return "Բարև", nil
		}})
	srv := httptestNew(app, t)
	const request = `{"text":"Hello","target":"hy"}`
	if status, _ := doBearerJSON(t, srv, student, http.MethodPost, "/api/translation", request); status != http.StatusForbidden {
		t.Fatalf("student translation status = %d", status)
	}
	if status, _ := doBearerJSON(t, srv, staff, http.MethodPost, "/api/translation", `{"text":"Hello","target":"invalid"}`); status != http.StatusBadRequest {
		t.Fatalf("invalid target status = %d", status)
	}
	status, body := doBearerJSON(t, srv, staff, http.MethodPost, "/api/translation", request)
	if status != http.StatusOK || body["text"] != "Բարև" {
		t.Fatalf("translation response = %d %v", status, body)
	}
}
