-- Integration connection queries (oauth_states, integrations, integration_credentials).

-- name: CreateOAuthState :exec
INSERT INTO oauth_states (state_hash, code_verifier_enc, provider, expires_at)
VALUES ($1, $2, $3, $4);

-- name: ConsumeOAuthState :one
-- Atomic single-use consumption: succeeds only for a live (unconsumed,
-- unexpired) state. No matching row -> pgx.ErrNoRows.
UPDATE oauth_states
SET consumed_at = now()
WHERE state_hash = $1
  AND consumed_at IS NULL
  AND expires_at > now()
RETURNING code_verifier_enc;

-- name: UpsertGoogleIntegration :one
INSERT INTO integrations (provider, account_identifier, connection_status)
VALUES ('google', $1, 'connected')
ON CONFLICT (provider, account_identifier) DO UPDATE
SET connection_status = 'connected'
RETURNING id, provider, account_identifier, connection_status, created_at;

-- name: RevokeActiveIntegrationCredentials :execrows
UPDATE integration_credentials
SET revoked_at = now()
WHERE integration_id = $1 AND revoked_at IS NULL;

-- name: InsertIntegrationCredential :exec
INSERT INTO integration_credentials (integration_id, refresh_token_enc, key_id, scopes)
VALUES ($1, $2, $3, $4);

-- name: GetGoogleConnection :one
-- Most recent 'google' connection row with its active (non-revoked)
-- credential, if any. credential_id NULL -> connected=false.
SELECT i.id,
       i.account_identifier,
       i.connection_status,
       i.created_at,
       c.id          AS credential_id,
       c.refresh_token_enc,
       c.key_id,
       c.scopes      AS credential_scopes,
       c.obtained_at
FROM integrations i
LEFT JOIN integration_credentials c
       ON c.integration_id = i.id AND c.revoked_at IS NULL
WHERE i.provider = 'google'
ORDER BY i.created_at DESC, c.obtained_at DESC NULLS LAST
LIMIT 1;

-- name: MarkGoogleIntegrationError :exec
UPDATE integrations
SET connection_status = 'error'
WHERE provider = 'google';

-- name: UpsertSlackIntegration :one
-- Idempotent connection upsert: repeated checks keep the original created_at.
INSERT INTO integrations (provider, account_identifier, connection_status, bot_user_id)
VALUES ('slack', $1, 'connected', $2)
ON CONFLICT (provider, account_identifier) DO UPDATE
SET connection_status = 'connected', bot_user_id = EXCLUDED.bot_user_id
RETURNING id, provider, account_identifier, connection_status, created_at;

-- name: MarkSlackIntegrationError :exec
UPDATE integrations
SET connection_status = 'error'
WHERE provider = 'slack';

-- name: InsertExternalOperationLog :exec
-- Every external API operation is recorded (PRD 8.7). group_id is NULL for
-- connection-level operations; error is a sanitized category, never raw text.
INSERT INTO external_operation_logs (group_id, provider, operation, status_code, error, duration_ms)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetGroupResource :one
SELECT id, group_id, provider, resource_type, external_resource_id, resource_name, status
FROM group_resources
WHERE group_id = $1 AND provider = $2 AND resource_type = $3 AND resource_name = $4;

-- name: InsertGroupResource :execrows
-- Claim a resource row; ON CONFLICT DO NOTHING makes the insert the winner
-- of any race (idempotency registry, PRD §8).
INSERT INTO group_resources (group_id, provider, resource_type, external_resource_id, resource_name, status)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (group_id, provider, resource_type, resource_name) DO NOTHING;

-- name: UpdateGroupResourceResult :exec
UPDATE group_resources
SET external_resource_id = $5, status = $6, updated_at = now()
WHERE group_id = $1 AND provider = $2 AND resource_type = $3 AND resource_name = $4;

-- ── Slack Events (Stage 2c) ────────────────────────────────────────────────

-- name: InsertSlackEvent :execrows
-- Atomic dedup: zero rows affected means the event_id was already recorded
-- (Slack retries up to 3×; delivery is at-least-once).
INSERT INTO slack_events (event_id, event_type, team_id, user_id, channel_id, payload)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (event_id) DO NOTHING;

-- name: CompleteSlackEvent :exec
UPDATE slack_events
SET processed_at = now(), process_status = $2, unresolved_reason = $3
WHERE event_id = $1;

-- name: GetSlackBotUserID :one
SELECT bot_user_id
FROM integrations
WHERE provider = 'slack' AND bot_user_id IS NOT NULL
ORDER BY created_at DESC
LIMIT 1;

-- name: FindGroupIDBySlackChannel :one
SELECT group_id
FROM group_resources
WHERE provider = 'slack' AND resource_type = 'channel' AND external_resource_id = $1
LIMIT 1;

-- name: FindUserIDBySlackID :one
SELECT id
FROM users
WHERE slack_user_id = $1
LIMIT 1;

-- name: FindUserIDsByEmail :many
-- Matching on lower(trim(email)): case-variant duplicates in users are
-- possible (the column unique index is case-sensitive) and MUST surface as
-- ambiguity rather than a silent single match.
SELECT id FROM users WHERE lower(btrim(email)) = $1;

-- name: LinkUserSlackID :execrows
-- Guarded link: only a user with NO Slack ID can receive one. Lost races
-- yield 0 rows (caller re-reads to distinguish already-linked vs mismatch).
UPDATE users SET slack_user_id = $1 WHERE id = $2 AND slack_user_id IS NULL;

-- name: GetUserSlackID :one
SELECT slack_user_id FROM users WHERE id = $1;

-- name: FindGroupMemberByUser :one
SELECT id, onboarding_status
FROM group_members
WHERE group_id = $1 AND user_id = $2
LIMIT 1;

-- name: MarkGroupMemberJoinedSlack :exec
-- Guarded transition: only pending/invited members move to joined_slack.
UPDATE group_members
SET onboarding_status = 'joined_slack'
WHERE id = $1 AND onboarding_status IN ('pending', 'invited');
