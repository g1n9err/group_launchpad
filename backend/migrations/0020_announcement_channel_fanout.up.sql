ALTER TABLE scheduled_announcements
    ADD COLUMN channel_ids text[] NOT NULL DEFAULT '{}';

UPDATE scheduled_announcements
SET channel_ids = ARRAY[channel_id]
WHERE channel_id IS NOT NULL AND channel_id <> '';
