CREATE TABLE audit_notifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    audit_id uuid NOT NULL UNIQUE REFERENCES audit_logs(id) ON DELETE CASCADE,
    changes jsonb NOT NULL CHECK (jsonb_typeof(changes) = 'array'),
    email_status text NOT NULL CHECK (email_status IN ('disabled','pending','sending','sent','failed','unknown')),
    email_error text,
    updated_at timestamptz NOT NULL DEFAULT now()
);
