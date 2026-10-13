-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.human_calling_provider_commands_call_idx')
        AND indisvalid AND indisready
) AND EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.human_calling_voicemail_commands_idx')
        AND indisvalid AND indisready
) AND EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.human_calling_calls_pending_disposition_idx')
        AND indisvalid AND indisready
);

-- acuity:next-statement
SET lock_timeout = '15s';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_provider_commands_call_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_provider_commands_call_idx
    ON human_calling_provider_commands (call_id, created_at, id);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_voicemail_commands_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_voicemail_commands_idx
    ON human_calling_provider_commands (call_id)
    WHERE action IN ('SPEAK_VOICEMAIL', 'START_VOICEMAIL_RECORDING')
        AND state IN ('PENDING', 'SENDING', 'SENT', 'AMBIGUOUS', 'RECONCILED');

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_calls_pending_disposition_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_calls_pending_disposition_idx
    ON human_calling_calls (id)
    WHERE terminal_outcome = 'ENDED' AND disposition_at IS NULL;

-- acuity:next-statement
RESET lock_timeout;
