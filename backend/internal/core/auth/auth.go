// Package auth implements multi-role authentication (PRD §3, §7.4).
//
// Roles come from the PRD's user model: coordinator (primary operator),
// admin (configuration & staff management), lecturer, ta, and student.
// Students interact through Google Forms and Slack — the PRD grants them no
// API surface, so authentication alone never authorizes them (fail closed).
//
// Credentials are bcrypt hashes; sessions are random 256-bit bearer tokens
// stored ONLY as SHA-256 hashes. Tokens are returned once at login/refresh,
// expire, and can be revoked (logout). Nothing lands in URLs.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"launchpad/internal/core"
)

// Roles (mirrors the users.role CHECK constraint, PRD §7.2).
const (
	RoleAdmin       = "admin"
	RoleCoordinator = "coordinator"
	RoleLecturer    = "lecturer"
	RoleTA          = "ta"
	RoleStudent     = "student"
)

// ErrInvalidCredentials is returned for every failed login — unknown email,
// missing credential, and wrong password are indistinguishable (no user
// enumeration). Infrastructure failures are NEVER mapped to this error.
var ErrInvalidCredentials = errors.New("auth: invalid credentials")

// ErrSelfDelete marks an administrator attempting to delete their own
// account (rejected before any transaction runs).
var ErrSelfDelete = errors.New("auth: admins cannot delete their own account")

// ErrLastAdmin marks a deletion that would leave the system without any
// administrator. Enforced inside the deletion transaction, so concurrent
// deletions of the final admins cannot both succeed.
var ErrLastAdmin = errors.New("auth: cannot delete the last administrator account")

// User is the authenticated principal.
type User struct {
	ID        uuid.UUID
	Email     string
	FullName  string
	Role      string
	CreatedAt time.Time
}

// Session is a freshly issued bearer credential. Token is the plaintext
// bearer token — shown exactly once; only Hash is persisted.
type Session struct {
	Token     string
	Hash      string
	User      User
	ExpiresAt time.Time
}

// Repository is the persistence port (impl: internal/store).
type Repository interface {
	// UserByEmail matches on lower(trim(email)) — the canonical form used
	// everywhere else in the system.
	UserByEmail(ctx context.Context, normalizedEmail string) (User, error)
	UserByID(ctx context.Context, id uuid.UUID) (User, error)
	// CredentialHash returns the stored bcrypt hash; ErrNoCredential when the
	// user cannot log in with a password.
	CredentialHash(ctx context.Context, userID uuid.UUID) (string, error)
	// CreateUser inserts a user; the caller validates role and email.
	CreateUser(ctx context.Context, email, fullName, role string) (User, error)
	// CreateUserWithCredential inserts the user AND its password hash in one
	// transaction — a partial "user without credential" state can never
	// survive a failure (which would otherwise brick the admin bootstrap).
	CreateUserWithCredential(ctx context.Context, email, fullName, role, passwordHash string) (User, error)
	ListUsers(ctx context.Context) ([]User, error)
	DeleteUser(ctx context.Context, id uuid.UUID) (bool, error)
	CountRole(ctx context.Context, role string) (int, error)

	InsertSession(ctx context.Context, userID uuid.UUID, credentialHash, tokenHash string, expiresAt time.Time) error
	// SessionByToken returns the session's user when the token hash exists,
	// is unrevoked, and unexpired; otherwise ErrInvalidCredentials. Touches
	// last_used_at on success.
	SessionByToken(ctx context.Context, tokenHash string) (User, time.Time, error)
	RevokeSession(ctx context.Context, tokenHash string) error
	// RotateSession atomically swaps one active session for a fresh one:
	// within a single transaction it locks the old session row, verifies it
	// is still unrevoked and unexpired, revokes it, and inserts the new
	// token hash. Concurrent rotations of the same token serialize on the
	// row lock — exactly one can succeed; the loser gets
	// ErrInvalidCredentials.
	RotateSession(ctx context.Context, oldTokenHash, newTokenHash string, expiresAt time.Time) (User, error)
	// ReplaceCredential updates the password hash AND revokes every active
	// session of the user in one transaction: after a reset, tokens issued
	// under the old password must stop working immediately (or not at all).
	ReplaceCredential(ctx context.Context, userID uuid.UUID, passwordHash string) error
	// DeleteUserProtected deletes a user account, enforcing — inside the
	// same serialized transaction — that an admin never deletes their own
	// account (ErrSelfDelete) and that the last administrator survives
	// (ErrLastAdmin). Returns core NotFound via the caller's mapping when the
	// account does not exist.
	DeleteUserProtected(ctx context.Context, id, selfID uuid.UUID) error
}

// ErrNoCredential marks a user without a password (e.g. ingested students).
var ErrNoCredential = errors.New("auth: no credential for user")

// MinPasswordLength — deliberately modest for operator-created accounts;
// admins create accounts, users change them. MaxPasswordLength is bcrypt's
// hard input limit (72 bytes) — longer inputs are rejected, not truncated.
const (
	MinPasswordLength = 12
	MaxPasswordLength = 72
)

// dummyHash equalizes login latency between "user exists" and "user does
// not" paths so response time cannot enumerate accounts.
var dummyHash = func() string {
	hash, _ := bcrypt.GenerateFromPassword([]byte("launchpad-timing-equalizer"), bcrypt.DefaultCost)
	return string(hash)
}()

var emailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// Service performs authentication and account management.
type Service struct {
	Repo       Repository
	SessionTTL time.Duration
}

// NewService builds the service. ttl <= 0 falls back to 12h.
func NewService(repo Repository, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &Service{Repo: repo, SessionTTL: ttl}
}

func normalize(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// NormalizeEmail is the canonical email form used for identity matching
// (same rule as core/integrations.NormalizeEmail).
func NormalizeEmail(email string) string {
	return normalize(email)
}

// ValidateEmail checks the canonical email form.
func ValidateEmail(email string) bool {
	e := normalize(email)
	return e != "" && len(e) <= 254 && emailPattern.MatchString(e)
}

// ValidRole reports whether the role exists in the PRD role model.
func ValidRole(role string) bool {
	switch role {
	case RoleAdmin, RoleCoordinator, RoleLecturer, RoleTA, RoleStudent:
		return true
	}
	return false
}

// Login verifies email+password and issues a session token. Unknown email,
// missing credential, and wrong password are indistinguishable; database and
// infrastructure failures surface as service errors instead (never as a
// misleading "invalid credentials").
func (s *Service) Login(ctx context.Context, email, password string) (Session, error) {
	email = normalize(email)
	if email == "" || password == "" {
		bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password)) //nolint:errcheck — timing equalization only
		return Session{}, ErrInvalidCredentials
	}
	user, err := s.Repo.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password)) //nolint:errcheck — timing equalization only
			return Session{}, ErrInvalidCredentials                            // never reveal whether the email exists
		}
		return Session{}, fmt.Errorf("auth: login lookup: %w", err) // infrastructure failure — NOT invalid credentials
	}
	hash, err := s.Repo.CredentialHash(ctx, user.ID)
	if err != nil {
		if errors.Is(err, ErrNoCredential) {
			bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password)) //nolint:errcheck — timing equalization only
			return Session{}, ErrInvalidCredentials
		}
		return Session{}, fmt.Errorf("auth: login credential: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return Session{}, ErrInvalidCredentials
	}
	return s.issue(ctx, user, hash)
}

// issue creates a session row and returns the one-time token.
func (s *Service) issue(ctx context.Context, user User, credentialHash string) (Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, fmt.Errorf("auth: generate token: %w", err)
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	expires := time.Now().Add(s.SessionTTL)
	if err := s.Repo.InsertSession(ctx, user.ID, credentialHash, hash, expires); err != nil {
		return Session{}, fmt.Errorf("auth: insert session: %w", err)
	}
	return Session{Token: token, Hash: hash, User: user, ExpiresAt: expires}, nil
}

// Authenticate resolves a bearer token to its user, or fails closed.
func (s *Service) Authenticate(ctx context.Context, token string) (User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return User{}, ErrInvalidCredentials
	}
	sum := sha256.Sum256([]byte(token))
	user, _, err := s.Repo.SessionByToken(ctx, hex.EncodeToString(sum[:]))
	if err != nil {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

// Logout revokes the session bound to the token (idempotent).
func (s *Service) Logout(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(token))
	return s.Repo.RevokeSession(ctx, hex.EncodeToString(sum[:]))
}

// Refresh rotates a still-valid token: revoking the old session and issuing
// the replacement happens in ONE database transaction, so two concurrent
// refreshes with the same token cannot both succeed (the loser receives
// ErrInvalidCredentials). A revoked or expired token cannot be refreshed.
func (s *Service) Refresh(ctx context.Context, token string) (Session, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Session{}, ErrInvalidCredentials
	}
	// Pre-generate the successor so the transaction is a single round trip
	// of verify-revoke-insert.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, fmt.Errorf("auth: generate token: %w", err)
	}
	newToken := hex.EncodeToString(raw)
	newSum := sha256.Sum256([]byte(newToken))
	expires := time.Now().Add(s.SessionTTL)

	oldSum := sha256.Sum256([]byte(token))
	user, err := s.Repo.RotateSession(ctx, hex.EncodeToString(oldSum[:]), hex.EncodeToString(newSum[:]), expires)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return Session{}, ErrInvalidCredentials
		}
		return Session{}, fmt.Errorf("auth: rotate session: %w", err)
	}
	return Session{Token: newToken, Hash: hex.EncodeToString(newSum[:]), User: user, ExpiresAt: expires}, nil
}

// HashPassword hashes with the bcrypt default cost (used by account
// management and the admin bootstrap). bcrypt accepts at most 72 bytes.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", core.Validation("password too short", map[string]string{
			"password": fmt.Sprintf("must contain at least %d characters", MinPasswordLength),
		})
	}
	if len(password) > MaxPasswordLength {
		return "", core.Validation("password too long", map[string]string{
			"password": fmt.Sprintf("must contain at most %d characters (bcrypt limit)", MaxPasswordLength),
		})
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	return string(hash), nil
}

// CreateUserWithPassword creates a user and credential in ONE transaction:
// either both exist or neither does (a partial state would brick the admin
// bootstrap with no recoverable path).
func (s *Service) CreateUserWithPassword(ctx context.Context, email, fullName, role, password string) (User, error) {
	email = normalize(email)
	if !ValidateEmail(email) {
		return User{}, core.Validation("invalid user data", map[string]string{"email": "must be a valid email address"})
	}
	if !ValidRole(role) {
		return User{}, core.Validation("invalid user data", map[string]string{"role": "unknown role: " + role})
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	return s.Repo.CreateUserWithCredential(ctx, email, strings.TrimSpace(fullName), role, hash)
}

// SetPassword replaces a user's password and revokes every active session
// for that user in one transaction: tokens issued before the reset stop
// authenticating immediately, and the new password works on the next login.
func (s *Service) SetPassword(ctx context.Context, id uuid.UUID, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.Repo.ReplaceCredential(ctx, id, hash); err != nil {
		return fmt.Errorf("auth: set password: %w", err)
	}
	return nil
}

// DeleteAccount removes a user account. The self-deletion and last-admin
// protections are enforced inside the deletion transaction, so concurrent
// administrator deletions serialize and the final admin always survives.
func (s *Service) DeleteAccount(ctx context.Context, id, selfID uuid.UUID) error {
	return s.Repo.DeleteUserProtected(ctx, id, selfID)
}

// BootstrapAdmin creates the initial admin account when — and only when — no
// admin exists and both email and password are supplied. It never overwrites
// an existing admin and never stores the plaintext password.
func (s *Service) BootstrapAdmin(ctx context.Context, email, password string) (created bool, err error) {
	email = normalize(email)
	if email == "" || password == "" {
		return false, nil
	}
	count, err := s.Repo.CountRole(ctx, RoleAdmin)
	if err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	if _, err := s.CreateUserWithPassword(ctx, email, "Administrator", RoleAdmin, password); err != nil {
		// A concurrent boot may have created the admin between the count and
		// the insert; treat a duplicate email as "already bootstrapped".
		var appErr *core.AppError
		if errors.As(err, &appErr) && (appErr.Code == core.CodeDuplicate || appErr.Code == core.CodeConflict) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
