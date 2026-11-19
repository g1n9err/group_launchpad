ALTER TABLE intake_forms
    ADD COLUMN questions jsonb NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(questions) = 'array');
