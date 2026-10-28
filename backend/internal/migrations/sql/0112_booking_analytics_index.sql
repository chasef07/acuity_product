-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.ai_interactions_booking_analytics_idx')
        AND indisvalid AND indisready
) AND to_regclass('public.ai_interactions_booking_facts_idx') IS NULL;

-- acuity:next-statement
SET lock_timeout = '15s';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS ai_interactions_booking_analytics_idx;

-- acuity:next-statement
-- Covers the booking analytics query before 0113 drops the retired search columns.
CREATE INDEX CONCURRENTLY ai_interactions_booking_analytics_idx
    ON ai_interactions (practice_id, started_at, id)
    INCLUDE (location_id, ended_at, new_appointment_id, booking_confirmed,
        booking_searched, booking_patient_basis)
    WHERE status <> 'IN_PROGRESS' AND lifecycle_stage = 3;

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS ai_interactions_booking_facts_idx;

-- acuity:next-statement
RESET lock_timeout;
