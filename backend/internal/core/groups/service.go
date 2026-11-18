// Package groups contains group-management business rules. Pure Go: no HTTP,
// no SQL, no external SDKs. Persistence goes through the Repository interface
// (implemented by internal/store); validation rules live here, once.
package groups

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"launchpad/internal/core"
)

const (
	nameMaxLen = 200
	dateFormat = "2006-01-02"

	// StatusDraft is the initial lifecycle state of every group (PRD 4.1).
	StatusDraft = "draft"
)

// Group is the domain representation of a student group.
type Group struct {
	ID         uuid.UUID
	Name       string
	CourseName string
	StartDate  time.Time
	Status     string
	CreatedAt  time.Time
}

// CreateInput carries raw (string) input from the transport layer; the service
// owns parsing and every validation rule.
type CreateInput struct {
	Name       string
	CourseName string
	StartDate  string // ISO date, YYYY-MM-DD
}

// Repository is the persistence port for groups, implemented by internal/store.
type Repository interface {
	Create(ctx context.Context, name, courseName string, startDate time.Time) (Group, error)
	GetByID(ctx context.Context, id uuid.UUID) (Group, error)
	List(ctx context.Context, limit, offset int) (items []Group, total int, err error)
}

// Service applies group business rules on top of a Repository.
type Service struct {
	repo Repository
}

// New builds a groups Service.
func New(repo Repository) *Service { return &Service{repo: repo} }

// Create validates input and persists a new group. Duplicate names surface as
// core.AppError{DUPLICATE_GROUP}; the database unique constraint is the guard,
// which is race-safe by design.
func (s *Service) Create(ctx context.Context, in CreateInput) (Group, error) {
	name := strings.TrimSpace(in.Name)
	course := strings.TrimSpace(in.CourseName)

	fieldErrors := map[string]string{}
	if name == "" {
		fieldErrors["name"] = "is required"
	} else if len(name) > nameMaxLen {
		fieldErrors["name"] = fmt.Sprintf("must be at most %d characters", nameMaxLen)
	}
	if course == "" {
		fieldErrors["courseName"] = "is required"
	} else if len(course) > nameMaxLen {
		fieldErrors["courseName"] = fmt.Sprintf("must be at most %d characters", nameMaxLen)
	}
	var startDate time.Time
	if strings.TrimSpace(in.StartDate) == "" {
		fieldErrors["startDate"] = "is required"
	} else {
		parsed, err := time.Parse(dateFormat, in.StartDate) // rejects 2026-02-30, wrong shapes
		if err != nil {
			fieldErrors["startDate"] = "must be a valid date (YYYY-MM-DD)"
		} else {
			startDate = parsed
		}
	}
	if len(fieldErrors) > 0 {
		return Group{}, core.Validation("invalid group data", fieldErrors)
	}

	g, err := s.repo.Create(ctx, name, course, startDate)
	if err != nil {
		return Group{}, err
	}
	return g, nil
}

// Get returns one group by ID; invalid UUIDs and unknown IDs are domain errors.
func (s *Service) Get(ctx context.Context, rawID string) (Group, error) {
	id, err := uuid.Parse(strings.TrimSpace(rawID))
	if err != nil {
		return Group{}, core.Validation("invalid group id", map[string]string{"groupId": "must be a UUID"})
	}
	g, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Group{}, err
	}
	return g, nil
}

// List returns a page of groups (newest first) with clamped pagination.
func (s *Service) List(ctx context.Context, limit, offset int) ([]Group, int, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.List(ctx, limit, offset)
}
