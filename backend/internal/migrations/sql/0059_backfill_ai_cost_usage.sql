-- acuity:no-transaction
-- acuity:complete-if-true
SELECT NOT EXISTS (SELECT 1 FROM ai_interactions WHERE cost_usage_evidence IS NULL)
    AND to_regprocedure('public.backfill_ai_cost_usage_evidence()') IS NULL;

-- acuity:next-statement
CREATE OR REPLACE PROCEDURE backfill_ai_cost_usage_evidence()
LANGUAGE plpgsql AS $$
DECLARE
    last_id uuid;
BEGIN
    LOOP
        PERFORM set_config('lock_timeout', '1s', true);
        WITH batch AS (
            SELECT id FROM ai_interactions
            WHERE cost_usage_evidence IS NULL AND (last_id IS NULL OR id > last_id)
            ORDER BY id LIMIT 100 FOR UPDATE
        ), updated AS (
            UPDATE ai_interactions AS interaction SET cost_usage_evidence = NULL
            FROM batch WHERE interaction.id = batch.id RETURNING interaction.id
        )
        SELECT id INTO last_id FROM updated ORDER BY id DESC LIMIT 1;
        IF NOT FOUND THEN EXIT; END IF;
        COMMIT;
    END LOOP;
END;
$$;

-- acuity:next-statement
-- Committed batches survive cancellation; rerunning resumes missing rows.
SET statement_timeout = '120s';

-- acuity:next-statement
CALL backfill_ai_cost_usage_evidence();

-- acuity:next-statement
RESET statement_timeout;

-- acuity:next-statement
DROP PROCEDURE backfill_ai_cost_usage_evidence();
