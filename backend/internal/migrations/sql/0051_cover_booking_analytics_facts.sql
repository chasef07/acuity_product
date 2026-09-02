-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.ai_interactions_booking_facts_idx')
        AND indisvalid AND indisready AND indnatts = 10
);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS ai_interactions_booking_facts_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY ai_interactions_booking_facts_idx
    ON ai_interactions (practice_id, started_at, id)
    INCLUDE (location_id, ended_at, new_appointment_id, booking_confirmed,
        booking_searched, booking_search_known, booking_patient_group)
    WHERE status <> 'IN_PROGRESS' AND lifecycle_stage = 3;
