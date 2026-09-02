-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.human_calling_completed_analytics_idx')
        AND indisvalid AND indisready
);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_completed_analytics_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_completed_analytics_idx
    ON human_calling_calls (practice_id, ended_at, id) INCLUDE (location_id, direction)
    WHERE ended_at IS NOT NULL;
