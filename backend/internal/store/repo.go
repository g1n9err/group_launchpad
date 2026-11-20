// Package store implements core persistence ports on PostgreSQL. SQL is
// compiled by sqlc into internal/store/gen; this package adapts the generated
// code to core interfaces and maps driver errors to domain errors.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"launchpad/internal/core"
	"launchpad/internal/core/groups"
	"launchpad/internal/store/gen"
)

// Compile-time proof that Repo satisfies the groups.Repository port.
var _ groups.Repository = (*Repo)(nil)

// Repo implements all persistence ports against one connection pool.
type Repo struct {
	pool *pgxpool.Pool
	q    *gen.Queries
}

// New builds a Repo over the given pool.
func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool, q: gen.New(pool)}
}

// Pool exposes the underlying pool (readiness ping, River wiring on Day 2).
func (r *Repo) Pool() *pgxpool.Pool { return r.pool }

// groups.Repository

func (r *Repo) Create(ctx context.Context, params groups.CreateParams) (groups.Group, error) {
	// One transaction: group + users + memberships commit together or not at
	// all — a failed student insertion can never leave a partial group.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return groups.Group{}, fmt.Errorf("store: create group tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck — no-op after commit

	qtx := r.q.WithTx(tx)
	row, err := qtx.CreateGroup(ctx, gen.CreateGroupParams{
		Name:       params.Name,
		CourseName: params.CourseName,
		StartDate:  params.StartDate,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return groups.Group{}, core.DuplicateGroup(params.Name)
		}
		return groups.Group{}, fmt.Errorf("store: create group: %w", err)
	}

	for _, email := range params.StudentEmails {
		userID, err := upsertStudentUser(ctx, qtx, email)
		if err != nil {
			return groups.Group{}, err
		}
		if _, err := qtx.InsertGroupMember(ctx, gen.InsertGroupMemberParams{
			GroupID: row.ID,
			UserID:  userID,
		}); err != nil {
			return groups.Group{}, fmt.Errorf("store: insert group member: %w", err)
		}
	}

	// Per-group staff assignments (PRD §4.1) — persisted atomically.
	for _, email := range params.StaffEmails {
		if err := insertStaffAssignment(ctx, tx, row.ID, email, "teaching_staff"); err != nil {
			return groups.Group{}, err
		}
	}
	for _, email := range params.LecturerEmails {
		if err := insertStaffAssignment(ctx, tx, row.ID, email, "lecturer"); err != nil {
			return groups.Group{}, err
		}
	}
	for _, email := range params.TAEmails {
		if err := insertStaffAssignment(ctx, tx, row.ID, email, "ta"); err != nil {
			return groups.Group{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return groups.Group{}, fmt.Errorf("store: create group commit: %w", err)
	}
	return groupFromRow(row), nil
}

func insertStaffAssignment(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, email, role string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO staff_assignments (group_id, email, role) VALUES ($1, $2, $3)
		 ON CONFLICT (group_id, email) DO NOTHING`,
		groupID, email, role)
	if err != nil {
		return fmt.Errorf("store: insert staff assignment: %w", err)
	}
	return nil
}

// upsertStudentUser reuses an existing user matched by normalized email
// (role and Slack identity untouched) or creates a 'student' user. The
// full_name is the email itself until the intake form supplies a real name —
// nothing is invented.
func upsertStudentUser(ctx context.Context, qtx *gen.Queries, normalizedEmail string) (uuid.UUID, error) {
	id, err := qtx.InsertUserIfAbsent(ctx, gen.InsertUserIfAbsentParams{
		FullName: normalizedEmail,
		Email:    normalizedEmail,
	})
	if err == nil {
		return id, nil
	}
	// ON CONFLICT DO NOTHING + RETURNING yields ErrNoRows when the row
	// existed (or a concurrent insert won the race); a bare unique violation
	// is also possible via other paths. Either way: reuse the existing user.
	if !errors.Is(err, pgx.ErrNoRows) && !isUniqueViolation(err) {
		return uuid.Nil, fmt.Errorf("store: insert user: %w", err)
	}
	id, err = qtx.FindUserIDByNormalizedEmail(ctx, normalizedEmail)
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: find existing user: %w", err)
	}
	return id, nil
}

func (r *Repo) GetByID(ctx context.Context, id uuid.UUID) (groups.Group, error) {
	row, err := r.q.GetGroup(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return groups.Group{}, core.NotFound("group", id.String())
		}
		return groups.Group{}, fmt.Errorf("store: get group %s: %w", id, err)
	}
	return groupFromRow(row), nil
}

func (r *Repo) List(ctx context.Context, limit, offset int) ([]groups.Group, int, error) {
	rows, err := r.q.ListGroups(ctx, gen.ListGroupsParams{Limit: int32(limit), Offset: int32(offset)})
	if err != nil {
		return nil, 0, fmt.Errorf("store: list groups: %w", err)
	}
	total, err := r.q.CountGroups(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("store: count groups: %w", err)
	}
	out := make([]groups.Group, len(rows))
	for i, row := range rows {
		out[i] = groupFromRow(row)
	}
	return out, int(total), nil
}

// StudentRoster returns the group's student roster in one query.
func (r *Repo) StudentRoster(ctx context.Context, groupID uuid.UUID) ([]groups.Student, error) {
	rows, err := r.q.ListStudentRoster(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("store: student roster: %w", err)
	}
	out := make([]groups.Student, len(rows))
	for i, row := range rows {
		out[i] = groups.Student{
			UserID:           row.UserID,
			FullName:         row.FullName,
			Email:            row.Email,
			OnboardingStatus: row.OnboardingStatus,
		}
	}
	return out, nil
}

// helper

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func groupFromRow(row gen.Group) groups.Group {
	return groups.Group{
		ID:         row.ID,
		Name:       row.Name,
		CourseName: row.CourseName,
		StartDate:  row.StartDate,
		Status:     row.Status,
		CreatedAt:  row.CreatedAt,
	}
}
