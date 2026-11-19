CREATE TABLE intake_form_deliveries (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    intake_form_id UUID NOT NULL REFERENCES intake_forms (id) ON DELETE CASCADE,
    user_id        UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    recipient      TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'sending', 'sent', 'failed', 'unknown')),
    attempts       INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    error_message  TEXT,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (intake_form_id, user_id)
);
