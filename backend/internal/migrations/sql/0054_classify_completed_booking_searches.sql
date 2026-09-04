-- Project completed booking availability attempts from stored tool execution
-- evidence. Calls completed as reschedules or cancellations are appointment
-- changes and stay outside the conversion denominator.
-- The precise-evidence column remains as retired schema so applied migrations
-- stay append-only; Product no longer reads its stored value.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

CREATE OR REPLACE FUNCTION ai_interactions_project_booking_facts() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    native_items jsonb;
    historical_tools jsonb;
    outcomes jsonb;
    appointment_type text;
    availability_completed boolean := false;
    new_patient_before_availability boolean := false;
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
    -- Current closeouts use native transcript outputs. Historical closeouts did
    -- not have domainOutcomes and instead recorded completed executions in
    -- toolExecutions, even when their transcript still has a message array.
    IF NEW.closeout_payload ? 'domainOutcomes' OR historical_tools IS NULL THEN
        SELECT EXISTS (
            SELECT 1
            FROM jsonb_array_elements(native_items) WITH ORDINALITY AS availability_call(item, position)
            WHERE lower(availability_call.item ->> 'type') = 'function_call'
                AND lower(availability_call.item ->> 'name') = 'get_availability'
                AND nullif(COALESCE(availability_call.item ->> 'call_id', availability_call.item ->> 'callId'), '') IS NOT NULL
                AND EXISTS (
                    SELECT 1
                    FROM jsonb_array_elements(native_items) AS availability_output(item)
                    WHERE lower(availability_output.item ->> 'type') = 'function_call_output'
                        AND COALESCE(availability_output.item ->> 'call_id', availability_output.item ->> 'callId')
                            = COALESCE(availability_call.item ->> 'call_id', availability_call.item ->> 'callId')
                        AND COALESCE(availability_output.item -> 'is_error', availability_output.item -> 'isError') = 'false'::jsonb
                )
        ) INTO availability_completed;
        SELECT EXISTS (
            SELECT 1
            FROM jsonb_array_elements(native_items) WITH ORDINALITY AS patient_call(item, position)
            JOIN jsonb_array_elements(native_items) WITH ORDINALITY AS availability_call(item, position)
                ON availability_call.position > patient_call.position
            WHERE lower(patient_call.item ->> 'type') = 'function_call'
                AND lower(patient_call.item ->> 'name') = 'add_patient'
                AND lower(availability_call.item ->> 'type') = 'function_call'
                AND lower(availability_call.item ->> 'name') = 'get_availability'
                AND nullif(COALESCE(availability_call.item ->> 'call_id', availability_call.item ->> 'callId'), '') IS NOT NULL
                AND EXISTS (
                    SELECT 1
                    FROM jsonb_array_elements(native_items) AS availability_output(item)
                    WHERE lower(availability_output.item ->> 'type') = 'function_call_output'
                        AND COALESCE(availability_output.item ->> 'call_id', availability_output.item ->> 'callId')
                            = COALESCE(availability_call.item ->> 'call_id', availability_call.item ->> 'callId')
                        AND COALESCE(availability_output.item -> 'is_error', availability_output.item -> 'isError') = 'false'::jsonb
                )
        ) INTO new_patient_before_availability;
    ELSE
        SELECT EXISTS (
            SELECT 1
            FROM jsonb_array_elements(historical_tools) AS availability_call(item)
            WHERE lower(COALESCE(availability_call.item ->> 'toolName', availability_call.item ->> 'tool_name', availability_call.item ->> 'name')) = 'get_availability'
                AND lower(availability_call.item ->> 'status') = 'success'
        ) INTO availability_completed;
        SELECT EXISTS (
            SELECT 1
            FROM jsonb_array_elements(historical_tools) WITH ORDINALITY AS patient_call(item, position)
            JOIN jsonb_array_elements(historical_tools) WITH ORDINALITY AS availability_call(item, position)
                ON availability_call.position > patient_call.position
            WHERE lower(COALESCE(patient_call.item ->> 'toolName', patient_call.item ->> 'tool_name', patient_call.item ->> 'name')) = 'add_patient'
                AND lower(COALESCE(availability_call.item ->> 'toolName', availability_call.item ->> 'tool_name', availability_call.item ->> 'name')) = 'get_availability'
                AND lower(availability_call.item ->> 'status') = 'success'
        ) INTO new_patient_before_availability;
    END IF;
    NEW.booking_confirmed := COALESCE(NEW.appointment_outcome = 'BOOKING'
        AND lower(btrim(NEW.booking_result ->> 'status')) = 'booked'
        AND nullif(NEW.new_appointment_id, '') IS NOT NULL, false);
    NEW.booking_searched := availability_completed
      AND NEW.appointment_outcome IS DISTINCT FROM 'RESCHEDULE'
      AND NEW.appointment_outcome IS DISTINCT FROM 'CANCELLATION';
    NEW.booking_search_known := (native_items IS NOT NULL OR historical_tools IS NOT NULL)
        AND COALESCE(NEW.closeout_payload ->> 'sessionReportUnavailable', 'false') <> 'true';
    NEW.booking_search_precise := false;
    -- Conversion uses the explicit Product rule: add_patient before a completed
    -- availability execution means new; every other completed booking search is
    -- existing. Non-conversion views retain receipt-backed classification.
    NEW.booking_patient_group := CASE
        WHEN NEW.booking_searched AND new_patient_before_availability THEN 'new'
        WHEN NEW.booking_searched THEN 'existing'
        WHEN NEW.booking_confirmed
            AND NEW.booking_result ->> 'appointmentId' = NEW.new_appointment_id
            AND appointment_type IN (
                'new adult medical', 'new pediatric medical', 'new adult vision',
                'new pediatric vision', 'crystal river new patient'
            ) THEN 'new'
        WHEN NEW.booking_confirmed
            AND NEW.booking_result ->> 'appointmentId' = NEW.new_appointment_id
            AND appointment_type IN (
                'established adult medical (follow up)', 'established pediatric medical (follow up)',
                'established adult vision', 'established pediatric vision', 'crystal river established patient',
                'post op', 'crystal river post op'
            ) THEN 'existing'
        -- Without a typed booking receipt, only explicit, non-superseded
        -- patient evidence can classify a call. Switching makes an earlier
        -- unbound identity event ambiguous; absence of creation is not existing.
        WHEN jsonb_path_exists(outcomes, '$[*] ? (@.outcome == "patient_switched" && @.status == "success" && !(@.evidence.superseded == true))') THEN 'unknown'
        WHEN jsonb_path_exists(outcomes, '$[*] ? ((@.outcome == "patient_new" || @.outcome == "patient_created") && @.status == "success" && !(@.evidence.superseded == true))') THEN 'new'
        WHEN jsonb_path_exists(outcomes, '$[*] ? (@.outcome == "patient_verified" && @.status == "success" && !(@.evidence.superseded == true))') THEN 'existing'
        ELSE 'unknown'
    END;
    RETURN NEW;
END;
$$;

-- Reproject rows whose invocation facts migration 0052 could have changed.
UPDATE ai_interactions
SET closeout_payload = closeout_payload
WHERE status <> 'IN_PROGRESS' AND lifecycle_stage = 3
    AND (
        booking_searched IS TRUE
        OR appointment_outcome IN ('RESCHEDULE', 'CANCELLATION')
        OR closeout_payload ->> 'bookingAnalyticsVersion' = '1'
        OR (
            appointment_outcome IS DISTINCT FROM 'BOOKING'
            AND lower(btrim(booking_result ->> 'status')) = 'booked'
            AND nullif(new_appointment_id, '') IS NOT NULL
            AND booking_result ->> 'appointmentId' = new_appointment_id
        )
    );
