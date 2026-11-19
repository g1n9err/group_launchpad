package store

import (
	"context"
	"errors"
	"fmt"

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
	return audit.Inventory{GroupName: group.Name, Resources: resources}, nil
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
		return uuid.Nil, tx.Commit(ctx)
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
	return notificationID, tx.Commit(ctx)
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
