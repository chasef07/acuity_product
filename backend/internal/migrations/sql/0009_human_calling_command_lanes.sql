-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1
    FROM pg_index index_metadata
    JOIN pg_class index_relation
        ON index_relation.oid = index_metadata.indexrelid
    JOIN pg_class table_relation
        ON table_relation.oid = index_metadata.indrelid
    JOIN pg_namespace table_namespace
        ON table_namespace.oid = table_relation.relnamespace
    JOIN pg_attribute indexed_column
        ON indexed_column.attrelid = table_relation.oid
        AND indexed_column.attnum = index_metadata.indkey[0]
    WHERE table_namespace.nspname = 'public'
        AND table_relation.relname = 'human_calling_provider_commands'
        AND index_relation.relname = 'human_calling_active_call_commands_idx'
        AND index_metadata.indisvalid
        AND index_metadata.indisunique
        AND index_metadata.indnkeyatts = 1
        AND index_metadata.indnatts = 1
        AND indexed_column.attname = 'call_id'
        AND pg_get_expr(
            index_metadata.indpred,
            index_metadata.indrelid
        ) = $predicate$((call_id IS NOT NULL) AND (state = ANY (ARRAY['SENDING'::text, 'AMBIGUOUS'::text])))$predicate$
);

-- acuity:next-statement
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
