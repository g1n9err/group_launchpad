package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"launchpad/internal/core"
	"launchpad/internal/core/groups"
)

const slackLaunchColumns = "id, group_id, status, attempts, COALESCE(error_message, ''), created_at, completed_at"

// QueueSlackLaunch writes the execution record and enqueues its worker in the same transaction.
func (r *Repo) QueueSlackLaunch(ctx context.Context, groupID uuid.UUID, enqueue func(pgx.Tx, uuid.UUID) error) (groups.SlackLaunch, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return groups.SlackLaunch{}, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO automation_jobs (group_id, job_type, idempotency_key)
		SELECT id, 'slack_launch', 'slack_launch:' || id::text FROM groups WHERE id=$1
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id`, groupID).Scan(&id)
	switch {
	case err == nil:
		if err := enqueue(tx, id); err != nil {
			return groups.SlackLaunch{}, fmt.Errorf("enqueue Slack launch: %w", err)
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return groups.SlackLaunch{}, err
	}
	run, err := slackLaunchByGroup(ctx, tx, groupID)
	if err != nil {
		return run, err
	}
	return run, tx.Commit(ctx)
}

func (r *Repo) SlackLaunch(ctx context.Context, groupID uuid.UUID) (groups.SlackLaunch, error) {
	return slackLaunchByGroup(ctx, r.pool, groupID)
}

func slackLaunchByGroup(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, groupID uuid.UUID) (groups.SlackLaunch, error) {
	var run groups.SlackLaunch
	err := db.QueryRow(ctx, "SELECT "+slackLaunchColumns+" FROM automation_jobs WHERE idempotency_key='slack_launch:' || $1::uuid::text", groupID).
		Scan(&run.ID, &run.GroupID, &run.Status, &run.Attempts, &run.Error, &run.CreatedAt, &run.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return run, core.NotFound("Slack launch", groupID.String())
	}
	return run, err
}

func (r *Repo) StartSlackLaunch(ctx context.Context, id uuid.UUID, attempt int) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE automation_jobs
		SET status='running', attempts=$2, started_at=COALESCE(started_at, now())
		WHERE id=$1 AND job_type='slack_launch' AND completed_at IS NULL`, id, attempt)
	return tag.RowsAffected() == 1, err
}

func (r *Repo) FinishSlackLaunchAttempt(ctx context.Context, id uuid.UUID, attempt int, status, message string, terminal bool) error {
	_, err := r.pool.Exec(ctx, `UPDATE automation_jobs SET status=$3, error_message=NULLIF($4, ''),
		completed_at=CASE WHEN $5 THEN now() END
		WHERE id=$1 AND attempts=$2 AND completed_at IS NULL`, id, attempt, status, message, terminal)
	return err
}
