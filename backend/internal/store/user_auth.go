// Store implementation of the auth.Repository port (migration 0016 tables).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"launchpad/internal/core"
	"launchpad/internal/core/auth"
)

func (r *Repo) UserByEmail(ctx context.Context, normalizedEmail string) (auth.User, error) {
	var u auth.User
	err := r.pool.QueryRow(ctx,
		`SELECT id, email, full_name, role, created_at FROM users WHERE lower(btrim(email)) = $1`,
		normalizedEmail).Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, auth.ErrInvalidCredentials
	}
	if err != nil {
		return auth.User{}, fmt.Errorf("store: user by email: %w", err)
	}
	return u, nil
}

func (r *Repo) UserByID(ctx context.Context, id uuid.UUID) (auth.User, error) {
	var u auth.User
	err := r.pool.QueryRow(ctx,
		`SELECT id, email, full_name, role, created_at FROM users WHERE id = $1`,
		id).Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, core.NotFound("user", id.String())
	}
	if err != nil {
		return auth.User{}, fmt.Errorf("store: user by id: %w", err)
	}
	return u, nil
}

func (r *Repo) CredentialHash(ctx context.Context, userID uuid.UUID) (string, error) {
	var hash string
	err := r.pool.QueryRow(ctx,
		`SELECT password_hash FROM user_credentials WHERE user_id = $1`, userID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", auth.ErrNoCredential
	}
	if err != nil {
		return "", fmt.Errorf("store: credential hash: %w", err)
	}
	return hash, nil
}

// createUserRow reads back a user by normalized email (conflict path).
func createUserRow(ctx context.Context, q queryRower, normalizedEmail string) (auth.User, error) {
	var u auth.User
	err := q.QueryRow(ctx,
		`SELECT id, email, full_name, role, created_at FROM users WHERE lower(btrim(email)) = $1`,
		normalizedEmail).Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.CreatedAt)
	return u, err
}

// queryRower abstracts pool and tx for shared read helpers.
type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (r *Repo) CreateUser(ctx context.Context, email, fullName, role string) (auth.User, error) {
	var u auth.User
	err := r.pool.QueryRow(ctx,
		`INSERT INTO users (email, full_name, role) VALUES ($1, $2, $3)
		 ON CONFLICT DO NOTHING RETURNING id, email, full_name, role, created_at`,
		email, fullName, role).Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) { // conflict path — report the existing row
		u, lookupErr := createUserRow(ctx, r.pool, email)
		if lookupErr == nil {
			// Users are shared across roles (e.g. a lecturer in one group may
			// be a student elsewhere); creating a duplicate is a conflict for
			// callers, who decide whether to keep the existing role.
			return u, &core.AppError{Code: core.CodeConflict, Message: "a user with this email already exists (role " + u.Role + ")"}
		}
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			// The conflicting row vanished between insert and lookup (race):
			// indistinguishable from a conflict for the caller.
			return auth.User{}, &core.AppError{Code: core.CodeConflict, Message: "a user with this email already exists"}
		}
		return auth.User{}, fmt.Errorf("store: create user: %w", lookupErr)
	}
	if err != nil {
		return auth.User{}, fmt.Errorf("store: create user: %w", err)
	}
	return u, nil
}

// CreateUserWithCredential inserts the user and its password hash in ONE
// transaction — no partial "user without credential" state can survive.
func (r *Repo) CreateUserWithCredential(ctx context.Context, email, fullName, role, passwordHash string) (auth.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return auth.User{}, fmt.Errorf("store: create user with credential tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck — no-op after commit

	var u auth.User
	err = tx.QueryRow(ctx,
		`INSERT INTO users (email, full_name, role) VALUES ($1, $2, $3)
		 ON CONFLICT DO NOTHING RETURNING id, email, full_name, role, created_at`,
		email, fullName, role).Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if existing, lookupErr := createUserRow(ctx, tx, email); lookupErr == nil {
			return existing, &core.AppError{Code: core.CodeConflict, Message: "a user with this email already exists (role " + existing.Role + ")"}
		} else if errors.Is(lookupErr, pgx.ErrNoRows) {
			return auth.User{}, &core.AppError{Code: core.CodeConflict, Message: "a user with this email already exists"}
		} else {
			return auth.User{}, fmt.Errorf("store: create user: %w", lookupErr)
		}
	}
	if err != nil {
		return auth.User{}, fmt.Errorf("store: create user: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_credentials (user_id, password_hash) VALUES ($1, $2)`,
		u.ID, passwordHash); err != nil {
		return auth.User{}, fmt.Errorf("store: create credential: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.User{}, fmt.Errorf("store: create user with credential commit: %w", err)
	}
	return u, nil
}

func (r *Repo) ListUsers(ctx context.Context) ([]auth.User, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, email, full_name, role, created_at FROM users ORDER BY created_at, id LIMIT 500`)
	if err != nil {
		return nil, fmt.Errorf("store: list users: %w", err)
	}
	defer rows.Close()
	var out []auth.User
	for rows.Next() {
		var u auth.User
		if err := rows.Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *Repo) DeleteUser(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("store: delete user: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) CountRole(ctx context.Context, role string) (int, error) {
	var count int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = $1`, role).Scan(&count); err != nil {
		return 0, fmt.Errorf("store: count role: %w", err)
	}
	return count, nil
}

func (r *Repo) InsertSession(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO user_sessions (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt)
	if err != nil {
		return fmt.Errorf("store: insert session: %w", err)
	}
	return nil
}

// SessionByToken returns the session's user when the hash matches an active
// (unrevoked, unexpired) session, touching last_used_at on success.
func (r *Repo) SessionByToken(ctx context.Context, tokenHash string) (auth.User, time.Time, error) {
	var u auth.User
	var expires time.Time
	err := r.pool.QueryRow(ctx, `
		UPDATE user_sessions s SET last_used_at = now()
		FROM users u
		WHERE s.user_id = u.id AND s.token_hash = $1
		  AND s.revoked_at IS NULL AND s.expires_at > now()
		RETURNING u.id, u.email, u.full_name, u.role, u.created_at, s.expires_at`,
		tokenHash).Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.CreatedAt, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, time.Time{}, auth.ErrInvalidCredentials
	}
	if err != nil {
		return auth.User{}, time.Time{}, fmt.Errorf("store: session by token: %w", err)
	}
	return u, expires, nil
}

// ReplaceCredential updates the password hash and revokes every active
// session of the user in ONE transaction: a token issued before a password
// reset can never authenticate afterwards, whatever the interleaving.
func (r *Repo) ReplaceCredential(ctx context.Context, userID uuid.UUID, hash string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: replace credential tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck — no-op after commit

	if _, err := tx.Exec(ctx,
		`INSERT INTO user_credentials (user_id, password_hash) VALUES ($1, $2)
		 ON CONFLICT (user_id) DO UPDATE SET password_hash = EXCLUDED.password_hash, updated_at = now()`,
		userID, hash); err != nil {
		return fmt.Errorf("store: replace credential: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE user_sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
		userID); err != nil {
		return fmt.Errorf("store: revoke sessions on reset: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: replace credential commit: %w", err)
	}
	return nil
}

// RotateSession atomically verifies + revokes the old session and inserts
// the successor. SELECT … FOR UPDATE serializes concurrent rotations of the
// same token: the second transaction sees revoked_at set and fails with
// ErrInvalidCredentials. The insert and revoke commit together — a failure
// leaves the old session untouched (the client simply retries).
func (r *Repo) RotateSession(ctx context.Context, oldTokenHash, newTokenHash string, expiresAt time.Time) (auth.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return auth.User{}, fmt.Errorf("store: rotate session tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck — no-op after commit

	var (
		u              auth.User
		sessionID      uuid.UUID
		alreadyRevoked bool
	)
	err = tx.QueryRow(ctx, `
		SELECT u.id, u.email, u.full_name, u.role, u.created_at, s.id, (s.revoked_at IS NOT NULL)
		FROM user_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1
		FOR UPDATE OF s`,
		oldTokenHash).Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.CreatedAt, &sessionID, &alreadyRevoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, auth.ErrInvalidCredentials
	}
	if err != nil {
		return auth.User{}, fmt.Errorf("store: rotate session lock: %w", err)
	}
	// Re-check liveness under the lock: revoked or expired sessions are not
	// refreshable (expiry is evaluated against the database clock).
	var alive bool
	if err := tx.QueryRow(ctx, `
		SELECT revoked_at IS NULL AND expires_at > now() FROM user_sessions WHERE id = $1`,
		sessionID).Scan(&alive); err != nil {
		return auth.User{}, fmt.Errorf("store: rotate session check: %w", err)
	}
	if alreadyRevoked || !alive {
		return auth.User{}, auth.ErrInvalidCredentials
	}
	if _, err := tx.Exec(ctx, `UPDATE user_sessions SET revoked_at = now() WHERE id = $1`, sessionID); err != nil {
		return auth.User{}, fmt.Errorf("store: rotate session revoke: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_sessions (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		u.ID, newTokenHash, expiresAt); err != nil {
		return auth.User{}, fmt.Errorf("store: rotate session insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.User{}, fmt.Errorf("store: rotate session commit: %w", err)
	}
	return u, nil
}

// DeleteUserProtected deletes a user account with the administrator
// protections enforced inside one serialized transaction:
//
//   - an admin never deletes their own account (ErrSelfDelete);
//   - the last administrator survives (ErrLastAdmin).
//
// A transaction-scoped advisory lock serializes concurrent admin deletions:
// two parallel attempts to remove the final two admins cannot both pass the
// count check — one commits, the other observes a single remaining admin and
// aborts. Non-admin targets bypass the lock.
func (r *Repo) DeleteUserProtected(ctx context.Context, id, selfID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: delete user tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck — no-op after commit

	var role string
	err = tx.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, id).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.NotFound("user", id.String())
	}
	if err != nil {
		return fmt.Errorf("store: delete user role: %w", err)
	}
	if id == selfID {
		return auth.ErrSelfDelete
	}
	if role == auth.RoleAdmin {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('launchpad:admin_delete'))`); err != nil {
			return fmt.Errorf("store: admin delete lock: %w", err)
		}
		var admins int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = $1`, auth.RoleAdmin).Scan(&admins); err != nil {
			return fmt.Errorf("store: count admins: %w", err)
		}
		if admins <= 1 {
			return auth.ErrLastAdmin
		}
	}
	tag, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("store: delete user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return core.NotFound("user", id.String())
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: delete user commit: %w", err)
	}
	return nil
}

func (r *Repo) RevokeSession(ctx context.Context, tokenHash string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE user_sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`,
		tokenHash)
	if err != nil {
		return fmt.Errorf("store: revoke session: %w", err)
	}
	return nil
}
