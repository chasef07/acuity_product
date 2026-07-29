-- acuity:no-transaction
DROP INDEX CONCURRENTLY IF EXISTS human_calling_active_call_commands_idx;

-- acuity:next-statement
DO $migration$
DECLARE
    duplicate_call_ids text;
BEGIN
    SELECT string_agg(call_id::text, ', ' ORDER BY call_id)
    INTO duplicate_call_ids
    FROM (
        SELECT call_id
        FROM human_calling_provider_commands
        WHERE call_id IS NOT NULL
            AND state IN ('SENDING', 'AMBIGUOUS')
        GROUP BY call_id
        HAVING count(*) > 1
        ORDER BY call_id
        LIMIT 10
    ) duplicates;

    IF duplicate_call_ids IS NOT NULL THEN
        RAISE EXCEPTION 'cannot enforce one active provider command per Call'
            USING
                DETAIL = 'Calls with duplicate active commands: ' || duplicate_call_ids,
                HINT = 'Reconcile duplicate SENDING or AMBIGUOUS commands, then retry the migration.';
    END IF;
END
$migration$;

-- acuity:next-statement
CREATE UNIQUE INDEX CONCURRENTLY human_calling_active_call_commands_idx
    ON human_calling_provider_commands (call_id)
    WHERE call_id IS NOT NULL
        AND state IN ('SENDING', 'AMBIGUOUS');
