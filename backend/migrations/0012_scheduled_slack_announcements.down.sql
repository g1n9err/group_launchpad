UPDATE scheduled_announcements SET status='failed' WHERE status='unknown';
ALTER TABLE scheduled_announcements
    DROP CONSTRAINT scheduled_announcements_status_check,
    ADD CONSTRAINT scheduled_announcements_status_check
        CHECK (status IN ('scheduled', 'publishing', 'published', 'failed', 'cancelled')),
    DROP COLUMN request_id,
    DROP COLUMN channel_id,
    DROP COLUMN channel_name,
    DROP COLUMN external_message_id,
    DROP COLUMN error_message,
    DROP COLUMN updated_at;
