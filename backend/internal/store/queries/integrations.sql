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
