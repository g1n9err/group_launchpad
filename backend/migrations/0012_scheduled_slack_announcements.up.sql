ALTER TABLE scheduled_announcements
    ADD COLUMN request_id uuid,
    ADD COLUMN channel_id text,
    ADD COLUMN channel_name text,
    ADD COLUMN external_message_id text,
    ADD COLUMN error_message text,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
    ADD UNIQUE (group_id, request_id),
    DROP CONSTRAINT scheduled_announcements_status_check,
    ADD CONSTRAINT scheduled_announcements_status_check
        CHECK (status IN ('scheduled', 'publishing', 'published', 'failed', 'cancelled', 'unknown'));
