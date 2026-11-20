package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"launchpad/internal/core"
	"launchpad/internal/core/audit"
)

func (r *Repo) AuditInventory(ctx context.Context, groupID uuid.UUID) (audit.Inventory, error) {
	group, err := r.GetByID(ctx, groupID)
	if err != nil {
		return audit.Inventory{}, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT provider, resource_type, CASE WHEN provider='google_classroom' THEN 'course' ELSE resource_name END,
		       external_resource_id
		FROM group_resources
		WHERE group_id=$1 AND status='active'
		  AND (provider,resource_type) IN (('slack','channel'),('google_classroom','course'),('google_drive','folder'))
		UNION ALL
		SELECT 'google_forms','form','intake',external_form_id FROM intake_forms
		WHERE group_id=$1 AND external_form_id IS NOT NULL
		  AND status IN ('created','distributed','closed')
		ORDER BY 1,2,3,4
	`, groupID)
	if err != nil {
		return audit.Inventory{}, err
	}
	resources, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (audit.Resource, error) {
		var resource audit.Resource
		err := row.Scan(&resource.Provider, &resource.Kind, &resource.Key, &resource.ID)
		return resource, err
	})
	if err != nil {
		return audit.Inventory{}, err
	}
	var root string
	for _, resource := range resources {
		if resource.Provider == "google_drive" && resource.Key == "root" {
			root = resource.ID
		}
	}
	for i := range resources {
		if resources[i].Provider == "google_drive" && resources[i].Key != "root" {
			resources[i].ParentID = root
		}
	}
	inventory := audit.Inventory{GroupName: group.Name, StartDate: group.StartDate, Resources: resources}

	// Per-group staff assignments for the membership audit (PRD §4.7).
	staffRows, err := r.pool.Query(ctx,
		`SELECT role, lower(btrim(email)) FROM staff_assignments WHERE group_id=$1 ORDER BY role, email`, groupID)
	if err != nil {
		return audit.Inventory{}, fmt.Errorf("store: audit staff assignments: %w", err)
	}
	members, err := pgx.CollectRows(staffRows, func(row pgx.CollectableRow) (audit.ExpectedMember, error) {
		var m audit.ExpectedMember
		err := row.Scan(&m.Role, &m.Email)
		return m, err
	})
	if err != nil {
		return audit.Inventory{}, err
	}
	inventory.Members = members

	// Student roster summary for the audit and the onboarding notification.
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE onboarding_status IN ('joined_slack','complete'))
		FROM group_members WHERE group_id=$1 AND member_role='student'`, groupID).
		Scan(&inventory.Roster.Expected, &inventory.Roster.Joined); err != nil {
		return audit.Inventory{}, fmt.Errorf("store: audit roster summary: %w", err)
	}
	return inventory, nil
}

func (r *Repo) AuditableGroupIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT g.id FROM groups g WHERE g.status <> 'archived'
		AND (g.status <> 'draft'
		  OR EXISTS (SELECT 1 FROM group_resources WHERE group_id=g.id)
		  OR EXISTS (SELECT 1 FROM intake_forms WHERE group_id=g.id)
		  OR EXISTS (SELECT 1 FROM automation_jobs WHERE group_id=g.id))
		ORDER BY g.id
	`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

func (r *Repo) SaveAudit(ctx context.Context, jobID int64, snapshot audit.Snapshot, expected []audit.Resource, emailEnabled bool) (uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	// Serialize snapshots for a group so simultaneous runs compare and
	// notify against the immediately preceding committed result.
	var locked uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT id FROM groups WHERE id=$1 FOR UPDATE", snapshot.GroupID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, core.NotFound("group", snapshot.GroupID.String())
		}
		return uuid.Nil, err
	}
	var previous []audit.Result
	err = tx.QueryRow(ctx, `
		SELECT actual_state FROM audit_logs WHERE group_id=$1 AND river_job_id IS NOT NULL
		ORDER BY created_at DESC,id DESC LIMIT 1
	`, snapshot.GroupID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}

	var auditID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO audit_logs (river_job_id, group_id, audit_type, expected_state, actual_state, differences, created_at)
		VALUES ($1,$2,$3,$4::jsonb,$5::jsonb,$6::jsonb,clock_timestamp())
		ON CONFLICT (river_job_id) DO NOTHING RETURNING id
	`, jobID, snapshot.GroupID, snapshot.Type, expected, snapshot.Results, snapshot.Issues).Scan(&auditID)
	if errors.Is(err, pgx.ErrNoRows) {
		var pending uuid.UUID
		err := tx.QueryRow(ctx, `SELECT n.id FROM audit_notifications n JOIN audit_logs a ON a.id=n.audit_id
		    WHERE a.river_job_id=$1 AND n.email_status='pending'`, jobID).Scan(&pending)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil
		}
		return pending, err
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("save group audit: %w", err)
	}
	changes := audit.Changes(previous, snapshot.Results)
	if len(changes) == 0 {
		// No inconsistencies — but the onboarding notification (PRD §4.9) is
		// independent of resource changes and must still be considered.
		return uuid.Nil, commitWithOnboarding(ctx, tx, jobID, snapshot.GroupID)
	}
	status := "disabled"
	if emailEnabled {
		status = "pending"
	}
	var notificationID uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO audit_notifications (audit_id, changes, email_status)
		VALUES ($1,$2::jsonb,$3) RETURNING id`, auditID, changes, status).Scan(&notificationID)
	if err != nil {
		return uuid.Nil, err
	}
	if !emailEnabled {
		notificationID = uuid.Nil
	}
	changeDetail, err := json.Marshal(changes)
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal audit changes: %w", err)
	}
	severity := "info"
	for _, change := range changes {
		if change.After != "ok" {
			severity = "warning"
			break
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO notifications (group_id, kind, severity, title, detail, dedupe_key)
		VALUES ($1, 'audit_inconsistency', $2, 'Resource audit detected changes', $3, $4)
		ON CONFLICT (dedupe_key) DO NOTHING`, snapshot.GroupID, severity, string(changeDetail), fmt.Sprintf("group_audit:%d", jobID))
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert audit notification: %w", err)
	}
	return notificationID, commitWithOnboarding(ctx, tx, jobID, snapshot.GroupID)
}

// commitWithOnboarding emits the PRD §4.9 "incomplete student onboarding"
// notification for audits that run at or after the group start date with
// students still pending, then commits. Aggregate counts only — student
// emails never enter the notification (PII-safe).
func commitWithOnboarding(ctx context.Context, tx pgx.Tx, jobID int64, groupID uuid.UUID) error {
	var startDate time.Time
	var expected, joined int
	err := tx.QueryRow(ctx, `
		SELECT g.start_date,
		       count(m.id),
		       count(m.id) FILTER (WHERE m.onboarding_status IN ('joined_slack','complete'))
		FROM groups g LEFT JOIN group_members m ON m.group_id = g.id AND m.member_role='student'
		WHERE g.id = $1
		GROUP BY g.start_date`, groupID).Scan(&startDate, &expected, &joined)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx) // group vanished mid-audit; audit row remains
	}
	if err != nil {
		return fmt.Errorf("onboarding summary for notification: %w", err)
	}
	if expected == 0 || joined >= expected || startDate.After(time.Now()) {
		return tx.Commit(ctx)
	}
	detail := fmt.Sprintf("%d of %d students have not joined Slack (group started %s)",
		expected-joined, expected, startDate.Format("2006-01-02"))
	_, err = tx.Exec(ctx, `INSERT INTO notifications (group_id, kind, severity, title, detail, dedupe_key)
		VALUES ($1, 'onboarding_incomplete', 'warning', 'Incomplete student onboarding', $2, $3)
		ON CONFLICT (dedupe_key) DO NOTHING`,
		groupID, detail, fmt.Sprintf("group_audit:%d:onboarding", jobID))
	if err != nil {
		return fmt.Errorf("insert onboarding notification: %w", err)
	}
	return tx.Commit(ctx)
}

func (r *Repo) RecentAudits(ctx context.Context, groupID uuid.UUID) ([]audit.Snapshot, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, group_id, audit_type, created_at, actual_state, differences
		FROM audit_logs WHERE group_id=$1 AND river_job_id IS NOT NULL
		ORDER BY created_at DESC,id DESC LIMIT 20
	`, groupID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (audit.Snapshot, error) {
		var result audit.Snapshot
		err := row.Scan(&result.ID, &result.GroupID, &result.Type, &result.CheckedAt, &result.Results, &result.Issues)
		return result, err
	})
}
