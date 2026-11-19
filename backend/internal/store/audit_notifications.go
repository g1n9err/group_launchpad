package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"launchpad/internal/core/audit"
)

func (r *Repo) AuditNotifications(ctx context.Context) ([]audit.Notification, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT n.id,a.group_id,g.name,a.created_at,n.changes,
		       CASE WHEN n.email_status='sending' AND n.updated_at < now() - interval '2 minutes'
		            THEN 'unknown' ELSE n.email_status END,
		       COALESCE(n.email_error,'')
		FROM audit_notifications n JOIN audit_logs a ON a.id=n.audit_id JOIN groups g ON g.id=a.group_id
		ORDER BY a.created_at DESC,n.id DESC LIMIT 50
	`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (audit.Notification, error) {
		var n audit.Notification
		err := row.Scan(&n.ID, &n.GroupID, &n.GroupName, &n.CheckedAt, &n.Changes, &n.EmailStatus, &n.EmailError)
		return n, err
	})
}

func (r *Repo) CurrentAuditIssues(ctx context.Context) ([]audit.OpenIssues, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT g.id,g.name,latest.created_at,latest.differences
		FROM groups g JOIN LATERAL (
		    SELECT created_at,differences FROM audit_logs
		    WHERE group_id=g.id AND river_job_id IS NOT NULL
		    ORDER BY created_at DESC,id DESC LIMIT 1
		) latest ON true
		WHERE g.status <> 'archived' AND latest.differences <> '[]'::jsonb
		ORDER BY latest.created_at DESC,g.id LIMIT 50
	`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (audit.OpenIssues, error) {
		var issues audit.OpenIssues
		err := row.Scan(&issues.GroupID, &issues.GroupName, &issues.CheckedAt, &issues.Issues)
		return issues, err
	})
}

func (r *Repo) StartAuditEmail(ctx context.Context, id uuid.UUID) (audit.Notification, bool, error) {
	var n audit.Notification
	err := r.pool.QueryRow(ctx, `
		UPDATE audit_notifications n SET email_status='sending',updated_at=now()
		FROM audit_logs a JOIN groups g ON g.id=a.group_id
		WHERE n.id=$1 AND n.audit_id=a.id AND n.email_status='pending' AND g.status <> 'archived'
		RETURNING n.id,a.group_id,g.name,a.created_at,n.changes
	`, id).Scan(&n.ID, &n.GroupID, &n.GroupName, &n.CheckedAt, &n.Changes)
	if errors.Is(err, pgx.ErrNoRows) {
		return n, false, nil
	}
	return n, err == nil, err
}

func (r *Repo) FinishAuditEmail(ctx context.Context, id uuid.UUID, status, message string) error {
	_, err := r.pool.Exec(ctx, `UPDATE audit_notifications
		SET email_status=$2,email_error=NULLIF($3,''),updated_at=now()
		WHERE id=$1 AND email_status='sending'`, id, status, message)
	return err
}
