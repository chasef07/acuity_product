-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.ai_interactions_analytics_facts_pending_idx')
        AND indisvalid AND indisready
);

-- acuity:next-statement
SET lock_timeout = '15s';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS ai_interactions_analytics_facts_pending_idx;

-- acuity:next-statement
-- The facts worker claims missing or older-version facts newest first.
CREATE INDEX CONCURRENTLY ai_interactions_analytics_facts_pending_idx
    ON ai_interactions (analytics_facts_version, started_at DESC);

-- acuity:next-statement
RESET lock_timeout;
