-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm'
) AND EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.work_tasks_title_search_idx')
        AND indisvalid AND indisready
) AND EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.work_tasks_caller_name_search_idx')
        AND indisvalid AND indisready
) AND EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.work_tasks_category_search_idx')
        AND indisvalid AND indisready
) AND EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.work_tasks_phone_digits_search_idx')
        AND indisvalid AND indisready
);

-- acuity:next-statement
SET lock_timeout = '15s';

-- acuity:next-statement
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS work_tasks_title_search_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY work_tasks_title_search_idx
    ON work_tasks USING gin (lower(title) gin_trgm_ops)
    WITH (fastupdate = off);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS work_tasks_caller_name_search_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY work_tasks_caller_name_search_idx
    ON work_tasks USING gin (lower(caller_name) gin_trgm_ops)
    WITH (fastupdate = off);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS work_tasks_category_search_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY work_tasks_category_search_idx
    ON work_tasks USING gin (lower(category) gin_trgm_ops)
    WITH (fastupdate = off);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS work_tasks_phone_digits_search_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY work_tasks_phone_digits_search_idx
    ON work_tasks USING gin (phone_digits gin_trgm_ops)
    WITH (fastupdate = off);

-- acuity:next-statement
RESET lock_timeout;
