package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"launchpad/internal/core"
	"launchpad/internal/core/announcements"
)

const announcementColumns = `
	id, group_id, request_id, channel_id, channel_ids, channel_name, message_type, message_content, scheduled_at,
	CASE WHEN status='publishing' AND updated_at < now() - interval '2 minutes' THEN 'unknown' ELSE status END,
	published_at, COALESCE(external_message_id, ''),
	CASE WHEN status='publishing' AND updated_at < now() - interval '2 minutes'
	     THEN 'publication was interrupted; Slack acceptance is unknown' ELSE COALESCE(error_message,'') END
`

func readAnnouncement(row interface{ Scan(...any) error }) (announcements.Announcement, error) {
	var item announcements.Announcement
	err := row.Scan(&item.ID, &item.GroupID, &item.RequestID, &item.ChannelID, &item.ChannelIDs, &item.ChannelName,
		&item.Type, &item.Message, &item.ScheduledAt, &item.Status, &item.PublishedAt, &item.ExternalMessageID, &item.Error)
	return item, err
}

func (r *Repo) ScheduleAnnouncement(ctx context.Context, groupID uuid.UUID, input announcements.Schedule, enqueue func(pgx.Tx, uuid.UUID) error) (announcements.Announcement, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return announcements.Announcement{}, err
	}
	defer tx.Rollback(ctx)

	item, err := readAnnouncement(tx.QueryRow(ctx, `
		WITH target AS (
			SELECT array_agg(resource.external_resource_id ORDER BY resource.resource_name) AS ids
			FROM group_resources resource
			WHERE resource.group_id=$1 AND resource.provider='slack' AND resource.resource_type='channel'
			  AND resource.status='active' AND resource.external_resource_id<>''
			  AND ($4='general' OR resource.resource_name=$3)
		)
		INSERT INTO scheduled_announcements
		    (group_id, request_id, channel_id, channel_ids, channel_name, message_type, message_content, scheduled_at)
		SELECT $1, $2, target.ids[1], target.ids, CASE WHEN $4='general' THEN 'all' ELSE $3 END, $4, $5, $6
		FROM target JOIN groups g ON g.id=$1
		WHERE cardinality(target.ids)>0 AND g.status<>'archived' AND $6 > now()
		ON CONFLICT (group_id, request_id) DO NOTHING
		RETURNING `+announcementColumns, groupID, input.RequestID, input.ChannelName, input.Type, input.Message, input.ScheduledAt))
	if err == nil {
		if err := enqueue(tx, item.ID); err != nil {
			return announcements.Announcement{}, err
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		item, err = readAnnouncement(tx.QueryRow(ctx, `SELECT `+announcementColumns+
			` FROM scheduled_announcements WHERE group_id=$1 AND request_id=$2`, groupID, input.RequestID))
		if errors.Is(err, pgx.ErrNoRows) {
			if !input.ScheduledAt.After(time.Now()) {
				return item, core.Validation("scheduledAt must be in the future", nil)
			}
			return item, &core.AppError{Code: core.CodeConflict, Message: "the channel is not active for this group"}
		}
		if err == nil && (item.ChannelName != input.ChannelName || item.Type != input.Type ||
			item.Message != input.Message || !item.ScheduledAt.Equal(input.ScheduledAt)) {
			return item, &core.AppError{Code: core.CodeConflict, Message: "requestId was already used for a different announcement"}
		}
	}
	if err != nil {
		return item, err
	}
	return item, tx.Commit(ctx)
}

func (r *Repo) Announcements(ctx context.Context, groupID uuid.UUID) ([]announcements.Announcement, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+announcementColumns+`
		FROM scheduled_announcements WHERE group_id=$1 AND request_id IS NOT NULL AND history_cleared_at IS NULL
		ORDER BY scheduled_at DESC,id DESC LIMIT 100`, groupID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (announcements.Announcement, error) {
		return readAnnouncement(row)
	})
}

func (r *Repo) ClearAnnouncementHistory(ctx context.Context, groupID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE scheduled_announcements SET history_cleared_at=now()
		WHERE group_id=$1 AND request_id IS NOT NULL AND history_cleared_at IS NULL
		  AND (status IN ('published','failed','cancelled','unknown')
		       OR (status='publishing' AND updated_at < now() - interval '2 minutes'))
	`, groupID)
	return err
}

func (r *Repo) StartAnnouncement(ctx context.Context, id uuid.UUID) (announcements.Announcement, bool, error) {
	item, err := readAnnouncement(r.pool.QueryRow(ctx, `
		WITH eligible AS (
			SELECT a.id, EXISTS (
			    SELECT 1 FROM groups g WHERE g.id=a.group_id AND g.status<>'archived'
			      AND cardinality(a.channel_ids)>0
			      AND cardinality(a.channel_ids)=(SELECT count(*) FROM group_resources r
			        WHERE r.group_id=a.group_id AND r.provider='slack' AND r.resource_type='channel'
			          AND r.status='active' AND r.external_resource_id=ANY(a.channel_ids))
		    ) AS active
		    FROM scheduled_announcements a WHERE a.id=$1
		)
		UPDATE scheduled_announcements a SET
		    status=CASE WHEN (SELECT active FROM eligible) THEN 'publishing' ELSE 'failed' END,
		    error_message=CASE WHEN (SELECT active FROM eligible) THEN NULL ELSE 'the group channel is no longer active' END,
		    updated_at=now()
		WHERE a.id=$1 AND a.status='scheduled' AND a.request_id IS NOT NULL AND a.scheduled_at<=now()
		RETURNING `+announcementColumns, id))
	if errors.Is(err, pgx.ErrNoRows) {
		item, err = readAnnouncement(r.pool.QueryRow(ctx, `SELECT `+announcementColumns+
			` FROM scheduled_announcements WHERE id=$1 AND request_id IS NOT NULL`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return item, false, nil
		}
		return item, false, err
	}

	return item, err == nil && item.Status == "publishing", err
}

func (r *Repo) FinishAnnouncement(ctx context.Context, id uuid.UUID, status, externalID, message string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var groupID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE scheduled_announcements
		SET status=$2, external_message_id=NULLIF($3,''), error_message=NULLIF($4,''),
		    published_at=CASE WHEN $2='published' THEN now() END, updated_at=now()
		WHERE id=$1 AND status='publishing'
		RETURNING group_id
	`, id, status, externalID, message).Scan(&groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx) // already finished; don't create a duplicate notification
	}
	if err != nil {
		return err
	}
	err = insertAnnouncementNotification(ctx, tx, groupID, id, status, message)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type announcementNotificationExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func insertAnnouncementNotification(ctx context.Context, exec announcementNotificationExecutor, groupID, id uuid.UUID, status, message string) error {
	kind, severity, title, detail := "announcement_failed", "error", "Announcement delivery failed", message
	if status == "published" {
		kind, severity, title, detail = "announcement_delivered", "info", "Announcement delivered", "Announcement was published to Slack."
	} else if status == "unknown" {
		severity, title = "warning", "Announcement delivery outcome unknown"
	}
	_, err := exec.Exec(ctx, `INSERT INTO notifications (group_id, kind, severity, title, detail, dedupe_key)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (dedupe_key) DO NOTHING`,
		groupID, kind, severity, title, detail, "announcement:"+id.String())
	if err != nil {
		return fmt.Errorf("store: insert announcement notification: %w", err)
	}
	return nil
}

// EnsureAnnouncementNotification repairs the notification side effect when a
// previous worker attempt finished the announcement but failed before River
// acknowledged the job.
func (r *Repo) EnsureAnnouncementNotification(ctx context.Context, id uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var groupID uuid.UUID
	var status, message string
	err = tx.QueryRow(ctx, `SELECT group_id,
		CASE WHEN status='publishing' AND updated_at < now() - interval '2 minutes' THEN 'unknown' ELSE status END,
		CASE WHEN status='publishing' AND updated_at < now() - interval '2 minutes'
		     THEN 'publication was interrupted; Slack acceptance is unknown' ELSE COALESCE(error_message, '') END
		FROM scheduled_announcements WHERE id=$1`, id).Scan(&groupID, &status, &message)
	if err != nil {
		return err
	}
	if status != "published" && status != "failed" && status != "unknown" {
		return tx.Commit(ctx)
	}
	if err := insertAnnouncementNotification(ctx, tx, groupID, id, status, message); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
