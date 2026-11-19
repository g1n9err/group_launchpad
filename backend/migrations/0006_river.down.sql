-- Remove the queue schema owned by 0006. Dropping river_job also removes
-- its indexes and owned ID sequence.
DROP TABLE river_notification;
DROP TABLE river_queue;
DROP TABLE river_leader;
DROP TABLE river_job;
DROP FUNCTION river_job_state_in_bitmask(bit(8), river_job_state);
DROP TYPE river_job_state;
