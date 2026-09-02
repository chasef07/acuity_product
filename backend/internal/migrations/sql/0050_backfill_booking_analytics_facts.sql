-- acuity:no-transaction
-- acuity:complete-if-true
SELECT NOT EXISTS (
    SELECT 1 FROM ai_interactions
    WHERE status <> 'IN_PROGRESS' AND lifecycle_stage = 3 AND booking_confirmed IS NULL
) AND to_regprocedure('public.backfill_booking_analytics_facts()') IS NULL;

-- acuity:next-statement
CREATE OR REPLACE PROCEDURE backfill_booking_analytics_facts()
LANGUAGE plpgsql AS $$
DECLARE
    last_id uuid;
BEGIN
    LOOP
        PERFORM set_config('lock_timeout', '1s', true);
        WITH batch AS (
            SELECT id FROM ai_interactions
            WHERE status <> 'IN_PROGRESS' AND lifecycle_stage = 3
                AND booking_confirmed IS NULL AND (last_id IS NULL OR id > last_id)
            ORDER BY id LIMIT 500 FOR UPDATE
        ), updated AS (
            UPDATE ai_interactions AS interaction SET booking_confirmed = NULL
            FROM batch WHERE interaction.id = batch.id RETURNING interaction.id
        )
        SELECT id INTO last_id FROM updated ORDER BY id DESC LIMIT 1;
        IF NOT FOUND THEN EXIT; END IF;
        COMMIT;
    END LOOP;
END;
$$;

-- acuity:next-statement
-- Bound the entire CALL on its pinned session. Each committed batch survives
-- timeout or cancellation, so a later migration attempt resumes its progress.
SET statement_timeout = '120s';

-- acuity:next-statement
CALL backfill_booking_analytics_facts();

-- acuity:next-statement
RESET statement_timeout;

-- acuity:next-statement
DROP PROCEDURE backfill_booking_analytics_facts();
