SET LOCAL lock_timeout = '1s';

-- Labels decided by the S2S jury arrive as call-labels-v2 from model "jury".
CREATE OR REPLACE FUNCTION project_ai_call_classification() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    classification jsonb;
    supplied_intent text;
    supplied_outcome text;
    projected_status text := 'failed';
    unavailable_reason text := 'invalid_classification';
BEGIN
    IF NEW.lifecycle_stage <> 3 OR NEW.closeout_payload IS NULL THEN
        RETURN NEW;
    END IF;
    classification := NEW.closeout_payload #> '{evaluation,classification}';
    IF TG_OP = 'UPDATE' AND OLD.lifecycle_stage = 3 AND OLD.closeout_payload IS NOT NULL
        AND classification IS NOT DISTINCT FROM (OLD.closeout_payload #> '{evaluation,classification}') THEN
        RETURN NEW;
    END IF;
    IF classification IS NULL THEN
        projected_status := 'skipped';
        unavailable_reason := 'classification_not_supplied';
    ELSIF jsonb_typeof(classification) = 'object' THEN
        IF jsonb_typeof(classification->'intent') = 'string' AND classification->>'intent' IN (
            'new_patient_booking', 'existing_patient_booking', 'reschedule_appointment',
            'cancel_appointment', 'appointment_information', 'office_information',
            'insurance_question', 'billing_question', 'prescription_or_refill',
            'records_or_forms', 'glasses_or_contacts', 'returning_call', 'speak_to_staff', 'other'
        ) THEN
            supplied_intent := classification->>'intent';
        END IF;
        IF jsonb_typeof(classification->'outcome') = 'string'
            AND classification->>'outcome' IN ('resolved', 'transferred', 'abandoned') THEN
            supplied_outcome := classification->>'outcome';
        END IF;
        IF (classification->>'version', classification->>'model') IN (('call-labels-v1', 'typesafe/jev-1.14'), ('call-labels-v2', 'jury'))
            AND classification->>'status' IN ('complete', 'incomplete', 'skipped') THEN
            IF classification->>'status' = 'complete' AND supplied_intent IS NOT NULL AND supplied_outcome IS NOT NULL THEN
                projected_status := 'complete';
                unavailable_reason := NULL;
            ELSIF classification->>'status' IN ('incomplete', 'skipped') THEN
                projected_status := CASE classification->>'status' WHEN 'skipped' THEN 'skipped' ELSE 'failed' END;
                unavailable_reason := COALESCE(NULLIF(classification->>'reason', ''), 'classification_' || (classification->>'status'));
            END IF;
        ELSE
            supplied_intent := NULL;
            supplied_outcome := NULL;
        END IF;
    END IF;
    INSERT INTO public.ai_interaction_classifications (
        interaction_id, intent, outcome, status, label_version, model, raw_judgments, error_code
    ) VALUES (
        NEW.id, supplied_intent, supplied_outcome, projected_status,
        COALESCE(NULLIF(classification->>'version', ''), 'call-labels-v1'),
        COALESCE(NULLIF(classification->>'model', ''), 'typesafe/jev-1.14'),
        classification->'answers', unavailable_reason
    ) ON CONFLICT (interaction_id) DO UPDATE SET
        intent = EXCLUDED.intent, outcome = EXCLUDED.outcome, status = EXCLUDED.status,
        label_version = EXCLUDED.label_version, model = EXCLUDED.model,
        raw_judgments = EXCLUDED.raw_judgments, error_code = EXCLUDED.error_code,
        attempts = 0, claim_token = NULL, lease_expires_at = NULL, updated_at = now();
    RETURN NEW;
END;
$$;
