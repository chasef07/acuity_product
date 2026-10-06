-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.messaging_messages_inbound_recent_idx')
        AND indisvalid AND indisready
) AND EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.ai_interactions_started_recent_idx')
        AND indisvalid AND indisready
);

-- acuity:next-statement
SET lock_timeout = '15s';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS messaging_messages_inbound_recent_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY messaging_messages_inbound_recent_idx
    ON messaging_messages (thread_id, created_at DESC, id DESC)
    WHERE direction = 'INBOUND';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS ai_interactions_started_recent_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY ai_interactions_started_recent_idx
    ON ai_interactions (practice_id, started_at DESC, id DESC)
    INCLUDE (location_id);

-- acuity:next-statement
RESET lock_timeout;
