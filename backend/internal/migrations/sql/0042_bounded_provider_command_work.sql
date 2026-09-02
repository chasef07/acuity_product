-- acuity:no-transaction
-- acuity:complete-if-true
SELECT
    EXISTS (
        SELECT 1
        FROM pg_indexes
        WHERE schemaname = 'public'
            AND tablename = 'human_calling_provider_commands'
            AND indexname = 'human_calling_ready_commands_idx'
            AND indexdef LIKE
                '%(next_attempt_at, created_at, id) INCLUDE (call_id, call_leg_id, action, depends_on_command_id)%'
            AND indexdef LIKE '%WHERE (state = ''PENDING''::text)%'
    )
    AND EXISTS (
        SELECT 1
        FROM pg_indexes
        WHERE schemaname = 'public'
            AND tablename = 'human_calling_provider_commands'
            AND indexname = 'human_calling_interrupted_commands_idx'
            AND indexdef LIKE
                '%(updated_at, id) INCLUDE (call_id, call_leg_id, action, created_at)%'
            AND indexdef LIKE
                '%WHERE ((call_id IS NOT NULL) AND (state = ''SENDING''::text))%'
    );

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_ready_commands_idx;

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_interrupted_commands_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_ready_commands_idx
    ON human_calling_provider_commands (next_attempt_at, created_at, id)
    INCLUDE (call_id, call_leg_id, action, depends_on_command_id)
    WHERE state = 'PENDING';

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_interrupted_commands_idx
    ON human_calling_provider_commands (updated_at, id)
    INCLUDE (call_id, call_leg_id, action, created_at)
    WHERE call_id IS NOT NULL AND state = 'SENDING';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_pending_commands_idx;
