package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// StaffEmailsFor returns the emails to invite/grant access for a group
// (PRD §4.1, §4.2): per-group staff_assignments first, then the
// admin-managed predefined_staff list. The STAFF_EMAILS env var remains the
// final fallback, applied by the caller (worker) when this returns nothing.
// Implements jobs.StaffAssignmentReader.
func (r *Repo) StaffEmailsFor(ctx context.Context, groupID uuid.UUID) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT email FROM staff_assignments WHERE group_id = $1 ORDER BY role, email`,
		groupID)
	if err != nil {
		return nil, fmt.Errorf("store: staff emails for: %w", err)
	}
	defer rows.Close()
	var emails []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, fmt.Errorf("store: scan staff email: %w", err)
		}
		emails = append(emails, email)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(emails) > 0 {
		return emails, nil
	}
	// No per-group staff: fall back to the predefined list (PRD §4.2).
	return r.PredefinedStaffEmails(ctx)
}
