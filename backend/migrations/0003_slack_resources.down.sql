-- 0003_slack_resources.down.sql
ALTER TABLE group_resources DROP CONSTRAINT uq_group_resources_per_group;
ALTER TABLE group_resources ADD CONSTRAINT group_resources_provider_resource_type_resource_name_key
    UNIQUE (provider, resource_type, resource_name);
