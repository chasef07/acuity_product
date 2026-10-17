-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid=to_regclass('public.ai_interaction_classifications_pending_idx')
        AND indisvalid AND indisready
);

-- acuity:next-statement
SET lock_timeout = '15s';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS ai_interaction_classifications_pending_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY ai_interaction_classifications_pending_idx
    ON ai_interaction_classifications(next_attempt_at, interaction_id)
    WHERE status IN ('pending', 'processing', 'failed');

-- acuity:next-statement
RESET lock_timeout;
