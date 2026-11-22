UPDATE classroom_join_deliveries SET status='failed' WHERE status='cancelled';
ALTER TABLE classroom_join_deliveries
    DROP CONSTRAINT classroom_join_deliveries_status_check,
    ADD CONSTRAINT classroom_join_deliveries_status_check
        CHECK (status IN ('pending','sending','sent','failed','unknown'));
