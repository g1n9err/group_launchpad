package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	gen "launchpad/internal/transport/gen"
)

// StaffEmailsFor returns the emails to invite/grant access for a group
// (PRD §4.1, §4.2): per-group staff_assignments first, then the
// admin-managed predefined_staff list. The STAFF_EMAILS env var remains the
// final fallback, applied by the caller (worker) when this returns nothing.
// Implements jobs.StaffAssignmentReader.
func (r *Repo) StaffEmailsFor(ctx context.Context, groupID uuid.UUID) ([]string, error) {
	staff, err := r.GroupStaff(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("store: staff emails for: %w", err)
	}
	emails := make([]string, 0, len(staff))
	seen := make(map[string]bool, len(staff))
	for _, member := range staff {
		email := string(member.Email)
		if !seen[email] {
			emails = append(emails, email)
			seen[email] = true
		}
	}
	if len(emails) > 0 {
		return emails, nil
	}
	// No per-group staff: fall back to the predefined list (PRD §4.2).
	return r.predefinedStaffEmailsForGroup(ctx, groupID)
}

func (r *Repo) predefinedStaffEmailsForGroup(ctx context.Context, groupID uuid.UUID) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT s.email FROM predefined_staff s JOIN groups g ON g.workspace_owner_id IS NOT DISTINCT FROM s.workspace_owner_id WHERE g.id=$1 ORDER BY s.created_at,s.id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("store: predefined staff emails for group: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		out = append(out, email)
	}
	return out, rows.Err()
}

func (r *Repo) GroupStaff(ctx context.Context, groupID uuid.UUID) ([]gen.GroupOnboardingStaff, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT role, lower(btrim(email)) AS email FROM staff_assignments WHERE group_id=$1
		UNION
		SELECT gm.member_role, lower(btrim(u.email))
		FROM group_members gm JOIN users u ON u.id=gm.user_id
		WHERE gm.group_id=$1 AND gm.member_role IN ('teaching_staff', 'lecturer', 'ta')
		ORDER BY role, email
	`, groupID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (gen.GroupOnboardingStaff, error) {
		var staff gen.GroupOnboardingStaff
		err := row.Scan(&staff.Role, &staff.Email)
		return staff, err
	})
}
