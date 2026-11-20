-- acuity:no-transaction
-- acuity:complete-if-true
-- 0123's human_calling_call_lane_commands_idx replaces this index.
SELECT to_regclass('public.human_calling_active_call_commands_idx') IS NULL;

-- acuity:next-statement
SET lock_timeout = '15s';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_active_call_commands_idx;

-- acuity:next-statement
RESET lock_timeout;
