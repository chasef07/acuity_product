-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.work_tasks_completed_recent_idx')
        AND indisvalid AND indisready
) AND EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.work_tasks_open_recent_idx')
        AND indisvalid AND indisready
) AND EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.work_tasks_phone_history_idx')
        AND indisvalid AND indisready
) AND to_regclass('public.work_task_activities_task_idx') IS NULL;

-- acuity:next-statement
SET lock_timeout = '15s';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS work_tasks_completed_recent_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY work_tasks_completed_recent_idx
    ON work_tasks (practice_id, completed_at DESC, id DESC)
    WHERE state = 'COMPLETED';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS work_tasks_open_recent_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY work_tasks_open_recent_idx
    ON work_tasks (practice_id, updated_at DESC, id DESC)
    INCLUDE (location_id, origin)
    WHERE state = 'OPEN';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS work_tasks_phone_history_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY work_tasks_phone_history_idx
    ON work_tasks (practice_id, phone, location_id);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS work_task_activities_task_idx;

-- acuity:next-statement
RESET lock_timeout;
