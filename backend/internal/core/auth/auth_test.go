package auth_test

// Multi-role authentication tests against real PostgreSQL (migration 0016):
// credential verification, session lifecycle, rotation, and the admin
// bootstrap. No external services are touched.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"launchpad/internal/core"
	"launchpad/internal/core/auth"
	"launchpad/internal/dbmigrate"
	"launchpad/internal/store"
	"launchpad/internal/testdb"
)

func newAuth(t *testing.T) (*auth.Service, *pgxpool.Pool) {
	t.Helper()
	url := testdb.New(t)
	if err := dbmigrate.Up(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo := store.New(pool)
	return auth.NewService(repo, time.Hour), pool
}

func TestLoginIssuesWorkingSession(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	if _, err := svc.CreateUserWithPassword(ctx, "Coordinator@Test.Example", "Coord One", auth.RoleCoordinator, "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	session, err := svc.Login(ctx, "  coordinator@test.example ", "correct-horse-battery")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if session.Token == "" || session.User.Role != auth.RoleCoordinator {
		t.Fatalf("session: %+v", session)
	}
	user, err := svc.Authenticate(ctx, session.Token)
	if err != nil || user.Email != "coordinator@test.example" {
		t.Fatalf("authenticate: %v %+v", err, user)
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	if _, err := svc.CreateUserWithPassword(ctx, "lecturer@test.example", "Lec", auth.RoleLecturer, "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, email, password string
	}{
		{"unknown email", "ghost@test.example", "correct-horse-battery"},
		{"wrong password", "lecturer@test.example", "wrong-password-123"},
		{"empty password", "lecturer@test.example", ""},
		{"student without credential", "student@test.example", "whatever-password"},
	}
	if _, err := svc.Repo.CreateUser(ctx, "student@test.example", "Stu", auth.RoleStudent); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		if _, err := svc.Login(ctx, tc.email, tc.password); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Errorf("%s: err=%v, want ErrInvalidCredentials", tc.name, err)
		}
	}
}

func TestAuthenticateFailsClosed(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	if _, err := svc.CreateUserWithPassword(ctx, "admin@test.example", "Admin", auth.RoleAdmin, "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	session, err := svc.Login(ctx, "admin@test.example", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, "not-the-token"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("forged token: err=%v", err)
	}
	if _, err := svc.Authenticate(ctx, ""); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("empty token: err=%v", err)
	}
	// Logout revokes; the same token must never work again.
	if err := svc.Logout(ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, session.Token); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("revoked token still authenticates: err=%v", err)
	}
}

func TestSessionExpiryFailsClosed(t *testing.T) {
	svc, pool := newAuth(t)
	ctx := context.Background()
	if _, err := svc.CreateUserWithPassword(ctx, "ta@test.example", "TA", auth.RoleTA, "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	session, err := svc.Login(ctx, "ta@test.example", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	// Force expiry behind the service's back.
	if _, err := pool.Exec(ctx, `UPDATE user_sessions SET expires_at = now() - interval '1 second' WHERE token_hash = $1`, session.Hash); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, session.Token); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("expired token: err=%v", err)
	}
}

func TestRefreshRotatesToken(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	if _, err := svc.CreateUserWithPassword(ctx, "coordinator@test.example", "C", auth.RoleCoordinator, "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	first, err := svc.Login(ctx, "coordinator@test.example", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Refresh(ctx, first.Token)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if second.Token == first.Token {
		t.Fatal("refresh reused the old token")
	}
	if _, err := svc.Authenticate(ctx, first.Token); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Error("old token still valid after rotation")
	}
	if _, err := svc.Authenticate(ctx, second.Token); err != nil {
		t.Errorf("new token rejected: %v", err)
	}
	// A revoked (rotated-away) token cannot refresh again.
	if _, err := svc.Refresh(ctx, first.Token); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Error("revoked token could refresh")
	}
}

func TestPasswordPolicyAndValidation(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	if _, err := svc.CreateUserWithPassword(ctx, "short@test.example", "S", auth.RoleStudent, "short"); err == nil {
		t.Error("short password accepted")
	}
	if _, err := svc.CreateUserWithPassword(ctx, "long@test.example", "S", auth.RoleStudent,
		"a-very-long-password-that-exceeds-bcrypts-seventy-two-byte-input-limit-1234567890"); err == nil {
		t.Error("over-72-byte password accepted (bcrypt would error)")
	}
	if _, err := svc.CreateUserWithPassword(ctx, "bad-email", "S", auth.RoleStudent, "correct-horse-battery"); err == nil {
		t.Error("invalid email accepted")
	}
	if _, err := svc.CreateUserWithPassword(ctx, "role@test.example", "S", "wizard", "correct-horse-battery"); err == nil {
		t.Error("unknown role accepted")
	}
}

func TestBootstrapAdminIdempotent(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	created, err := svc.BootstrapAdmin(ctx, "Root@test.example", "correct-horse-battery")
	if err != nil || !created {
		t.Fatalf("first bootstrap: created=%t err=%v", created, err)
	}
	created, err = svc.BootstrapAdmin(ctx, "Other@test.example", "correct-horse-battery")
	if err != nil || created {
		t.Fatalf("second bootstrap must not create: created=%t err=%v", created, err)
	}
	// The bootstrapped admin can log in with the given password.
	if _, err := svc.Login(ctx, "root@test.example", "correct-horse-battery"); err != nil {
		t.Fatalf("login as bootstrap admin: %v", err)
	}
	count, err := svc.Repo.CountRole(ctx, auth.RoleAdmin)
	if err != nil || count != 1 {
		t.Fatalf("admins=%d err=%v", count, err)
	}
}

func TestPasswordResetAndDeleteUser(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	user, err := svc.CreateUserWithPassword(ctx, "lecturer@test.example", "Lec", auth.RoleLecturer, "first-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPassword(ctx, user.ID, "second-password-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "lecturer@test.example", "first-password-123"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Error("old password still works after reset")
	}
	if _, err := svc.Login(ctx, "lecturer@test.example", "second-password-123"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	found, err := svc.Repo.DeleteUser(ctx, user.ID)
	if err != nil || !found {
		t.Fatalf("delete: found=%t err=%v", found, err)
	}
	if _, err := svc.Login(ctx, "lecturer@test.example", "second-password-123"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Error("deleted user can still log in")
	}
}

func TestCreateUserDuplicateIsAConflict(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	if _, err := svc.CreateUserWithPassword(ctx, "dup@test.example", "One", auth.RoleLecturer, "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.CreateUserWithPassword(ctx, "DUP@test.example", "Two", auth.RoleTA, "correct-horse-battery")
	var appErr *core.AppError
	if !errors.As(err, &appErr) || appErr.Code != core.CodeConflict {
		t.Fatalf("duplicate create err=%v, want CONFLICT", err)
	}
	// The credential of the FIRST account is untouched.
	if _, err := svc.Login(ctx, "dup@test.example", "correct-horse-battery"); err != nil {
		t.Errorf("original account broken by duplicate attempt: %v", err)
	}
}
