-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.messaging_messages_staff_analytics_idx')
        AND indisvalid AND indisready
);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS messaging_messages_staff_analytics_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY messaging_messages_staff_analytics_idx
    ON messaging_messages (practice_id, location_id, created_at)
    INCLUDE (created_by_subject)
    WHERE direction = 'OUTBOUND' AND created_by_kind = 'HUMAN'
        AND delivery_state IN ('SENT', 'DELIVERED');
