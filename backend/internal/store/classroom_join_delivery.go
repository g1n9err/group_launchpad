package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *Repo) QueueClassroomJoinDeliveries(ctx context.Context, groupID uuid.UUID, courseID string, emails []string, enqueue func(pgx.Tx, uuid.UUID) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		INSERT INTO classroom_join_deliveries (group_id, course_id, recipient)
		SELECT $1::uuid, $2::text, recipients.email
		FROM (
			SELECT lower(btrim(u.email)) AS email
			FROM group_members gm JOIN users u ON u.id=gm.user_id
			WHERE gm.group_id=$1 AND gm.member_role IN ('student', 'ta', 'teaching_staff', 'lecturer')
			UNION
			SELECT lower(btrim(email))
			FROM staff_assignments
			WHERE group_id=$1 AND role IN ('ta', 'teaching_staff', 'lecturer')
		) recipients
		WHERE $3::text[] IS NULL OR recipients.email=ANY($3::text[])
		ON CONFLICT (group_id, course_id, recipient) DO UPDATE SET status='pending'
		WHERE classroom_join_deliveries.status IN ('failed','cancelled')
		RETURNING id
	`, groupID, courseID, emails)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := enqueue(tx, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Repo) StartClassroomJoinDelivery(ctx context.Context, id uuid.UUID) (recipient, courseName string, started bool, err error) {
	err = r.pool.QueryRow(ctx, `
		UPDATE classroom_join_deliveries d SET status='sending'
		FROM groups g
		WHERE d.id=$1 AND d.status='pending' AND g.id=d.group_id
		RETURNING d.recipient, g.course_name || ' / ' || g.name
	`, id).Scan(&recipient, &courseName)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	return recipient, courseName, err == nil, err
}

func (r *Repo) FinishClassroomJoinDelivery(ctx context.Context, id uuid.UUID, status string) error {
	_, err := r.pool.Exec(ctx, "UPDATE classroom_join_deliveries SET status=$2 WHERE id=$1 AND status='sending'", id, status)
	return err
}
