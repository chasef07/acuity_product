-- acuity:no-transaction
-- acuity:complete-if-true
-- An AMBIGUOUS Start or Stop ring window is uncertain caller ringback audio,
-- resolved only by its playback event or the Call's end. It no longer holds
-- the Call's command lane, so transfers, bridges, and hangups still send.
-- 0124 drops the index this one replaces.
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.human_calling_call_lane_commands_idx')
        AND indisvalid AND indisready AND indisunique
);

-- acuity:next-statement
SET lock_timeout = '15s';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_call_lane_commands_idx;

-- acuity:next-statement
CREATE UNIQUE INDEX CONCURRENTLY human_calling_call_lane_commands_idx
    ON human_calling_provider_commands (call_id)
    WHERE call_id IS NOT NULL
        AND action <> 'DIAL_STAFF'
        AND (
            state = 'SENDING'
            OR (
                state = 'AMBIGUOUS'
                AND action NOT IN ('START_RING_WINDOW', 'STOP_RING_WINDOW')
            )
        );

-- acuity:next-statement
RESET lock_timeout;
