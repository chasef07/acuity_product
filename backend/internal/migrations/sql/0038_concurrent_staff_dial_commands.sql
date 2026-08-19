-- acuity:no-transaction
-- acuity:complete-if-true
SELECT
    EXISTS (
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
            ) = $predicate$((call_id IS NOT NULL) AND (action <> 'DIAL_STAFF'::text) AND (state = ANY (ARRAY['SENDING'::text, 'AMBIGUOUS'::text])))$predicate$
    )
    AND EXISTS (
        SELECT 1
        FROM pg_index index_metadata
        JOIN pg_class index_relation
            ON index_relation.oid = index_metadata.indexrelid
        JOIN pg_class table_relation
            ON table_relation.oid = index_metadata.indrelid
        JOIN pg_namespace table_namespace
            ON table_namespace.oid = table_relation.relnamespace
        JOIN pg_attribute first_indexed_column
            ON first_indexed_column.attrelid = table_relation.oid
            AND first_indexed_column.attnum = index_metadata.indkey[0]
        JOIN pg_attribute second_indexed_column
            ON second_indexed_column.attrelid = table_relation.oid
            AND second_indexed_column.attnum = index_metadata.indkey[1]
        WHERE table_namespace.nspname = 'public'
            AND table_relation.relname = 'human_calling_provider_commands'
            AND index_relation.relname = 'human_calling_active_staff_dial_commands_idx'
            AND index_metadata.indisvalid
            AND index_metadata.indisunique
            AND index_metadata.indnkeyatts = 2
            AND index_metadata.indnatts = 2
            AND first_indexed_column.attname = 'call_id'
            AND second_indexed_column.attname = 'call_leg_id'
            AND pg_get_expr(
                index_metadata.indpred,
                index_metadata.indrelid
            ) = $predicate$((call_id IS NOT NULL) AND (call_leg_id IS NOT NULL) AND (action = 'DIAL_STAFF'::text) AND (state = ANY (ARRAY['SENDING'::text, 'AMBIGUOUS'::text])))$predicate$
    );

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_active_call_commands_idx;

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_active_staff_dial_commands_idx;

-- acuity:next-statement
CREATE UNIQUE INDEX CONCURRENTLY human_calling_active_call_commands_idx
    ON human_calling_provider_commands (call_id)
    WHERE call_id IS NOT NULL
        AND action <> 'DIAL_STAFF'
        AND state IN ('SENDING', 'AMBIGUOUS');

-- acuity:next-statement
CREATE UNIQUE INDEX CONCURRENTLY human_calling_active_staff_dial_commands_idx
    ON human_calling_provider_commands (call_id, call_leg_id)
    WHERE call_id IS NOT NULL
        AND call_leg_id IS NOT NULL
        AND action = 'DIAL_STAFF'
        AND state IN ('SENDING', 'AMBIGUOUS');
