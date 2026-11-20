-- 0015_notification_deduplication.up.sql
-- Stable source keys make worker-created notifications safe to retry.

ALTER TABLE notifications ADD COLUMN dedupe_key TEXT;
CREATE UNIQUE INDEX uq_notifications_dedupe_key
    ON notifications (dedupe_key);
