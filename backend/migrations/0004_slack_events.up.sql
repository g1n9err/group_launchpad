-- 0004_slack_events.up.sql — Slack Events API (Stage 2c)
--
-- Durable at-least-once event handling: dedup by event_id (Slack retries),
-- durable processing state (nothing lost after ack), and the linkage columns
-- needed to resolve member_joined_channel against our own data.

-- Event intake + dedup + processing ledger. PK on event_id makes duplicate
-- delivery an INSERT-conflict, atomically.
CREATE TABLE slack_events (
    event_id         TEXT PRIMARY KEY,
    event_type       TEXT NOT NULL,
    team_id          TEXT,
    user_id          TEXT,
    channel_id       TEXT,
    payload          JSONB NOT NULL,
    received_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at     TIMESTAMPTZ,
    process_status   TEXT NOT NULL DEFAULT 'pending'
                     CHECK (process_status IN ('pending', 'processed', 'unresolved', 'bot', 'ignored')),
    unresolved_reason TEXT
);

-- Reconciliation sweep index (future worker re-processes stuck 'pending').
CREATE INDEX idx_slack_events_pending
    ON slack_events (received_at)
    WHERE process_status = 'pending';

-- Resolve Slack users to our users (populated by the invite flow / wizard).
ALTER TABLE users ADD COLUMN slack_user_id TEXT;
CREATE UNIQUE INDEX idx_users_slack_user_id
    ON users (slack_user_id) WHERE slack_user_id IS NOT NULL;

-- Bot identity (from auth.test) so bot joins are classified, never treated
-- as a student onboarding.
ALTER TABLE integrations ADD COLUMN bot_user_id TEXT;
