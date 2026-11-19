ALTER TABLE intake_forms ADD COLUMN verified_responses_since timestamptz;

CREATE TABLE intake_form_responses (
    intake_form_id uuid NOT NULL REFERENCES intake_forms(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    external_response_id text NOT NULL,
    submitted_at timestamptz NOT NULL,
    answers jsonb NOT NULL CHECK (jsonb_typeof(answers) = 'object'),
    PRIMARY KEY (intake_form_id, user_id)
);
