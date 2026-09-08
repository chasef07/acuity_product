-- Preserve historical reporting when the lookup telemetry added in 0063 is
-- unavailable. This assumption is distinct from verified identity or a phone match.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE ai_interactions DROP CONSTRAINT ai_interactions_booking_patient_basis_check;
ALTER TABLE ai_interactions ADD CONSTRAINT ai_interactions_booking_patient_basis_check
    CHECK (booking_patient_basis IN (
        'confirmed_new', 'confirmed_existing', 'phone_match', 'assumed_new', 'legacy_existing'
    ));

CREATE OR REPLACE FUNCTION ai_interactions_project_booking_patient_basis() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    outcomes jsonb := CASE WHEN jsonb_typeof(NEW.closeout_payload -> 'domainOutcomes') = 'array'
        THEN NEW.closeout_payload -> 'domainOutcomes' ELSE '[]'::jsonb END;
    last_switch bigint;
BEGIN
    -- Legacy closeouts recorded outcome-specific output classes. A successful
    -- execution alone does not prove an existing patient was identified.
    IF NOT (NEW.closeout_payload ? 'domainOutcomes')
        AND jsonb_typeof(NEW.closeout_payload -> 'toolExecutions') = 'array' THEN
        SELECT COALESCE(jsonb_agg(jsonb_build_object(
            'outcome', item -> 'outputClass', 'status', item -> 'status'
        ) ORDER BY position), '[]'::jsonb) INTO outcomes
        FROM jsonb_array_elements(NEW.closeout_payload -> 'toolExecutions')
            WITH ORDINALITY AS executions(item, position);
    END IF;
    -- A successful switch activates a verified existing patient. Ignore earlier
    -- patient results when a later switch establishes a different active patient.
    SELECT max(position) INTO last_switch
    FROM jsonb_array_elements(outcomes) WITH ORDINALITY AS events(item, position)
    WHERE item ->> 'outcome' = 'patient_switched' AND item ->> 'status' = 'success'
        AND COALESCE(item #> '{evidence,superseded}', 'false'::jsonb) <> 'true'::jsonb;
    IF last_switch IS NOT NULL THEN
        SELECT jsonb_agg(item ORDER BY position) INTO outcomes
        FROM jsonb_array_elements(outcomes) WITH ORDINALITY AS events(item, position)
        WHERE position >= last_switch;
    END IF;
    NEW.booking_patient_basis := CASE
        WHEN jsonb_path_exists(outcomes, '$[*] ? ((@.outcome == "patient_created" || @.outcome == "patient_new") && @.status == "success" && !(@.evidence.superseded == true))') THEN 'confirmed_new'
        WHEN jsonb_path_exists(outcomes, '$[*] ? ((@.outcome == "patient_verified" || @.outcome == "patient_switched") && @.status == "success" && !(@.evidence.superseded == true))') THEN 'confirmed_existing'
        -- A completed identity lookup takes precedence over a household phone match.
        WHEN jsonb_path_exists(outcomes, '$[*] ? (@.outcome == "patient_not_found" && @.status == "success" && !(@.evidence.superseded == true))') THEN 'assumed_new'
        WHEN COALESCE(NEW.closeout_payload #>> '{phoneLookup,status}', NEW.booking_phone_lookup_status) IN ('verified', 'multiple_matches') THEN 'phone_match'
        -- Missing historical telemetry is not a negative lookup. Preserve the
        -- previous reporting category until native or backfilled evidence exists.
        WHEN NEW.closeout_payload #>> '{phoneLookup,status}' IS NULL
            AND NEW.booking_phone_lookup_status IS NULL
            AND NEW.booking_patient_group = 'existing' THEN 'legacy_existing'
        ELSE 'assumed_new'
    END;
    RETURN NEW;
END;
$$;

DROP TRIGGER ai_interactions_booking_patient_basis ON ai_interactions;
CREATE TRIGGER ai_interactions_booking_patient_basis
BEFORE INSERT OR UPDATE OF closeout_payload, booking_phone_lookup_status,
    transcript, booking_result, appointment_outcome, new_appointment_id,
    lifecycle_stage, status, booking_confirmed ON ai_interactions
FOR EACH ROW EXECUTE FUNCTION ai_interactions_project_booking_patient_basis();

-- Reproject only the potentially affected reporting facts. Provider payloads,
-- appointment/search facts, timestamps, and saved lookup evidence remain intact.
UPDATE ai_interactions SET booking_phone_lookup_status = booking_phone_lookup_status
WHERE status <> 'IN_PROGRESS' AND lifecycle_stage = 3
    AND booking_patient_basis = 'assumed_new' AND booking_patient_group = 'existing';
