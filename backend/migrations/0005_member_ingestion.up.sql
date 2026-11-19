-- 0005_member_ingestion.up.sql — Student member ingestion (Stage 3B)
--
-- Users are matched by NORMALIZED email (lower/trim — the same form the
-- Stage 3A identity linker uses). The stock UNIQUE(email) is case-SENSITIVE,
-- which would allow case-variant duplicate users (the exact ambiguity 3A
-- refuses to guess between). Enforce normalized uniqueness going forward.

CREATE UNIQUE INDEX uq_users_email_normalized
    ON users (lower(btrim(email)));
