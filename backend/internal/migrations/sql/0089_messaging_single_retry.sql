-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.messaging_messages_retry_of_message_idx')
        AND indisvalid AND indisready AND indisunique
);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS messaging_messages_retry_of_message_idx;

-- acuity:next-statement
CREATE UNIQUE INDEX CONCURRENTLY messaging_messages_retry_of_message_idx
    ON messaging_messages (retry_of_message_id)
    WHERE retry_of_message_id IS NOT NULL;
