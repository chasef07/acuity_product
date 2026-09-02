-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.human_calling_staff_analytics_idx')
        AND indisvalid AND indisready
);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_staff_analytics_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_staff_analytics_idx
    ON human_calling_call_legs (call_id, bridged_at, id) INCLUDE (staff_subject, ended_at)
    WHERE role = 'STAFF' AND bridged_at IS NOT NULL;
