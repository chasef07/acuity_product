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
        AND table_relation.relname = 'human_calling_provider_receipts'
        AND index_relation.relname =
            'human_calling_quarantined_receipts_idx'
        AND index_metadata.indisvalid
        AND NOT index_metadata.indisunique
        AND index_metadata.indnkeyatts = 1
        AND index_metadata.indnatts = 1
        AND indexed_column.attname = 'quarantined_at'
        AND pg_get_expr(
            index_metadata.indpred,
            index_metadata.indrelid
        ) = $predicate$(state = 'QUARANTINED'::text)$predicate$
);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_quarantined_receipts_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_quarantined_receipts_idx
    ON human_calling_provider_receipts (quarantined_at)
    WHERE state = 'QUARANTINED';
