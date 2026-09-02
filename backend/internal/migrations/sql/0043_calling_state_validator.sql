-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1
    FROM pg_indexes
    WHERE schemaname = 'public'
        AND tablename = 'human_calling_calls'
        AND indexname = 'human_calling_state_active_practice_idx'
        AND indexdef LIKE '%(practice_id, id)%'
        AND indexdef LIKE '%terminal_outcome IS NULL%'
        AND indexdef LIKE '%disposition_at IS NULL%'
        AND indexdef LIKE '%''ENDED''::text%'
        AND indexdef LIKE '%''VOICEMAIL''::text%'
);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_state_active_practice_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_state_active_practice_idx
    ON human_calling_calls (practice_id, id)
    WHERE terminal_outcome IS NULL
        OR (
            disposition_at IS NULL
            AND terminal_outcome IN ('ENDED', 'VOICEMAIL')
        );
