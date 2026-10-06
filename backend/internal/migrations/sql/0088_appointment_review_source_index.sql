-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.work_tasks_appointment_review_source_idx')
        AND indisvalid AND indisready
);

-- acuity:next-statement
SET lock_timeout = '15s';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS work_tasks_appointment_review_source_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY work_tasks_appointment_review_source_idx
    ON work_tasks (practice_id, source_call_id)
    WHERE origin = 'APPOINTMENT_REVIEW';

-- acuity:next-statement
RESET lock_timeout;
