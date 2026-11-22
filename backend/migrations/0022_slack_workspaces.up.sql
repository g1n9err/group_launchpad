-- Slack can be installed into several workspaces. Groups keep an optional
-- installation assignment; NULL remains compatible with the legacy server
-- SLACK_BOT_TOKEN workspace.
ALTER TABLE integrations ADD COLUMN team_name TEXT;
ALTER TABLE groups ADD COLUMN slack_integration_id UUID REFERENCES integrations(id) ON DELETE SET NULL;
CREATE INDEX idx_groups_slack_integration ON groups (slack_integration_id);
