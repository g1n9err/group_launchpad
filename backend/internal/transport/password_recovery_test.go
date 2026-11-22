package transport_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"launchpad/internal/core/auth"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

func TestEmailPasswordRecoveryConsumesLinkAndRevokesSessions(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "users")
	repo := store.New(pool)
	svc := auth.NewService(repo, time.Hour)
	ctx := context.Background()
	const email = "member@recovery.test"
	const oldPassword = "old-password-test"
	const newPassword = "new-password-test"
	if _, err := svc.CreateUserWithPassword(ctx, email, "Member", auth.RoleCoordinator, oldPassword); err != nil {
		t.Fatal(err)
	}
	session, err := svc.Login(ctx, email, oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	messages := make(chan string, 2)
	recovery := &transport.PasswordRecovery{
		Auth:    auth.Recovery{Repo: repo, Key: "password-recovery-test-key"},
		SiteURL: "https://launchpad.example",
		Send: func(_ context.Context, recipient, _, body string) (bool, error) {
			if recipient != email {
				return false, fmt.Errorf("unexpected recipient %q", recipient)
			}
			messages <- body
			return false, nil
		},
	}
	app := transport.New(testConfig(), discardLogger(), nil, nil, nil, nil, nil, svc, pool.Ping,
		transport.GroupProvisioning{PasswordRecovery: recovery})
	srv := httptestNew(app, t)
	post := func(path string, body map[string]string) int {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Post(srv.URL+path, "application/json", strings.NewReader(string(payload)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	for _, address := range []string{email, "unknown@recovery.test"} {
		if status := post("/api/auth/password-reset/request", map[string]string{"email": address}); status != http.StatusAccepted {
			t.Fatalf("reset request status = %d", status)
		}
	}
	var token string
	select {
	case message := <-messages:
		_, link, found := strings.Cut(message, "https://launchpad.example#/reset-password/")
		if !found {
			t.Fatalf("recovery email missing reset link: %q", message)
		}
		token = strings.Fields(link)[0]
	case <-time.After(5 * time.Second):
		t.Fatal("recovery email not delivered")
	}
	body := map[string]string{"token": token, "password": newPassword}
	if status := post("/api/auth/password-reset", body); status != http.StatusNoContent {
		t.Fatalf("password reset status = %d", status)
	}
	if status := post("/api/auth/password-reset", body); status != http.StatusBadRequest {
		t.Fatalf("replayed reset status = %d", status)
	}
	if _, err := svc.Authenticate(ctx, session.Token); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("previous session remained valid: %v", err)
	}
	if _, err := svc.Login(ctx, email, newPassword); err != nil {
		t.Fatalf("could not sign in with new password: %v", err)
	}
}
