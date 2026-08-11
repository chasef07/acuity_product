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
    WHERE table_namespace.nspname = 'public'
        AND table_relation.relname = 'human_calling_provider_commands'
        AND index_relation.relname =
            'human_calling_stale_leg_commands_idx'
        AND index_metadata.indisvalid
        AND index_metadata.indnkeyatts = 3
        AND index_metadata.indnatts = 5
        AND pg_get_indexdef(index_metadata.indexrelid) LIKE
            '%(call_leg_id, created_at, id) INCLUDE (action, payload)%'
        AND pg_get_expr(
            index_metadata.indpred,
            index_metadata.indrelid
        ) LIKE
            '%call_leg_id IS NOT NULL%SENDING%SENT%AMBIGUOUS%'
);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_stale_leg_commands_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_stale_leg_commands_idx
    ON human_calling_provider_commands (call_leg_id, created_at, id)
    INCLUDE (action, payload)
    WHERE call_leg_id IS NOT NULL
        AND state IN ('SENDING', 'SENT', 'AMBIGUOUS');
