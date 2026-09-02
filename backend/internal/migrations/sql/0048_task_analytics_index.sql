-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.work_tasks_created_analytics_idx')
        AND indisvalid AND indisready
);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS work_tasks_created_analytics_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY work_tasks_created_analytics_idx
    ON work_tasks (practice_id, created_at, id) INCLUDE (location_id);
