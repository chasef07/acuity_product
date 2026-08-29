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
        AND table_relation.relname = 'ai_interaction_receipts'
        AND index_relation.relname = 'ai_interaction_pending_receipts_idx'
        AND index_metadata.indisvalid
        AND index_metadata.indnkeyatts = 2
        AND index_metadata.indnatts = 2
        AND pg_get_indexdef(index_metadata.indexrelid) LIKE
            '%(received_at, id)%'
        AND pg_get_expr(
            index_metadata.indpred,
            index_metadata.indrelid
        ) LIKE '%PENDING%kind%START%OUTCOME_CHECKPOINT%CLOSEOUT%'
        AND pg_get_expr(
            index_metadata.indpred,
            index_metadata.indrelid
        ) NOT LIKE '%SUMMARY%'
);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS ai_interaction_pending_receipts_idx;

-- acuity:next-statement
-- Historical SUMMARY receipts remain immutable audit evidence. This index is
-- the runtime and operational backlog: only supported lifecycle messages are
-- eligible for projection or zero-backlog release gates.
CREATE INDEX CONCURRENTLY ai_interaction_pending_receipts_idx
    ON ai_interaction_receipts (received_at, id)
    WHERE state = 'PENDING'
        AND kind IN ('START', 'OUTCOME_CHECKPOINT', 'CLOSEOUT');
