-- One-time data migration after 0063, with reviewed evidence supplied privately.
-- psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -v practice_id=UUID \
--   -v apply=false -f scripts/backfill-booking-phone-lookups.sql < evidence.csv
-- CSV header: interaction_id,status. Status: verified or multiple_matches.
-- Review the dry-run counts before executing the same command with apply=true.
\set ON_ERROR_STOP on
\if :{?apply}
\else
  \set apply false
\endif
BEGIN;
SET LOCAL statement_timeout = '30s';
SET LOCAL lock_timeout = '1s';
CREATE TEMP TABLE booking_backfill_scope ON COMMIT DROP AS
    SELECT :'practice_id'::uuid AS practice_id;
CREATE TEMP TABLE booking_phone_lookup_backfill (
    interaction_id uuid PRIMARY KEY,
    status text NOT NULL CHECK (status IN ('verified', 'multiple_matches'))
) ON COMMIT DROP;
\copy booking_phone_lookup_backfill (interaction_id,status) FROM pstdin WITH (FORMAT csv, HEADER true)

DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM booking_phone_lookup_backfill) THEN
        RAISE EXCEPTION 'Empty historical lookup evidence';
    END IF;
    IF EXISTS (
        SELECT 1 FROM booking_phone_lookup_backfill evidence
        LEFT JOIN ai_interactions call ON call.id = evidence.interaction_id
        CROSS JOIN booking_backfill_scope scope
        WHERE call.id IS NULL OR call.practice_id <> scope.practice_id
            OR call.status = 'IN_PROGRESS' OR call.lifecycle_stage <> 3
    ) THEN
        RAISE EXCEPTION 'Evidence must match completed calls in the selected Practice';
    END IF;
    IF EXISTS (
        SELECT 1 FROM booking_phone_lookup_backfill evidence
        JOIN ai_interactions call ON call.id = evidence.interaction_id
        WHERE call.booking_phone_lookup_status IS NOT NULL
            AND call.booking_phone_lookup_status <> evidence.status
    ) THEN
        RAISE EXCEPTION 'Evidence conflicts with an already applied historical assumption';
    END IF;
END $$;

-- This reporting-only column does not rewrite provider closeouts or booking facts.
-- Preserve native lookup evidence and make repeated applications no-ops.
WITH changed AS (
    UPDATE ai_interactions call
    SET booking_phone_lookup_status = evidence.status
    FROM booking_phone_lookup_backfill evidence, booking_backfill_scope scope
    WHERE call.id = evidence.interaction_id AND call.practice_id = scope.practice_id
        AND call.booking_phone_lookup_status IS NULL
        AND call.closeout_payload #>> '{phoneLookup,status}' IS NULL
    RETURNING call.booking_patient_basis
)
SELECT booking_patient_basis, count(*) AS affected_calls FROM changed
GROUP BY booking_patient_basis ORDER BY booking_patient_basis;

\if :apply
COMMIT;
\else
ROLLBACK;
\echo 'Dry run rolled back. Nothing persisted.'
\endif
