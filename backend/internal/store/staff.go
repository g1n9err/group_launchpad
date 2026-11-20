// Store implementation for predefined staff (PRD §3.2, §4.2) and
// group-membership checks used by role-based authorization.
package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"launchpad/internal/core"
	"launchpad/internal/core/groups"
	"launchpad/internal/transport"
)

// PredefinedStaffRow is the transport-level predefined staff model.
// (Defined in transport; store implements transport.StaffAdminStore.)

func (r *Repo) ListPredefinedStaff(ctx context.Context) ([]transport.PredefinedStaffRow, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, email, full_name, created_at FROM predefined_staff ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list predefined staff: %w", err)
	}
	defer rows.Close()
	out := []transport.PredefinedStaffRow{}
	for rows.Next() {
		var s transport.PredefinedStaffRow
		if err := rows.Scan(&s.ID, &s.Email, &s.FullName, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan predefined staff: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) CreatePredefinedStaff(ctx context.Context, email, fullName string) (transport.PredefinedStaffRow, error) {
	var s transport.PredefinedStaffRow
	err := r.pool.QueryRow(ctx,
		`INSERT INTO predefined_staff (email, full_name) VALUES ($1, $2)
		 RETURNING id, email, full_name, created_at`,
		email, fullName).Scan(&s.ID, &s.Email, &s.FullName, &s.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return transport.PredefinedStaffRow{}, &core.AppError{Code: core.CodeConflict, Message: "this staff member is already predefined"}
		}
		return transport.PredefinedStaffRow{}, fmt.Errorf("store: create predefined staff: %w", err)
	}
	return s, nil
}

func (r *Repo) DeletePredefinedStaff(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM predefined_staff WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("store: delete predefined staff: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// PredefinedStaffEmails returns the admin-managed global staff list.
func (r *Repo) PredefinedStaffEmails(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT email FROM predefined_staff ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("store: predefined staff emails: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, fmt.Errorf("store: scan staff email: %w", err)
		}
		out = append(out, email)
	}
	return out, rows.Err()
}

// Staff member roles that grant group-scoped API access to lecturers and
// teaching assistants (PRD §3.3, §3.4). The values mirror the CHECK
// constraints of group_members.member_role and staff_assignments.role
// (migrations 0001 and 0014). Deliberately EXCLUDED:
//   - 'student': the PRD grants students no API surface (§3.5) — a lecturer
//     who happens to also be a rostered student must not inherit roster
//     access through that membership;
//   - 'teaching_staff': the predefined operational staff (§4.2) is a
//     provisioning concept, not an API role — the PRD authorizes no API
//     access for it.
var staffAccessMemberRoles = []string{"lecturer", "ta"}

// IsGroupMember reports whether the user holds LECTURER or TA access to the
// group: a group_members row with member_role lecturer/ta for the user id,
// or a staff_assignments row with role lecturer/ta matching the user's
// normalized email (PRD §4.1). Student and teaching-staff memberships never
// grant scoped API access. Implements transport.GroupMembershipChecker.
func (r *Repo) IsGroupMember(ctx context.Context, userID uuid.UUID, normalizedEmail string, groupID uuid.UUID) (bool, error) {
	var member bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM group_members
		    WHERE group_id = $1 AND user_id = $2 AND member_role = ANY($3)
		    UNION ALL
		    SELECT 1 FROM staff_assignments
		    WHERE group_id = $1 AND lower(btrim(email)) = $4 AND role = ANY($3)
		)`, groupID, userID, staffAccessMemberRoles, normalizedEmail).Scan(&member)
	if err != nil {
		return false, fmt.Errorf("store: is group member: %w", err)
	}
	return member, nil
}

// ListAssignedGroups returns the groups in which the user holds LECTURER or
// TA access (group_members by user id or staff_assignments by normalized
// email, both restricted to the staff-access roles), plus the total count
// for pagination. Student/teaching-staff memberships never surface here.
func (r *Repo) ListAssignedGroups(ctx context.Context, userID uuid.UUID, normalizedEmail string, limit, offset int) ([]groups.Group, int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT g.id, g.name, g.course_name, g.start_date, g.status, g.created_at
		FROM groups g
		WHERE g.id IN (
		    SELECT group_id FROM group_members WHERE user_id = $1 AND member_role = ANY($3)
		    UNION
		    SELECT group_id FROM staff_assignments WHERE lower(btrim(email)) = $2 AND role = ANY($3)
		)
		ORDER BY g.created_at DESC, g.id LIMIT $4 OFFSET $5`,
		userID, normalizedEmail, staffAccessMemberRoles, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list assigned groups: %w", err)
	}
	defer rows.Close()
	out := []groups.Group{}
	for rows.Next() {
		var g groups.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.CourseName, &g.StartDate, &g.Status, &g.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("store: scan assigned group: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM groups g WHERE g.id IN (
		    SELECT group_id FROM group_members WHERE user_id = $1 AND member_role = ANY($3)
		    UNION
		    SELECT group_id FROM staff_assignments WHERE lower(btrim(email)) = $2 AND role = ANY($3)
		)`, userID, normalizedEmail, staffAccessMemberRoles).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count assigned groups: %w", err)
	}
	return out, total, nil
}
