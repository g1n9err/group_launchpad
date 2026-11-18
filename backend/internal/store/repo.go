// Package store implements core persistence ports on PostgreSQL. SQL is
// compiled by sqlc into internal/store/gen; this package adapts the generated
// code to core interfaces and maps driver errors to domain errors.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

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

func (r *Repo) Create(ctx context.Context, name, courseName string, startDate time.Time) (groups.Group, error) {
	row, err := r.q.CreateGroup(ctx, gen.CreateGroupParams{
		Name:       name,
		CourseName: courseName,
		StartDate:  startDate,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return groups.Group{}, core.DuplicateGroup(name)
		}
		return groups.Group{}, fmt.Errorf("store: create group: %w", err)
	}
	return groupFromRow(row), nil
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
