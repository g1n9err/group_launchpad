-- 0014_staff_notifications_templates.up.sql — Per-group staff, persistent
-- notifications, and announcement templates (PRD §4.1, §4.8, §4.9).

-- ── Per-group staff assignments (PRD §4.1) ─────────────────────────────
-- group_members already supports member_role lecturer/ta/teaching_staff,
-- but group creation currently only ingests students. This migration
-- adds a dedicated staff_assignments table for explicit per-group staff
-- configuration with roles, decoupled from user onboarding state.

CREATE TABLE staff_assignments (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id    UUID NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    role        TEXT NOT NULL
                CHECK (role IN ('teaching_staff', 'lecturer', 'ta')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (group_id, email)
);
CREATE INDEX idx_staff_assignments_group ON staff_assignments (group_id);

-- ── Persistent coordinator notifications (PRD §4.9) ────────────────────
-- Captures launch results, invite failures, onboarding gaps, audit
-- findings, and announcement delivery outcomes. Audit-specific
-- notifications remain in audit_notifications; this is the general table.

CREATE TABLE notifications (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id    UUID REFERENCES groups (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL
                CHECK (kind IN (
                    'launch_completed', 'launch_partially_failed', 'launch_failed',
                    'invite_failure', 'classroom_failure', 'drive_failure',
                    'forms_failure', 'onboarding_incomplete',
                    'audit_inconsistency', 'announcement_delivered',
                    'announcement_failed'
                )),
    severity    TEXT NOT NULL DEFAULT 'info'
                CHECK (severity IN ('info', 'warning', 'error')),
    title       TEXT NOT NULL,
    detail      TEXT,
    read_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_group_created ON notifications (group_id, created_at DESC);
CREATE INDEX idx_notifications_unread ON notifications (read_at) WHERE read_at IS NULL;

-- ── Announcement templates (PRD §4.8) ───────────────────────────────────
-- Reusable message templates that can be selected when scheduling.

CREATE TABLE announcement_templates (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    body        TEXT NOT NULL,
    kind        TEXT NOT NULL DEFAULT 'general'
                CHECK (kind IN ('welcome', 'staff_intro', 'rules', 'reminder', 'general')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
