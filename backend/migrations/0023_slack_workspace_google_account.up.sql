ALTER TABLE integrations ADD COLUMN google_integration_id UUID REFERENCES integrations(id) ON DELETE SET NULL;
CREATE INDEX idx_integrations_google_account ON integrations (google_integration_id);
