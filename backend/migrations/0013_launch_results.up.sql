-- 0013_launch_results.up.sql — Unified launch result persistence (PRD §5).
-- Stores per-platform step outcomes so the coordinator can inspect
-- exactly what succeeded, partially succeeded, or failed.

CREATE TABLE launch_results (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id     UUID NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'running'
                 CHECK (status IN ('running', 'completed', 'partially_failed', 'failed')),
    steps        JSONB NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (group_id) -- one result per group (latest launch overwrites)
);
