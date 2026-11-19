-- 0003_slack_resources.up.sql — Slack channel operations (Stage 2b)
--
-- The idempotency registry (PRD §8) is per-GROUP: each group tracks its own
-- channels, welcome messages, and (later) Classroom/Drive/Form resources.
-- The original workspace-global UNIQUE (provider, resource_type,
-- resource_name) prevented two groups from ever recording the same resource
-- name — replace it with a per-group constraint.

ALTER TABLE group_resources DROP CONSTRAINT group_resources_provider_resource_type_resource_name_key;
ALTER TABLE group_resources ADD CONSTRAINT uq_group_resources_per_group
    UNIQUE (group_id, provider, resource_type, resource_name);
