package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"launchpad/internal/core"
	"launchpad/internal/core/announcements"
)

const announcementColumns = `
	id, group_id, request_id, channel_id, channel_name, message_type, message_content, scheduled_at,
	CASE WHEN status='publishing' AND updated_at < now() - interval '2 minutes' THEN 'unknown' ELSE status END,
	published_at, COALESCE(external_message_id, ''),
	CASE WHEN status='publishing' AND updated_at < now() - interval '2 minutes'
	     THEN 'publication was interrupted; Slack acceptance is unknown' ELSE COALESCE(error_message,'') END
`

func readAnnouncement(row interface{ Scan(...any) error }) (announcements.Announcement, error) {
	var item announcements.Announcement
	err := row.Scan(&item.ID, &item.GroupID, &item.RequestID, &item.ChannelID, &item.ChannelName,
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
		INSERT INTO scheduled_announcements
		    (group_id, request_id, channel_id, channel_name, message_type, message_content, scheduled_at)
		SELECT $1, $2, resource.external_resource_id, $3, $4, $5, $6
		FROM group_resources resource JOIN groups g ON g.id=resource.group_id
		WHERE resource.group_id=$1 AND resource.provider='slack' AND resource.resource_type='channel'
		  AND resource.resource_name=$3 AND resource.status='active' AND resource.external_resource_id<>''
		  AND g.status<>'archived' AND $6 > now()
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
		FROM scheduled_announcements WHERE group_id=$1 AND request_id IS NOT NULL
		ORDER BY scheduled_at DESC,id DESC LIMIT 100`, groupID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (announcements.Announcement, error) {
		return readAnnouncement(row)
	})
}

func (r *Repo) StartAnnouncement(ctx context.Context, id uuid.UUID) (announcements.Announcement, bool, error) {
	item, err := readAnnouncement(r.pool.QueryRow(ctx, `
		WITH eligible AS (
		    SELECT a.id, EXISTS (
		        SELECT 1 FROM group_resources r JOIN groups g ON g.id=r.group_id
		        WHERE r.group_id=a.group_id AND r.provider='slack' AND r.resource_type='channel'
		          AND r.resource_name=a.channel_name AND r.external_resource_id=a.channel_id
		          AND r.status='active' AND g.status<>'archived'
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
	_, err := r.pool.Exec(ctx, `
		UPDATE scheduled_announcements
		SET status=$2, external_message_id=NULLIF($3,''), error_message=NULLIF($4,''),
		    published_at=CASE WHEN $2='published' THEN now() END, updated_at=now()
		WHERE id=$1 AND status='publishing'
	`, id, status, externalID, message)
	return err
}
