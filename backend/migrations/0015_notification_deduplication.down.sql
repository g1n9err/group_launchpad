-- 0015_notification_deduplication.down.sql
DROP INDEX IF EXISTS uq_notifications_dedupe_key;
ALTER TABLE notifications DROP COLUMN IF EXISTS dedupe_key;
