-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1
    FROM pg_indexes
    WHERE schemaname = 'public'
        AND tablename = 'human_calling_provider_commands'
        AND indexname = 'human_calling_interrupted_credential_commands_idx'
        AND indexdef LIKE '%(updated_at, id)%'
        AND indexdef LIKE '%call_id IS NULL%'
        AND indexdef LIKE '%CREATE_CREDENTIAL%'
        AND indexdef LIKE '%DISABLE_CREDENTIAL%'
        AND indexdef LIKE '%state = ''SENDING''::text%'
);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_interrupted_credential_commands_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_interrupted_credential_commands_idx
    ON human_calling_provider_commands (updated_at, id)
    WHERE call_id IS NULL
        AND action IN ('CREATE_CREDENTIAL', 'DISABLE_CREDENTIAL')
        AND state = 'SENDING';
