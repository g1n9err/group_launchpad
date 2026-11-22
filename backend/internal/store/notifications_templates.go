// Store implementations for notifications (PRD §4.9) and announcement
// templates (PRD §4.8), backed by migration 0014 tables.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"launchpad/internal/transport"
)

// ── NotificationStore ──────────────────────────────────────────────────────

func (r *Repo) InsertNotification(ctx context.Context, groupID *uuid.UUID, kind, severity, title, detail string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO notifications (group_id, kind, severity, title, detail) VALUES ($1, $2, $3, $4, $5)`,
		groupID, kind, severity, title, detail)
	if err != nil {
		return fmt.Errorf("store: insert notification: %w", err)
	}
	return nil
}

// InsertNotificationOnce persists a worker notification at most once per
// source event, even when River retries after a process interruption.
func (r *Repo) InsertNotificationOnce(ctx context.Context, groupID *uuid.UUID, kind, severity, title, detail, dedupeKey string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO notifications (group_id, kind, severity, title, detail, dedupe_key)
		 VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (dedupe_key) DO NOTHING`,
		groupID, kind, severity, title, detail, dedupeKey)
	if err != nil {
		return fmt.Errorf("store: insert notification once: %w", err)
	}
	return nil
}

func (r *Repo) ListNotifications(ctx context.Context, limit, offset int) ([]transport.NotificationRow, int, int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, group_id, kind, severity, title, detail, read_at, created_at
		 FROM notifications WHERE ($3::uuid IS NULL OR workspace_owner_id=$3) ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset, nullableOwnerFromContext(ctx))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("store: list notifications: %w", err)
	}
	defer rows.Close()
	var out []transport.NotificationRow
	for rows.Next() {
		var n transport.NotificationRow
		var gid *uuid.UUID
		if err := rows.Scan(&n.ID, &gid, &n.Kind, &n.Severity, &n.Title, &n.Detail, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, 0, 0, fmt.Errorf("store: scan notification: %w", err)
		}
		n.GroupID = gid
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}
	var total int
	owner := nullableOwnerFromContext(ctx)
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE ($1::uuid IS NULL OR workspace_owner_id=$1)`, owner).Scan(&total); err != nil {
		return nil, 0, 0, err
	}
	var unread int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE read_at IS NULL AND ($1::uuid IS NULL OR workspace_owner_id=$1)`, owner).Scan(&unread); err != nil {
		return nil, 0, 0, fmt.Errorf("store: count unread notifications: %w", err)
	}
	return out, total, unread, nil
}

func (r *Repo) MarkNotificationRead(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE notifications SET read_at = now() WHERE id = $1 AND read_at IS NULL AND ($2::uuid IS NULL OR workspace_owner_id=$2)`, id, nullableOwnerFromContext(ctx))
	if err != nil {
		return false, fmt.Errorf("store: mark notification read: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) MarkAllNotificationsRead(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE read_at IS NULL AND ($1::uuid IS NULL OR workspace_owner_id=$1)`, nullableOwnerFromContext(ctx))
	if err != nil {
		return fmt.Errorf("store: mark all read: %w", err)
	}
	return nil
}

// DeleteNotification removes one coordinator notification permanently.
func (r *Repo) DeleteNotification(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM notifications WHERE id = $1 AND ($2::uuid IS NULL OR workspace_owner_id=$2)`, id, nullableOwnerFromContext(ctx))
	if err != nil {
		return false, fmt.Errorf("store: delete notification: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) DeleteAllNotifications(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM notifications WHERE ($1::uuid IS NULL OR workspace_owner_id=$1)`, nullableOwnerFromContext(ctx))
	if err != nil {
		return 0, fmt.Errorf("store: delete all notifications: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ── TemplateStore ──────────────────────────────────────────────────────────

func (r *Repo) CreateTemplate(ctx context.Context, name, body, kind string) (transport.TemplateRow, error) {
	var t transport.TemplateRow
	err := r.pool.QueryRow(ctx,
		`INSERT INTO announcement_templates (name, body, kind, workspace_owner_id)
		 VALUES ($1, $2, $3, $4) RETURNING id, name, body, kind, created_at, updated_at`,
		name, body, kind, nullableOwnerFromContext(ctx)).Scan(&t.ID, &t.Name, &t.Body, &t.Kind, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return transport.TemplateRow{}, fmt.Errorf("store: create template: %w", err)
	}
	return t, nil
}

func (r *Repo) ListTemplates(ctx context.Context) ([]transport.TemplateRow, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, body, kind, created_at, updated_at FROM announcement_templates WHERE ($1::uuid IS NULL OR workspace_owner_id=$1) ORDER BY name`, nullableOwnerFromContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("store: list templates: %w", err)
	}
	defer rows.Close()
	var out []transport.TemplateRow
	for rows.Next() {
		var t transport.TemplateRow
		if err := rows.Scan(&t.ID, &t.Name, &t.Body, &t.Kind, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) GetTemplate(ctx context.Context, id uuid.UUID) (transport.TemplateRow, error) {
	var t transport.TemplateRow
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, body, kind, created_at, updated_at FROM announcement_templates WHERE id = $1 AND ($2::uuid IS NULL OR workspace_owner_id=$2)`,
		id, nullableOwnerFromContext(ctx)).Scan(&t.ID, &t.Name, &t.Body, &t.Kind, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transport.TemplateRow{}, transport.ErrTemplateNotFound
		}
		return transport.TemplateRow{}, fmt.Errorf("store: get template: %w", err)
	}
	return t, nil
}

func (r *Repo) UpdateTemplate(ctx context.Context, id uuid.UUID, name, body, kind string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE announcement_templates SET name = $2, body = $3, kind = $4, updated_at = now() WHERE id = $1 AND ($5::uuid IS NULL OR workspace_owner_id=$5)`,
		id, name, body, kind, nullableOwnerFromContext(ctx))
	if err != nil {
		return fmt.Errorf("store: update template: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return transport.ErrTemplateNotFound
	}
	return nil
}

func (r *Repo) DeleteTemplate(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM announcement_templates WHERE id = $1 AND ($2::uuid IS NULL OR workspace_owner_id=$2)`, id, nullableOwnerFromContext(ctx))
	if err != nil {
		return false, fmt.Errorf("store: delete template: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
