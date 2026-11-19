package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

func (r *Repo) SaveAudit(ctx context.Context, jobID int64, snapshot audit.Snapshot, expected []audit.Resource) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO audit_logs (river_job_id, group_id, audit_type, expected_state, actual_state, differences)
		VALUES ($1,$2,$3,$4::jsonb,$5::jsonb,$6::jsonb)
		ON CONFLICT (river_job_id) DO NOTHING
	`, jobID, snapshot.GroupID, snapshot.Type, expected, snapshot.Results, snapshot.Issues)
	if err != nil {
		return fmt.Errorf("save group audit: %w", err)
	}
	return nil
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
