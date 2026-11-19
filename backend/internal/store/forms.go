package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"launchpad/internal/core"
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
	result, err = r.GetIntakeForm(ctx, groupID)
	return result, false, err
}

func (r *Repo) GetIntakeForm(ctx context.Context, groupID uuid.UUID) (integrations.IntakeForm, error) {
	var form integrations.IntakeForm
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(external_form_id, ''), status, questions, COALESCE(form_url, '')
		FROM intake_forms WHERE group_id=$1
	`, groupID).Scan(&form.ID, &form.Status, &form.Questions, &form.ResponderURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return form, core.NotFound("intake form", groupID.String())
	}
	return form, err
}

func (r *Repo) SaveIntakeResponderURL(ctx context.Context, groupID uuid.UUID, formID, link string) (string, error) {
	var saved string
	err := r.pool.QueryRow(ctx, `
		UPDATE intake_forms SET form_url=COALESCE(NULLIF(form_url, ''), $3)
		WHERE group_id=$1 AND external_form_id=$2 AND status IN ('created', 'distributed')
		RETURNING form_url
	`, groupID, formID, link).Scan(&saved)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", &core.AppError{Code: core.CodeConflict, Message: "intake form changed while it was being published"}
	}
	return saved, err
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
