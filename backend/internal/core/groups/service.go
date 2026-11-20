// Package groups contains group-management business rules. Pure Go: no HTTP,
// no SQL, no external SDKs. Persistence goes through the Repository interface
// (implemented by internal/store); validation rules live here, once.
package groups

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"launchpad/internal/core"
	"launchpad/internal/core/integrations"
)

const (
	nameMaxLen = 200
	dateFormat = "2006-01-02"

	// StatusDraft is the initial lifecycle state of every group (PRD 4.1).
	StatusDraft = "draft"

	// maxStudentEmails bounds one ingestion batch (sanity, not a PRD limit).
	maxStudentEmails = 500
)

// emailPattern is a pragmatic RFC-ish check after normalization: one @,
// plausible domain. Deliberately permissive — the normalized-unique index
// and Slack email matching are the real correctness mechanisms.
var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

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

	// StudentEmails is optional (backward compatible). Raw as submitted;
	// normalized (lower/trim) here — the same canonical form used by the
	// Slack identity linker (core/integrations.NormalizeEmail).
	StudentEmails []string

	// StaffEmails: per-group teaching staff (PRD §4.1 Multi-select Required).
	// LecturerEmails: per-group lecturers (PRD §4.1 Multi-select Required).
	// TAEmails: per-group teaching assistants (PRD §4.1 Multi-select Optional).
	// All normalized like StudentEmails.
	StaffEmails    []string
	LecturerEmails []string
	TAEmails       []string
}

// CreateParams is the validated command handed to the Repository.
type CreateParams struct {
	Name           string
	CourseName     string
	StartDate      time.Time
	StudentEmails  []string // normalized (lower/trim)
	StaffEmails    []string // teaching staff (PRD §4.1)
	LecturerEmails []string // lecturers (PRD §4.1)
	TAEmails       []string // TAs (PRD §4.1, optional)
}

// Repository is the persistence port for groups, implemented by internal/store.
type Repository interface {
	// Create persists the group AND its student memberships atomically:
	// a failure anywhere rolls the whole thing back (no partial groups).
	Create(ctx context.Context, params CreateParams) (Group, error)
	GetByID(ctx context.Context, id uuid.UUID) (Group, error)
	List(ctx context.Context, limit, offset int) (items []Group, total int, err error)
	// StudentRoster returns the group's student members with their onboarding
	// status in ONE query (no N+1).
	StudentRoster(ctx context.Context, groupID uuid.UUID) ([]Student, error)
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
	fieldErrors := map[string]string{}
	startDate, name, course := validateGroupBasics(in, fieldErrors)
	studentEmails, emailErr := normalizeStudentEmails(in.StudentEmails)
	if emailErr != "" {
		fieldErrors["studentEmails"] = emailErr
	}
	staffEmails, _ := normalizeStaffEmails(in.StaffEmails, fieldErrors, "staffEmails")
	lecturerEmails, _ := normalizeStaffEmails(in.LecturerEmails, fieldErrors, "lecturerEmails")
	taEmails, _ := normalizeStaffEmails(in.TAEmails, fieldErrors, "taEmails")

	// PRD §4.1: teaching staff and lecturers are Required, TAs are optional.
	if len(in.StaffEmails) == 0 && len(fieldErrors["staffEmails"]) == 0 {
		fieldErrors["staffEmails"] = "is required (PRD 4.1)"
	}
	if len(in.LecturerEmails) == 0 && len(fieldErrors["lecturerEmails"]) == 0 {
		fieldErrors["lecturerEmails"] = "is required (PRD 4.1)"
	}

	// Cross-category duplicates: the same person in multiple staff roles is
	// rejected — the DB stores one role per email per group (UNIQUE
	// constraint). Categories are scanned in a FIXED order (never a map —
	// map iteration would make the reported field non-deterministic), and
	// the duplicate is always reported against the LATER category.
	seenRoles := make(map[string]string) // email → first category
	for _, cat := range []string{"staffEmails", "lecturerEmails", "taEmails"} {
		var emails []string
		switch cat {
		case "staffEmails":
			emails = staffEmails
		case "lecturerEmails":
			emails = lecturerEmails
		case "taEmails":
			emails = taEmails
		}
		for _, e := range emails {
			if prevCat, dup := seenRoles[e]; dup {
				if fieldErrors[cat] == "" {
					fieldErrors[cat] = fmt.Sprintf("%s already assigned as %s", e, prevCat)
				}
			} else {
				seenRoles[e] = cat
			}
		}
	}

	if len(fieldErrors) > 0 {
		return Group{}, core.Validation("invalid group data", fieldErrors)
	}

	g, err := s.repo.Create(ctx, CreateParams{
		Name:           name,
		CourseName:     course,
		StartDate:      startDate,
		StudentEmails:  studentEmails,
		StaffEmails:    staffEmails,
		LecturerEmails: lecturerEmails,
		TAEmails:       taEmails,
	})
	if err != nil {
		return Group{}, err
	}
	return g, nil
}

// normalizeStaffEmails canonicalizes staff/lecturer/TA emails using the same
// normalization as students. Validation errors go into fieldErrors under the
// given field name. TA emails are optional and can be empty.
func normalizeStaffEmails(raw []string, fieldErrors map[string]string, field string) ([]string, string) {
	if len(raw) == 0 {
		return nil, ""
	}
	if len(raw) > maxStudentEmails {
		fieldErrors[field] = fmt.Sprintf("too many emails (max %d)", maxStudentEmails)
		return nil, ""
	}
	seen := map[string]bool{}
	var invalid []string
	var duplicates []string
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		n := integrations.NormalizeEmail(r)
		if n == "" || !emailPattern.MatchString(n) {
			invalid = append(invalid, strings.TrimSpace(r))
			continue
		}
		if seen[n] {
			duplicates = append(duplicates, n)
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	switch {
	case len(invalid) > 0:
		fieldErrors[field] = "invalid email format: " + strings.Join(invalid, ", ")
		return nil, ""
	case len(duplicates) > 0:
		fieldErrors[field] = "duplicate email addresses: " + strings.Join(duplicates, ", ")
		return nil, ""
	}
	return out, ""
}

// validateGroupBasics checks name/course/date; returns the parsed date and
// the trimmed name/course.
func validateGroupBasics(in CreateInput, fieldErrors map[string]string) (time.Time, string, string) {
	name := strings.TrimSpace(in.Name)
	course := strings.TrimSpace(in.CourseName)
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
	return startDate, name, course
}

// normalizeStudentEmails canonicalizes every address (single source of truth:
// integrations.NormalizeEmail — the same form the Slack identity linker
// matches on), rejects malformed entries and post-normalization duplicates.
// Nothing is silently discarded: problems are reported verbatim to the client.
func normalizeStudentEmails(raw []string) ([]string, string) {
	if len(raw) == 0 {
		return nil, ""
	}
	if len(raw) > maxStudentEmails {
		return nil, fmt.Sprintf("too many student emails (max %d)", maxStudentEmails)
	}
	seen := map[string]bool{}
	var invalid []string
	var duplicates []string
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		n := integrations.NormalizeEmail(r)
		if n == "" || !emailPattern.MatchString(n) {
			invalid = append(invalid, strings.TrimSpace(r))
			continue
		}
		if seen[n] {
			duplicates = append(duplicates, n)
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	switch {
	case len(invalid) > 0:
		return nil, "invalid email format: " + strings.Join(invalid, ", ")
	case len(duplicates) > 0:
		return nil, "duplicate email addresses: " + strings.Join(duplicates, ", ")
	}
	return out, ""
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
