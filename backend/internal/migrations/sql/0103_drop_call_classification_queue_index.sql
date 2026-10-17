-- acuity:no-transaction
SET lock_timeout = '1s';
-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS ai_interaction_classifications_pending_idx;
-- acuity:next-statement
RESET lock_timeout;
