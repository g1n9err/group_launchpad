DROP INDEX IF EXISTS idx_groups_slack_integration;
ALTER TABLE groups DROP COLUMN IF EXISTS slack_integration_id;
ALTER TABLE integrations DROP COLUMN IF EXISTS team_name;
