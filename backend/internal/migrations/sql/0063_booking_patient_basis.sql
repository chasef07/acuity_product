-- Reporting assumptions are separate from verified patient identity and the
-- historical booking_patient_group projection used by older application versions.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

-- Historical phone-match assumptions are separate from provider closeout evidence.
-- Filled only by the explicit release backfill, never by schema migration network I/O.
ALTER TABLE ai_interactions ADD COLUMN booking_phone_lookup_status text
    CHECK (booking_phone_lookup_status IN ('verified', 'multiple_matches'));

ALTER TABLE ai_interactions ADD COLUMN booking_patient_basis text NOT NULL DEFAULT 'assumed_new'
    CHECK (booking_patient_basis IN ('confirmed_new', 'confirmed_existing', 'phone_match', 'assumed_new'));

CREATE FUNCTION ai_interactions_project_booking_patient_basis() RETURNS trigger
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
        ELSE 'assumed_new'
    END;
    RETURN NEW;
END;
$$;

CREATE TRIGGER ai_interactions_booking_patient_basis
BEFORE INSERT OR UPDATE OF closeout_payload, booking_phone_lookup_status ON ai_interactions
FOR EACH ROW EXECUTE FUNCTION ai_interactions_project_booking_patient_basis();

-- Historical calls lacking lookup evidence follow the explicit assume-new rule.
-- No timestamp correlation or external log queries run in the application.
UPDATE ai_interactions SET booking_phone_lookup_status = booking_phone_lookup_status
WHERE status <> 'IN_PROGRESS' AND lifecycle_stage = 3;
