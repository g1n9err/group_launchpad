-- 0001_init.up.sql — Group Launchpad initial schema
-- Full PRD §7.2 entity set + reliability tables (idempotency, operation log),
-- so later migrations are purely additive.

-- ── Users ────────────────────────────────────────────────────────────────
CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    full_name   TEXT NOT NULL,
    email       TEXT NOT NULL UNIQUE,
    role        TEXT NOT NULL DEFAULT 'student'
                CHECK (role IN ('coordinator', 'admin', 'lecturer', 'ta', 'student')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── Groups ───────────────────────────────────────────────────────────────
CREATE TABLE groups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,          -- duplicate identifiers prevented (PRD 4.1)
    course_name TEXT NOT NULL,
    start_date  DATE NOT NULL,
    status      TEXT NOT NULL DEFAULT 'draft'
                CHECK (status IN ('draft', 'launching', 'launched',
                                  'partially_failed', 'failed', 'archived')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_groups_status ON groups (status);

-- ── Group members (staff / lecturers / TAs / students) ───────────────────
CREATE TABLE group_members (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id          UUID NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    user_id           UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    member_role       TEXT NOT NULL
                      CHECK (member_role IN ('teaching_staff', 'lecturer', 'ta', 'student')),
    onboarding_status TEXT NOT NULL DEFAULT 'pending'
                      CHECK (onboarding_status IN
                             ('pending', 'invited', 'joined_slack', 'form_submitted', 'complete')),
    UNIQUE (group_id, user_id)
);
CREATE INDEX idx_group_members_group ON group_members (group_id);

-- ── Integrations (OAuth connections, per provider account) ───────────────
CREATE TABLE integrations (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider           TEXT NOT NULL
                       CHECK (provider IN ('slack', 'google_classroom', 'google_drive', 'google_forms')),
    account_identifier TEXT NOT NULL,
    connection_status  TEXT NOT NULL DEFAULT 'disconnected'
                       CHECK (connection_status IN ('connected', 'disconnected', 'error')),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, account_identifier)
);

-- ── External resources per group — the idempotency registry (PRD §8) ─────
CREATE TABLE group_resources (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id             UUID NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    provider             TEXT NOT NULL
                         CHECK (provider IN ('slack', 'google_classroom', 'google_drive', 'google_forms')),
    resource_type        TEXT NOT NULL,        -- 'channel' | 'course' | 'folder' | 'form'
    external_resource_id TEXT NOT NULL,
    resource_name        TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'active'
                         CHECK (status IN ('pending', 'active', 'error', 'deleted')),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, resource_type, resource_name)
);
CREATE INDEX idx_group_resources_group ON group_resources (group_id);

-- ── Intake forms (Google Forms, confirmed) ───────────────────────────────
CREATE TABLE intake_forms (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id         UUID NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    external_form_id TEXT,
    form_url         TEXT,
    status           TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'created', 'distributed', 'closed', 'error')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (group_id)
);

-- ── Audit logs (expected vs actual snapshots, daily sync) ────────────────
CREATE TABLE audit_logs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id       UUID NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    audit_type     TEXT NOT NULL DEFAULT 'daily_sync',
    expected_state JSONB NOT NULL,
    actual_state   JSONB NOT NULL,
    differences    JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_logs_group_created ON audit_logs (group_id, created_at DESC);

-- ── Scheduled announcements ──────────────────────────────────────────────
CREATE TABLE scheduled_announcements (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id        UUID NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    message_type    TEXT NOT NULL,             -- 'welcome' | 'staff_intro' | 'rules' | 'reminder' | 'general'
    message_content TEXT NOT NULL,
    scheduled_at    TIMESTAMPTZ NOT NULL,
    status          TEXT NOT NULL DEFAULT 'scheduled'
                    CHECK (status IN ('scheduled', 'publishing', 'published', 'failed', 'cancelled')),
    published_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_scheduled_announcements_due ON scheduled_announcements (status, scheduled_at);

-- ── Automation jobs — per-group workflow execution state (PRD §8) ────────
CREATE TABLE automation_jobs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id       UUID NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    job_type       TEXT NOT NULL,              -- 'launch' | 'daily_audit' | 'announcement_publish' | ...
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'partially_failed')),
    attempts       INT NOT NULL DEFAULT 0,
    idempotency_key TEXT NOT NULL UNIQUE,      -- e.g. 'launch:<group_id>' — safe re-execution
    error_message  TEXT,
    started_at     TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_automation_jobs_group ON automation_jobs (group_id);

-- ── External operation log — every external API call is recorded (PRD 8.7)
CREATE TABLE external_operation_logs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id    UUID REFERENCES groups (id) ON DELETE CASCADE,
    provider    TEXT NOT NULL,                 -- 'slack' | 'google_classroom' | ...
    operation   TEXT NOT NULL,                 -- e.g. 'slack.conversations.create'
    status_code INT,
    error       TEXT,
    duration_ms INT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_external_operation_logs_group ON external_operation_logs (group_id, created_at DESC);
