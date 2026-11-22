-- Each self-registered coordinator owns an isolated Launchpad workspace.
-- Nullable owner IDs preserve platform-admin access to legacy records; all
-- records created through coordinator sessions receive that user's ID.
ALTER TABLE groups ADD COLUMN workspace_owner_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE integrations ADD COLUMN workspace_owner_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE oauth_states ADD COLUMN workspace_owner_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE notifications ADD COLUMN workspace_owner_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE announcement_templates ADD COLUMN workspace_owner_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE predefined_staff ADD COLUMN workspace_owner_id UUID REFERENCES users(id) ON DELETE CASCADE;

-- Keep existing installations under the earliest coordinator account, when
-- one exists. A deployment that only used the legacy Basic account remains
-- visible to platform admins until its data is explicitly assigned.
WITH owner AS (SELECT id FROM users WHERE role='coordinator' ORDER BY created_at,id LIMIT 1)
UPDATE groups SET workspace_owner_id=(SELECT id FROM owner) WHERE workspace_owner_id IS NULL;
WITH owner AS (SELECT id FROM users WHERE role='coordinator' ORDER BY created_at,id LIMIT 1)
UPDATE integrations SET workspace_owner_id=(SELECT id FROM owner) WHERE workspace_owner_id IS NULL;
WITH owner AS (SELECT id FROM users WHERE role='coordinator' ORDER BY created_at,id LIMIT 1)
UPDATE oauth_states SET workspace_owner_id=(SELECT id FROM owner) WHERE workspace_owner_id IS NULL;
WITH owner AS (SELECT id FROM users WHERE role='coordinator' ORDER BY created_at,id LIMIT 1)
UPDATE notifications SET workspace_owner_id=(SELECT id FROM owner) WHERE workspace_owner_id IS NULL;
WITH owner AS (SELECT id FROM users WHERE role='coordinator' ORDER BY created_at,id LIMIT 1)
UPDATE announcement_templates SET workspace_owner_id=(SELECT id FROM owner) WHERE workspace_owner_id IS NULL;
WITH owner AS (SELECT id FROM users WHERE role='coordinator' ORDER BY created_at,id LIMIT 1)
UPDATE predefined_staff SET workspace_owner_id=(SELECT id FROM owner) WHERE workspace_owner_id IS NULL;

ALTER TABLE groups DROP CONSTRAINT IF EXISTS groups_name_key;
CREATE UNIQUE INDEX uq_groups_workspace_name ON groups(workspace_owner_id,name) NULLS NOT DISTINCT;
CREATE INDEX idx_groups_workspace_owner ON groups(workspace_owner_id,created_at DESC);

ALTER TABLE group_resources DROP CONSTRAINT IF EXISTS uq_group_resources_per_group;
CREATE UNIQUE INDEX uq_group_resources_group_key ON group_resources(group_id,provider,resource_type,resource_name);

ALTER TABLE integrations DROP CONSTRAINT IF EXISTS integrations_provider_account_identifier_key;
CREATE UNIQUE INDEX uq_integrations_workspace_account ON integrations(provider,workspace_owner_id,account_identifier) NULLS NOT DISTINCT;
CREATE INDEX idx_integrations_workspace_owner ON integrations(workspace_owner_id,provider);
CREATE INDEX idx_oauth_states_workspace_owner ON oauth_states(workspace_owner_id,provider);

CREATE INDEX idx_notifications_workspace_owner ON notifications(workspace_owner_id,created_at DESC);
CREATE OR REPLACE FUNCTION set_notification_workspace_owner() RETURNS trigger AS $$
BEGIN
    IF NEW.group_id IS NOT NULL THEN
        SELECT workspace_owner_id INTO NEW.workspace_owner_id FROM groups WHERE id=NEW.group_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER notifications_workspace_owner_from_group
    BEFORE INSERT OR UPDATE OF group_id ON notifications
    FOR EACH ROW EXECUTE FUNCTION set_notification_workspace_owner();
ALTER TABLE announcement_templates DROP CONSTRAINT IF EXISTS announcement_templates_name_key;
CREATE UNIQUE INDEX uq_announcement_templates_workspace_name ON announcement_templates(workspace_owner_id,name) NULLS NOT DISTINCT;
DROP INDEX IF EXISTS uq_predefined_staff_email;
CREATE UNIQUE INDEX uq_predefined_staff_workspace_email ON predefined_staff(workspace_owner_id,lower(btrim(email))) NULLS NOT DISTINCT;
