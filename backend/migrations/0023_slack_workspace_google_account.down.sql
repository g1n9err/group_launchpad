DROP INDEX IF EXISTS idx_integrations_google_account;
ALTER TABLE integrations DROP COLUMN IF EXISTS google_integration_id;
