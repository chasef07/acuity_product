-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.human_calling_open_calls_idx')
        AND indisvalid AND indisready
) AND EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.human_calling_terminal_leg_commands_idx')
        AND indisvalid AND indisready
) AND EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.human_calling_ambiguous_credential_commands_idx')
        AND indisvalid AND indisready
) AND EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.human_calling_provider_commands_call_leg_idx')
        AND indisvalid AND indisready
) AND EXISTS (
    SELECT 1 FROM pg_stats_ext
    WHERE statistics_name = 'human_calling_provider_commands_action_state_stats'
        AND most_common_vals IS NOT NULL
);

-- acuity:next-statement
SET lock_timeout = '15s';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_open_calls_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_open_calls_idx
    ON human_calling_calls (id)
    WHERE terminal_outcome IS NULL;

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_terminal_leg_commands_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_terminal_leg_commands_idx
    ON human_calling_provider_commands (call_leg_id)
    WHERE call_leg_id IS NOT NULL
        AND (
            (
                action IN ('HANGUP_LEG', 'STOP_RING_WINDOW')
                AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
            )
            OR (action = 'STOP_RING_WINDOW' AND state = 'FAILED')
        );

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_ambiguous_credential_commands_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_ambiguous_credential_commands_idx
    ON human_calling_provider_commands (updated_at, id)
    WHERE state = 'AMBIGUOUS'
        AND action IN ('CREATE_CREDENTIAL', 'DISABLE_CREDENTIAL');

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_provider_commands_call_leg_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_provider_commands_call_leg_idx
    ON human_calling_provider_commands (call_leg_id)
    WHERE call_leg_id IS NOT NULL;

-- acuity:next-statement
CREATE STATISTICS IF NOT EXISTS human_calling_provider_commands_action_state_stats (mcv)
    ON action, state FROM human_calling_provider_commands;

-- acuity:next-statement
ANALYZE human_calling_provider_commands;

-- acuity:next-statement
RESET lock_timeout;
