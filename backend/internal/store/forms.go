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
		SELECT COALESCE(external_form_id, ''), status, questions, COALESCE(form_url, ''), verified_responses_since
		FROM intake_forms WHERE group_id=$1
	`, groupID).Scan(&form.ID, &form.Status, &form.Questions, &form.ResponderURL, &form.VerifiedSince)
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

func (r *Repo) MarkIntakeVerified(ctx context.Context, groupID uuid.UUID, formID string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE intake_forms SET verified_responses_since=COALESCE(verified_responses_since, now())
		WHERE group_id=$1 AND external_form_id=$2 AND status IN ('created','distributed')
	`, groupID, formID)
	if err == nil && tag.RowsAffected() != 1 {
		return &core.AppError{Code: core.CodeConflict, Message: "intake form changed while enabling verified email"}
	}
	return err
}

func (r *Repo) SaveIntakeResponse(ctx context.Context, groupID uuid.UUID, formID string, response integrations.IntakeResponse) (bool, bool, error) {
	// Updating the response and member status in the same statement keeps the
	// two onboarding signals consistent with concurrent Slack join events.
	var matched, changed bool
	err := r.pool.QueryRow(ctx, `
		WITH member AS (
		    SELECT gm.id AS member_id, gm.user_id, f.id AS intake_form_id
		    FROM intake_forms f JOIN group_members gm ON gm.group_id=f.group_id
		    JOIN users u ON u.id=gm.user_id
		    WHERE f.group_id=$1 AND f.external_form_id=$2 AND gm.member_role='student'
		      AND lower(btrim(u.email))=$3
		      AND f.verified_responses_since <= $4
		), saved AS (
		    INSERT INTO intake_form_responses AS previous
		        (intake_form_id, user_id, external_response_id, submitted_at, answers)
		    SELECT intake_form_id, user_id, $5, $6, $7::jsonb FROM member
		    ON CONFLICT (intake_form_id, user_id) DO UPDATE
		    SET external_response_id=EXCLUDED.external_response_id, submitted_at=EXCLUDED.submitted_at, answers=EXCLUDED.answers
		    WHERE (EXCLUDED.submitted_at, EXCLUDED.external_response_id) >
		          (previous.submitted_at, previous.external_response_id)
		    RETURNING user_id
		), onboarding AS (
		    UPDATE group_members gm
		    SET onboarding_status=CASE WHEN gm.onboarding_status='joined_slack' THEN 'complete' ELSE 'form_submitted' END
		    FROM member m
		    WHERE gm.id=m.member_id AND gm.onboarding_status IN ('pending','invited','joined_slack')
		    RETURNING gm.id
		)
		SELECT EXISTS (SELECT 1 FROM member), EXISTS (SELECT 1 FROM saved)
	`, groupID, formID, response.Email, response.CreatedAt, response.ID, response.SubmittedAt, response.Answers).Scan(&matched, &changed)
	return matched, changed, err
}
