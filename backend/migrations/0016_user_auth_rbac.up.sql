-- 0016_user_auth_rbac.up.sql — Multi-role authentication (PRD §3, §7.4).
--
-- Per-user credentials (bcrypt — never plaintext), expiring bearer sessions
-- (only the SHA-256 hash of the token is stored), and the admin-managed
-- predefined staff list (PRD §3.2 "manage predefined staff", §4.2).

CREATE TABLE user_credentials (
    user_id       UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL,            -- bcrypt hash; plaintext never persisted
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE user_sessions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,      -- hex(sha256(token)); the token itself
                                             -- is shown once at login/refresh only
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);
CREATE INDEX idx_user_sessions_user ON user_sessions (user_id);
CREATE INDEX idx_user_sessions_active ON user_sessions (expires_at) WHERE revoked_at IS NULL;

-- Predefined staff (PRD §4.2: "a predefined list of eight staff members").
-- Admin-managed; replaces the STAFF_EMAILS env fallback once populated.
CREATE TABLE predefined_staff (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email      TEXT NOT NULL,
    full_name  TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Normalized-email uniqueness (same canonical form as users).
CREATE UNIQUE INDEX uq_predefined_staff_email ON predefined_staff (lower(btrim(email)));
