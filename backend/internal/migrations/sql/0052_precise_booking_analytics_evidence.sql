-- Distinguish completed booking searches from blocked tool invocations and
-- appointment-change availability. New Agent closeouts carry versioned,
-- patient-scoped evidence. Legacy appointment changes are excluded without
-- interpreting caller-facing prose.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE ai_interactions
    ADD COLUMN booking_search_precise boolean;

CREATE OR REPLACE FUNCTION ai_interactions_project_booking_facts() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    native_items jsonb;
    historical_tools jsonb;
    outcomes jsonb;
    appointment_type text;
    precise_booking_evidence boolean;
    availability_new boolean;
    availability_existing boolean;
BEGIN
    IF NEW.status = 'IN_PROGRESS' OR NEW.lifecycle_stage <> 3 THEN
        NEW.booking_confirmed := NULL;
        NEW.booking_searched := NULL;
        NEW.booking_search_known := NULL;
        NEW.booking_search_precise := NULL;
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
    appointment_type := lower(btrim(NEW.booking_result ->> 'appointmentTypeName'));
    precise_booking_evidence := COALESCE(NEW.closeout_payload ->> 'bookingAnalyticsVersion', '') = '1';
    availability_new := COALESCE(jsonb_path_exists(outcomes,
        '$[*] ? (@.outcome == "availability_searched" && @.status == "success" && @.evidence.patientGroup == "new")'), false);
    availability_existing := COALESCE(jsonb_path_exists(outcomes,
        '$[*] ? (@.outcome == "availability_searched" && @.status == "success" && @.evidence.patientGroup == "existing")'), false);

    NEW.booking_confirmed := COALESCE(NEW.appointment_outcome = 'BOOKING'
        AND lower(btrim(NEW.booking_result ->> 'status')) = 'booked'
        AND nullif(NEW.new_appointment_id, '') IS NOT NULL, false);
    IF precise_booking_evidence THEN
        NEW.booking_searched := COALESCE(jsonb_path_exists(outcomes,
            '$[*] ? (@.outcome == "availability_searched" && @.status == "success" && @.evidence.intent == "booking")'), false)
            AND NEW.appointment_outcome NOT IN ('RESCHEDULE', 'CANCELLATION');
        NEW.booking_search_known := true;
        NEW.booking_search_precise := NEW.booking_searched;
    ELSE
        NEW.booking_searched := (
            COALESCE(jsonb_path_exists(native_items,
                '$[*] ? (@.type == "function_call" && @.name == "get_availability")'), false)
            OR COALESCE(jsonb_path_exists(historical_tools,
                '$[*] ? (@.toolName == "get_availability" || @.tool_name == "get_availability" || @.name == "get_availability")'), false)
        ) AND NEW.appointment_outcome NOT IN ('RESCHEDULE', 'CANCELLATION');
        NEW.booking_search_known := (native_items IS NOT NULL OR historical_tools IS NOT NULL)
            AND COALESCE(NEW.closeout_payload ->> 'sessionReportUnavailable', 'false') <> 'true';
        NEW.booking_search_precise := false;
    END IF;

    -- A matched successful appointment receipt is the most specific evidence,
    -- including a newly booked leg of a reschedule. Numeric EHR type IDs alone
    -- are not global across Practices and never classify a call.
    NEW.booking_patient_group := CASE
        WHEN lower(btrim(NEW.booking_result ->> 'status')) = 'booked'
            AND nullif(NEW.new_appointment_id, '') IS NOT NULL
            AND NEW.booking_result ->> 'appointmentId' = NEW.new_appointment_id
            AND appointment_type IN (
                'new adult medical', 'new pediatric medical', 'new adult vision',
                'new pediatric vision', 'crystal river new patient'
            ) THEN 'new'
        WHEN lower(btrim(NEW.booking_result ->> 'status')) = 'booked'
            AND nullif(NEW.new_appointment_id, '') IS NOT NULL
            AND NEW.booking_result ->> 'appointmentId' = NEW.new_appointment_id
            AND appointment_type IN (
                'established adult medical (follow up)', 'established pediatric medical (follow up)',
                'established adult vision', 'established pediatric vision', 'crystal river established patient',
                'post op', 'crystal river post op'
            ) THEN 'existing'
        WHEN availability_new AND NOT availability_existing THEN 'new'
        WHEN availability_existing AND NOT availability_new THEN 'existing'
        WHEN jsonb_path_exists(outcomes, '$[*] ? (@.outcome == "patient_switched" && @.status == "success" && !(@.evidence.superseded == true))') THEN 'unknown'
        WHEN jsonb_path_exists(outcomes, '$[*] ? ((@.outcome == "patient_new" || @.outcome == "patient_created") && @.status == "success" && !(@.evidence.superseded == true))') THEN 'new'
        WHEN jsonb_path_exists(outcomes, '$[*] ? (@.outcome == "patient_verified" && @.status == "success" && !(@.evidence.superseded == true))') THEN 'existing'
        ELSE 'unknown'
    END;
    RETURN NEW;
END;
$$;

-- Reproject historical appointment changes plus any versioned Agent closeouts
-- that arrived before this Product migration. Future writes use the trigger.
UPDATE ai_interactions
SET closeout_payload = closeout_payload
WHERE status <> 'IN_PROGRESS' AND lifecycle_stage = 3
    AND (
        appointment_outcome IN ('RESCHEDULE', 'CANCELLATION')
        OR closeout_payload ->> 'bookingAnalyticsVersion' = '1'
    );
