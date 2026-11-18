-- 0002_integrations_oauth.up.sql — Google OAuth core (Stage 1)
--
-- One Google OAuth connection serves Classroom, Drive, and Forms (PRD 4.3-4.5);
-- the provider value 'google' marks that unified connection row.

-- Widen the provider check to include the unified Google connection.
ALTER TABLE integrations DROP CONSTRAINT integrations_provider_check;
ALTER TABLE integrations ADD CONSTRAINT integrations_provider_check
    CHECK (provider IN ('slack', 'google', 'google_classroom', 'google_drive', 'google_forms'));

-- Single-use, short-lived OAuth state values. The raw state is never stored —
-- only its SHA-256 hash; the PKCE verifier is stored encrypted.
CREATE TABLE oauth_states (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    state_hash        TEXT NOT NULL UNIQUE,
    code_verifier_enc TEXT NOT NULL,          -- AES-256-GCM ciphertext (base64)
    provider          TEXT NOT NULL DEFAULT 'google',
    expires_at        TIMESTAMPTZ NOT NULL,
    consumed_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_oauth_states_expires ON oauth_states (expires_at);

-- Encrypted credentials. Refresh tokens are NEVER stored in plaintext (PRD 9.2);
-- access tokens live only in process memory.
CREATE TABLE integration_credentials (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id    UUID NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    refresh_token_enc TEXT NOT NULL,          -- AES-256-GCM ciphertext (base64)
    key_id            TEXT NOT NULL,          -- encryption key fingerprint, supports rotation
    scopes            TEXT[] NOT NULL,
    obtained_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at        TIMESTAMPTZ
);
CREATE INDEX idx_integration_credentials_integration ON integration_credentials (integration_id);

-- At most one active credential per integration.
CREATE UNIQUE INDEX idx_integration_credentials_active
    ON integration_credentials (integration_id) WHERE revoked_at IS NULL;
