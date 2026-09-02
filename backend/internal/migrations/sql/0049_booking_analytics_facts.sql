-- Store derived facts with the source evidence so reports never parse transcripts.
-- The trigger also covers writes from overlapping older application revisions.
SET LOCAL lock_timeout = '1s';
ALTER TABLE ai_interactions
    ADD COLUMN booking_confirmed boolean,
    ADD COLUMN booking_searched boolean,
    ADD COLUMN booking_search_known boolean,
    ADD COLUMN booking_patient_group text;

CREATE FUNCTION ai_interactions_project_booking_facts() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    native_items jsonb;
    historical_tools jsonb;
    outcomes jsonb;
BEGIN
    IF NEW.status = 'IN_PROGRESS' OR NEW.lifecycle_stage <> 3 THEN
        NEW.booking_confirmed := NULL;
        NEW.booking_searched := NULL;
        NEW.booking_search_known := NULL;
        NEW.booking_patient_group := NULL;
        RETURN NEW;
    END IF;
    native_items := CASE
        WHEN jsonb_typeof(NEW.transcript #> '{chat_history,items}') = 'array' THEN NEW.transcript #> '{chat_history,items}'
        WHEN jsonb_typeof(NEW.transcript #> '{chatHistory,items}') = 'array' THEN NEW.transcript #> '{chatHistory,items}'
        WHEN jsonb_typeof(NEW.transcript -> 'items') = 'array' THEN NEW.transcript -> 'items'
    END;
    historical_tools := CASE WHEN jsonb_typeof(NEW.closeout_payload -> 'toolExecutions') = 'array'
        THEN NEW.closeout_payload -> 'toolExecutions' END;
    outcomes := COALESCE(NEW.closeout_payload -> 'domainOutcomes', '[]'::jsonb);
    NEW.booking_confirmed := COALESCE(NEW.appointment_outcome = 'BOOKING'
        AND lower(btrim(NEW.booking_result ->> 'status')) = 'booked'
        AND nullif(NEW.new_appointment_id, '') IS NOT NULL, false);
    NEW.booking_searched := COALESCE(jsonb_path_exists(native_items,
        '$[*] ? (@.type == "function_call" && @.name == "get_availability")'), false)
        OR COALESCE(jsonb_path_exists(historical_tools,
        '$[*] ? (@.toolName == "get_availability" || @.tool_name == "get_availability" || @.name == "get_availability")'), false);
    NEW.booking_search_known := (native_items IS NOT NULL OR historical_tools IS NOT NULL)
        AND COALESCE(NEW.closeout_payload ->> 'sessionReportUnavailable', 'false') <> 'true';
    NEW.booking_patient_group := CASE
        WHEN jsonb_path_exists(outcomes, '$[*] ? (@.outcome == "patient_switched" && @.status == "success")') THEN 'unknown'
        WHEN jsonb_path_exists(outcomes, '$[*] ? (@.outcome == "patient_new" && @.status == "success")') THEN 'new'
        WHEN jsonb_path_exists(outcomes, '$[*] ? (@.outcome == "patient_created" && @.status == "success")') THEN 'unknown'
        WHEN jsonb_path_exists(outcomes, '$[*] ? (@.outcome == "patient_verified" && @.status == "success")') THEN 'existing'
        ELSE 'unknown'
    END;
    RETURN NEW;
END;
$$;

CREATE TRIGGER ai_interactions_booking_facts
BEFORE INSERT OR UPDATE OF transcript, closeout_payload, booking_result,
    appointment_outcome, new_appointment_id, lifecycle_stage, status, booking_confirmed
ON ai_interactions FOR EACH ROW
EXECUTE FUNCTION ai_interactions_project_booking_facts();
