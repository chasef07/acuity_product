SET LOCAL lock_timeout = '1s';

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM work_recovery_reconciliation_queue) THEN
        RAISE EXCEPTION 'work_recovery_reconciliation_queue still has rows; let the previous release drain them before dropping it';
    END IF;
END
$$;

DROP TABLE work_recovery_reconciliation_queue;
