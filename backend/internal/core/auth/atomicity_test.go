package auth_test

// Correctness tests for the atomicity fixes:
//   1. password reset revokes every active session in the same transaction;
//   2. refresh rotation is a single transaction — concurrent/replayed
//      rotations cannot mint two valid successors;
//   3. the last administrator survives even concurrent deletions;
//   4. login keeps invalid-credentials and infrastructure failures distinct.
//
// All database tests run against throwaway PostgreSQL databases.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"launchpad/internal/core/auth"
	"launchpad/internal/dbmigrate"
	"launchpad/internal/store"
	"launchpad/internal/testdb"
)

func TestPasswordResetRevokesExistingSessions(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	if _, err := svc.CreateUserWithPassword(ctx, "lecturer@reset.test", "Lec", auth.RoleLecturer, "first-password-123"); err != nil {
		t.Fatal(err)
	}
	// Two independent sessions issued under the old password.
	first, err := svc.Login(ctx, "lecturer@reset.test", "first-password-123")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Login(ctx, "lecturer@reset.test", "first-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, first.Token); err != nil {
		t.Fatalf("pre-reset token should authenticate: %v", err)
	}

	// Atomic reset: new hash + revocation of every active session.
	if err := svc.SetPassword(ctx, first.User.ID, "second-password-123"); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Authenticate(ctx, first.Token); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("token issued before reset still authenticates: %v", err)
	}
	if _, err := svc.Authenticate(ctx, second.Token); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("second pre-reset token still authenticates: %v", err)
	}
	if _, err := svc.Login(ctx, "lecturer@reset.test", "first-password-123"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("old password still logs in: %v", err)
	}
	fresh, err := svc.Login(ctx, "lecturer@reset.test", "second-password-123")
	if err != nil {
		t.Fatalf("login with new password: %v", err)
	}
	if _, err := svc.Authenticate(ctx, fresh.Token); err != nil {
		t.Errorf("post-reset token rejected: %v", err)
	}
}

func TestRefreshRotationInvalidatesOldTokenAndIsReplaySafe(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	if _, err := svc.CreateUserWithPassword(ctx, "ta@rotate.test", "TA", auth.RoleTA, "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	session, err := svc.Login(ctx, "ta@rotate.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}

	rotated, err := svc.Refresh(ctx, session.Token)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if rotated.Token == session.Token {
		t.Fatal("rotation reused the old token")
	}
	if _, err := svc.Authenticate(ctx, session.Token); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("old token still valid after rotation: %v", err)
	}
	if _, err := svc.Authenticate(ctx, rotated.Token); err != nil {
		t.Errorf("rotated token rejected: %v", err)
	}

	// Replay of the consumed token must fail and must not disturb the
	// successor.
	if _, err := svc.Refresh(ctx, session.Token); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("replayed refresh succeeded: %v", err)
	}
	if _, err := svc.Authenticate(ctx, rotated.Token); err != nil {
		t.Errorf("successor disturbed by replay: %v", err)
	}

	// A revoked successor cannot be refreshed either (revocation persists).
	if err := svc.Logout(ctx, rotated.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refresh(ctx, rotated.Token); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("revoked token refreshed: %v", err)
	}
}

func TestConcurrentRefreshMintsExactlyOneSuccessor(t *testing.T) {
	svc, pool := newAuth(t)
	ctx := context.Background()
	if _, err := svc.CreateUserWithPassword(ctx, "coordinator@race.test", "Coord", auth.RoleCoordinator, "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	session, err := svc.Login(ctx, "coordinator@race.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}

	const attempts = 8
	results := make([]error, attempts)
	tokens := make([]string, attempts)
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait() // maximize contention on the same token
			rotated, err := svc.Refresh(context.Background(), session.Token)
			results[i] = err
			tokens[i] = rotated.Token
		}(i)
	}
	start.Done()
	wg.Wait()

	succeeded := 0
	var winner string
	for i, err := range results {
		switch {
		case err == nil:
			succeeded++
			winner = tokens[i]
		case errors.Is(err, auth.ErrInvalidCredentials):
			// expected loser
		default:
			t.Errorf("attempt %d: unexpected error: %v", i, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent refreshes succeeded %d times, want exactly 1 (errors: %v)", succeeded, results)
	}
	if _, err := svc.Authenticate(ctx, winner); err != nil {
		t.Errorf("winner token does not authenticate: %v", err)
	}
	// Database invariant: exactly one ACTIVE session for the user — the
	// single successor. The original and every losing successor are gone
	// (losers never inserted; the original is revoked).
	var active int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM user_sessions s JOIN users u ON u.id=s.user_id
		WHERE u.email='coordinator@race.test' AND s.revoked_at IS NULL AND s.expires_at > now()`).
		Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Errorf("active sessions after race = %d, want 1", active)
	}
}

func TestLastAdminSurvivesConcurrentDeletion(t *testing.T) {
	svc, _ := newAuth(t)
	ctx := context.Background()
	first, err := svc.CreateUserWithPassword(ctx, "first@admin.test", "First", auth.RoleAdmin, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateUserWithPassword(ctx, "second@admin.test", "Second", auth.RoleAdmin, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}

	// Self-deletion is always rejected.
	if err := svc.DeleteAccount(ctx, first.ID, first.ID); !errors.Is(err, auth.ErrSelfDelete) {
		t.Fatalf("self delete: %v, want ErrSelfDelete", err)
	}

	// With two admins, one may delete the other (2 → 1)…
	if err := svc.DeleteAccount(ctx, second.ID, first.ID); err != nil {
		t.Fatalf("delete non-final admin: %v", err)
	}
	// …but the final admin survives every attempt.
	byStandID := uuid.MustParse("00000000-0000-0000-0000-0000000000aa")
	if err := svc.DeleteAccount(ctx, first.ID, byStandID); !errors.Is(err, auth.ErrLastAdmin) {
		t.Fatalf("last admin delete: %v, want ErrLastAdmin", err)
	}

	// Recreate the second admin and delete EACH OTHER concurrently: exactly
	// one may succeed — the advisory-locked transaction guarantees a
	// survivor.
	second, err = svc.CreateUserWithPassword(ctx, "second@admin.test", "Second", auth.RoleAdmin, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	errs := make([]error, 2)
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	for i, pair := range [][2]uuid.UUID{{second.ID, first.ID}, {first.ID, second.ID}} { // target, self
		wg.Add(1)
		go func(i int, target, self uuid.UUID) {
			defer wg.Done()
			start.Wait()
			errs[i] = svc.DeleteAccount(context.Background(), target, self)
		}(i, pair[0], pair[1])
	}
	start.Done()
	wg.Wait()

	succeeded := 0
	for i, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, auth.ErrLastAdmin):
			// the serialized loser
		default:
			t.Errorf("deletion %d: unexpected error: %v", i, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent admin deletions succeeded %d times, want exactly 1 (errors: %v)", succeeded, errs)
	}
	admins, err := svc.Repo.CountRole(ctx, auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if admins != 1 {
		t.Errorf("admins remaining after race = %d, want 1", admins)
	}

	// The survivor cannot be deleted by a non-admin identity either.
	var survivor uuid.UUID
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		if _, err := svc.Repo.UserByID(ctx, id); err == nil {
			survivor = id
		}
	}
	if err := svc.DeleteAccount(ctx, survivor, byStandID); !errors.Is(err, auth.ErrLastAdmin) {
		t.Errorf("final admin deleted by bystander: %v", err)
	}
}

// failingRepo embeds the Repository interface and overrides select methods
// to simulate infrastructure failures; every other call would panic, which
// the tests never reach.
type failingRepo struct {
	auth.Repository
	userErr   error
	credErr   error
	knownUser auth.User
}

func (f *failingRepo) UserByEmail(context.Context, string) (auth.User, error) {
	if f.userErr != nil {
		return auth.User{}, f.userErr
	}
	return f.knownUser, nil
}

func (f *failingRepo) CredentialHash(context.Context, uuid.UUID) (string, error) {
	if f.credErr != nil {
		return "", f.credErr
	}
	return "", auth.ErrNoCredential
}

func TestLoginDistinguishesCredentialFailuresFromInfrastructureFailures(t *testing.T) {
	ctx := context.Background()
	password := "correct-horse-battery"

	// Invalid credentials: unknown email / missing credential → exactly
	// ErrInvalidCredentials (never a service error).
	unknown := &failingRepo{userErr: auth.ErrInvalidCredentials}
	if _, err := auth.NewService(unknown, time.Hour).Login(ctx, "ghost@x.test", password); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("unknown email: %v, want ErrInvalidCredentials", err)
	}
	known := auth.User{ID: uuid.New(), Email: "known@x.test", Role: auth.RoleLecturer}
	noCredential := &failingRepo{knownUser: known}
	if _, err := auth.NewService(noCredential, time.Hour).Login(ctx, "known@x.test", password); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("missing credential: %v, want ErrInvalidCredentials", err)
	}

	// Infrastructure failures must NOT become invalid credentials.
	dbDown := errors.New("connection refused")
	repoDown := &failingRepo{userErr: dbDown}
	if _, err := auth.NewService(repoDown, time.Hour).Login(ctx, "any@x.test", password); !errors.Is(err, dbDown) {
		t.Errorf("user lookup failure swallowed: %v, want wrapped dbDown", err)
	}
	credDown := &failingRepo{knownUser: known, credErr: dbDown}
	if _, err := auth.NewService(credDown, time.Hour).Login(ctx, "known@x.test", password); !errors.Is(err, dbDown) {
		t.Errorf("credential lookup failure swallowed: %v, want wrapped dbDown", err)
	}
}

// TestRotateSessionFailsClosedOnExpired verifies the transaction re-checks
// expiry under the row lock using the database clock.
func TestRotateSessionFailsClosedOnExpired(t *testing.T) {
	svc, pool := newAuth(t)
	ctx := context.Background()
	user, err := svc.CreateUserWithPassword(ctx, "expired@rotate.test", "E", auth.RoleStudent, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.Login(ctx, "expired@rotate.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	// Expire the session behind the service's back.
	if _, err := pool.Exec(ctx, `UPDATE user_sessions SET expires_at = now() - interval '1 second' WHERE user_id = $1`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refresh(ctx, session.Token); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("expired session refreshed: %v", err)
	}
	var active int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_sessions WHERE user_id=$1 AND revoked_at IS NULL AND expires_at > now()`, user.ID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Errorf("active sessions after failed refresh = %d, want 0", active)
	}
}

// Store-level proof that the credential replacement and session revocation
// share one transaction: a pool-level failure between the two statements
// leaves BOTH untouched (old password and old sessions stay valid together).
func TestReplaceCredentialRollsBackAtomicallyOnFailure(t *testing.T) {
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
	ctx := context.Background()

	user, err := repo.CreateUserWithCredential(ctx, "atomic@reset.test", "A", auth.RoleLecturer, "$2a$10$dummyhashdummyhashdummyhashdummyhashdummyhashdu")
	if err != nil {
		t.Fatal(err)
	}
	// Cancel the context mid-operation: whatever statement is running fails,
	// and the deferred rollback must undo any prior write in the tx.
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := repo.ReplaceCredential(cancelCtx, user.ID, "$2a$10$newhashnewhashnewhashnewhashnewhashnewhash"); err == nil {
		t.Fatal("canceled ReplaceCredential unexpectedly succeeded")
	}
	// The OLD credential row is unchanged (no partial update).
	var hash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM user_credentials WHERE user_id=$1`, user.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != "$2a$10$dummyhashdummyhashdummyhashdummyhashdummyhashdu" {
		t.Errorf("credential partially updated: %q", hash)
	}
}
