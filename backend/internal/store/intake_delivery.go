package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"launchpad/internal/core"
	"launchpad/internal/core/integrations"
)

func (r *Repo) QueueIntakeDeliveries(ctx context.Context, groupID uuid.UUID, message *string, enqueue func(pgx.Tx, uuid.UUID) error) ([]integrations.IntakeDelivery, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var formID uuid.UUID
	var status, formURL string
	err = tx.QueryRow(ctx, "SELECT id, status, COALESCE(form_url, '') FROM intake_forms WHERE group_id=$1 FOR UPDATE", groupID).
		Scan(&formID, &status, &formURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, core.NotFound("intake form", groupID.String())
	}
	if err != nil {
		return nil, err
	}
	if formURL == "" || (status != "created" && status != "distributed") {
		return nil, &core.AppError{Code: core.CodeConflict, Message: "the intake form must be published before distribution"}
	}

	rows, err := tx.Query(ctx, `
		INSERT INTO intake_form_deliveries (intake_form_id, user_id, recipient, message)
		SELECT $1, u.id, u.email, COALESCE($3::text, '') FROM group_members gm JOIN users u ON u.id=gm.user_id
		WHERE gm.group_id=$2 AND gm.member_role='student'
		ON CONFLICT (intake_form_id, user_id) DO UPDATE
		SET status='pending', recipient=EXCLUDED.recipient,
		    message=COALESCE($3::text, intake_form_deliveries.message), error_message=NULL, updated_at=now()
		WHERE intake_form_deliveries.status='failed'
		RETURNING id
	`, formID, groupID, message)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := enqueue(tx, id); err != nil {
			return nil, err
		}
	}
	deliveries, err := intakeDeliveries(ctx, tx, groupID)
	if err != nil {
		return nil, err
	}
	return deliveries, tx.Commit(ctx)
}

func (r *Repo) IntakeDeliveries(ctx context.Context, groupID uuid.UUID) ([]integrations.IntakeDelivery, error) {
	if _, err := r.GetIntakeForm(ctx, groupID); err != nil {
		return nil, err
	}
	return intakeDeliveries(ctx, r.pool, groupID)
}

func intakeDeliveries(ctx context.Context, db interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, groupID uuid.UUID) ([]integrations.IntakeDelivery, error) {
	rows, err := db.Query(ctx, `
		SELECT d.id, d.recipient,
		       CASE WHEN d.status='sending' AND d.updated_at < now() - interval '2 minutes'
		            THEN 'unknown' ELSE d.status END,
		       d.attempts,
		       CASE WHEN d.status='sending' AND d.updated_at < now() - interval '2 minutes'
		            THEN 'delivery was interrupted; relay acceptance is unknown'
		            ELSE COALESCE(d.error_message, '') END
		FROM intake_form_deliveries d JOIN intake_forms f ON f.id=d.intake_form_id
		WHERE f.group_id=$1 ORDER BY d.recipient, d.id
	`, groupID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (integrations.IntakeDelivery, error) {
		var d integrations.IntakeDelivery
		err := row.Scan(&d.ID, &d.Recipient, &d.Status, &d.Attempts, &d.Error)
		return d, err
	})
}

func (r *Repo) StartIntakeDelivery(ctx context.Context, id uuid.UUID) (integrations.IntakeEmail, bool, error) {
	var email integrations.IntakeEmail
	err := r.pool.QueryRow(ctx, `
		UPDATE intake_form_deliveries d SET status='sending', attempts=d.attempts+1, updated_at=now()
		FROM intake_forms f JOIN groups g ON g.id=f.group_id
		WHERE d.id=$1 AND d.status='pending' AND f.id=d.intake_form_id
		  AND f.status IN ('created', 'distributed') AND NULLIF(f.form_url, '') IS NOT NULL
		RETURNING d.recipient, g.name, f.form_url, d.message
	`, id).Scan(&email.Recipient, &email.GroupName, &email.ResponderURL, &email.Message)
	if errors.Is(err, pgx.ErrNoRows) {
		return email, false, nil
	}
	return email, err == nil, err
}

func (r *Repo) FinishIntakeDelivery(ctx context.Context, id uuid.UUID, status, message string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var formID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT f.id FROM intake_forms f JOIN intake_form_deliveries d ON f.id=d.intake_form_id
		WHERE d.id=$1 FOR UPDATE OF f
	`, id).Scan(&formID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE intake_form_deliveries SET status=$2, error_message=NULLIF($3, ''), updated_at=now()
		WHERE id=$1 AND status='sending'
	`, id, status, message)
	if err != nil {
		return err
	}
	if status == "sent" && tag.RowsAffected() == 1 {
		if _, err := tx.Exec(ctx, `
			UPDATE intake_forms f SET status='distributed' WHERE f.id=$1 AND f.status='created'
			  AND NOT EXISTS (
			    SELECT 1 FROM group_members gm WHERE gm.group_id=f.group_id AND gm.member_role='student'
			    AND NOT EXISTS (
			      SELECT 1 FROM intake_form_deliveries d
			      WHERE d.intake_form_id=f.id AND d.user_id=gm.user_id AND d.status='sent'
			    )
			  )
		`, formID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
