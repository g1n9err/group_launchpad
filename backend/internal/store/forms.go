package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"launchpad/internal/core/integrations"
)

func (r *Repo) ClaimIntakeForm(ctx context.Context, groupID uuid.UUID, questions []integrations.IntakeQuestion) (integrations.IntakeForm, bool, error) {
	var result integrations.IntakeForm
	err := r.pool.QueryRow(ctx, `
		INSERT INTO intake_forms (group_id, questions)
		VALUES ($1, $2::jsonb) ON CONFLICT (group_id) DO NOTHING
		RETURNING status, questions
	`, groupID, questions).Scan(&result.Status, &result.Questions)
	if !errors.Is(err, pgx.ErrNoRows) {
		return result, err == nil, err
	}
	err = r.pool.QueryRow(ctx, `SELECT COALESCE(external_form_id, ''), status, questions FROM intake_forms WHERE group_id=$1`, groupID).
		Scan(&result.ID, &result.Status, &result.Questions)
	return result, false, err
}

func (r *Repo) ReleaseIntakeForm(ctx context.Context, groupID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM intake_forms WHERE group_id=$1 AND external_form_id IS NULL AND status='pending'`, groupID)
	return err
}

func (r *Repo) SaveIntakeFormID(ctx context.Context, groupID uuid.UUID, id string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE intake_forms SET external_form_id=$2
		WHERE group_id=$1 AND (external_form_id IS NULL OR external_form_id=$2)`, groupID, id)
	if err == nil && tag.RowsAffected() != 1 {
		return fmt.Errorf("intake form ID conflicts with the saved ID")
	}
	return err
}

func (r *Repo) CompleteIntakeForm(ctx context.Context, groupID uuid.UUID, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE intake_forms SET status='created'
		WHERE group_id=$1 AND external_form_id=$2 AND status='pending'`, groupID, id)
	return err
}
