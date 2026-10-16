SET LOCAL lock_timeout = '1s';

-- One source of truth for both choice judges and recoverable worker execution.
-- Existing calls are intentionally not enqueued by this migration.
CREATE TABLE ai_interaction_classifications (
    interaction_id uuid PRIMARY KEY REFERENCES ai_interactions(id) ON DELETE CASCADE,
    intent text CHECK (intent IN (
        'new_patient_booking', 'existing_patient_booking', 'reschedule_appointment',
        'cancel_appointment', 'appointment_information', 'office_information',
        'insurance_question', 'billing_question', 'prescription_or_refill',
        'records_or_forms', 'glasses_or_contacts', 'returning_call', 'speak_to_staff', 'other'
    )),
    outcome text CHECK (outcome IN ('resolved', 'transferred', 'abandoned')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'complete', 'failed', 'skipped')),
    label_version text NOT NULL DEFAULT 'call-labels-v1',
    model text NOT NULL DEFAULT 'typesafe/jev-1.14',
    raw_judgments jsonb,
    error_code text,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    claim_token uuid,
    lease_expires_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'processing') = (claim_token IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CHECK (status <> 'complete' OR (intent IS NOT NULL AND outcome IS NOT NULL))
);

-- Ingestion roles cannot write judge results. The trigger owns only enqueueing.
CREATE FUNCTION enqueue_ai_call_classification() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    IF NEW.lifecycle_stage = 3 AND NEW.closeout_payload IS NOT NULL AND (
        TG_OP = 'INSERT' OR NEW.transcript IS DISTINCT FROM OLD.transcript
        OR NEW.closeout_payload IS DISTINCT FROM OLD.closeout_payload
        OR NEW.lifecycle_stage IS DISTINCT FROM OLD.lifecycle_stage
        OR NEW.ended_at IS DISTINCT FROM OLD.ended_at
    ) THEN
        INSERT INTO public.ai_interaction_classifications(interaction_id) VALUES (NEW.id)
        ON CONFLICT (interaction_id) DO UPDATE SET
            intent = NULL, outcome = NULL, status = 'pending', attempts = 0,
            raw_judgments = NULL, error_code = NULL, claim_token = NULL,
            lease_expires_at = NULL, next_attempt_at = now(), updated_at = now();
    END IF;
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION enqueue_ai_call_classification() FROM PUBLIC;
CREATE TRIGGER ai_call_classification_enqueue
AFTER INSERT OR UPDATE OF transcript, closeout_payload, lifecycle_stage, ended_at
ON ai_interactions FOR EACH ROW EXECUTE FUNCTION enqueue_ai_call_classification();

-- A late provider fact invalidates an earlier result without enqueuing history.
CREATE FUNCTION refresh_ai_call_classification_from_call() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE calling_id uuid;
BEGIN
    IF TG_TABLE_NAME = 'human_calling_call_legs' THEN
        IF TG_OP = 'UPDATE' AND NEW.bridged_at IS NOT DISTINCT FROM OLD.bridged_at
            AND NEW.ended_at IS NOT DISTINCT FROM OLD.ended_at AND NEW.state IS NOT DISTINCT FROM OLD.state THEN
            RETURN NEW;
        END IF;
        calling_id := NEW.call_id;
    ELSE
        IF TG_OP = 'UPDATE' AND NEW.ended_at IS NOT DISTINCT FROM OLD.ended_at
            AND NEW.terminal_outcome IS NOT DISTINCT FROM OLD.terminal_outcome THEN
            RETURN NEW;
        END IF;
        calling_id := NEW.id;
    END IF;
    UPDATE public.ai_interaction_classifications labels SET
        intent=NULL, outcome=NULL, status='pending', attempts=0, error_code=NULL,
        raw_judgments=NULL, claim_token=NULL, lease_expires_at=NULL,
        next_attempt_at=now(), updated_at=now()
    FROM public.ai_interactions interaction
    JOIN public.human_calling_handoffs handoff ON handoff.practice_id=interaction.practice_id
        AND handoff.service_subject=interaction.service_subject AND handoff.source_call_id=interaction.source_call_id
    JOIN public.human_calling_calls call ON call.source_handoff_id=handoff.id AND call.practice_id=interaction.practice_id
    WHERE labels.interaction_id=interaction.id AND call.id=calling_id;
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION refresh_ai_call_classification_from_call() FROM PUBLIC;
CREATE TRIGGER ai_call_classification_call_changed AFTER INSERT OR UPDATE OF ended_at, terminal_outcome
ON human_calling_calls FOR EACH ROW EXECUTE FUNCTION refresh_ai_call_classification_from_call();
CREATE TRIGGER ai_call_classification_leg_changed AFTER INSERT OR UPDATE OF bridged_at, ended_at, state
ON human_calling_call_legs FOR EACH ROW EXECUTE FUNCTION refresh_ai_call_classification_from_call();
