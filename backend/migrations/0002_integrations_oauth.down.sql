-- 0002_integrations_oauth.down.sql
DROP TABLE IF EXISTS integration_credentials;
DROP TABLE IF EXISTS oauth_states;
ALTER TABLE integrations DROP CONSTRAINT integrations_provider_check;
ALTER TABLE integrations ADD CONSTRAINT integrations_provider_check
    CHECK (provider IN ('slack', 'google_classroom', 'google_drive', 'google_forms'));
