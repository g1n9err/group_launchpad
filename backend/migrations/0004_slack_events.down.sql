-- 0004_slack_events.down.sql
DROP INDEX IF EXISTS idx_slack_events_pending;
DROP TABLE IF EXISTS slack_events;
DROP INDEX IF EXISTS idx_users_slack_user_id;
ALTER TABLE users DROP COLUMN IF EXISTS slack_user_id;
ALTER TABLE integrations DROP COLUMN IF EXISTS bot_user_id;
