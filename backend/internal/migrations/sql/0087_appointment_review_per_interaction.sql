SET LOCAL lock_timeout = '1s';

CREATE OR REPLACE FUNCTION work_preserve_task_source()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        OLD.practice_id,
        OLD.location_id,
        OLD.call_id,
        OLD.phone,
        OLD.urgency,
        OLD.created_by_kind,
        OLD.created_by_subject,
        OLD.created_by_email,
        OLD.created_at,
        OLD.caller_name,
        OLD.source_call_id,
        OLD.source_category,
        OLD.ai_idempotency_key,
        OLD.ai_input_fingerprint,
        OLD.source_message_id,
        OLD.message_thread_id
    ) IS DISTINCT FROM ROW(
        NEW.practice_id,
        NEW.location_id,
        NEW.call_id,
        NEW.phone,
        NEW.urgency,
        NEW.created_by_kind,
        NEW.created_by_subject,
        NEW.created_by_email,
        NEW.created_at,
        NEW.caller_name,
        NEW.source_call_id,
        NEW.source_category,
        NEW.ai_idempotency_key,
        NEW.ai_input_fingerprint,
        NEW.source_message_id,
        NEW.message_thread_id
    ) THEN
        RAISE EXCEPTION 'Task source is immutable';
    END IF;
    IF OLD.source_message IS DISTINCT FROM NEW.source_message
        AND NOT (
            OLD.origin = 'APPOINTMENT_REVIEW'
            AND OLD.state = 'OPEN'
            AND NEW.state = 'OPEN'
            AND NEW.source_message IS NOT NULL
        ) THEN
        RAISE EXCEPTION 'Task source is immutable';
    END IF;
    IF ROW(OLD.origin, OLD.recovery_outcome) IS DISTINCT FROM
        ROW(NEW.origin, NEW.recovery_outcome)
        AND NOT (
            OLD.origin = 'MISSED_CALL_RECOVERY'
            AND OLD.recovery_outcome = 'MISSED_CALL'
            AND NEW.origin = 'VOICEMAIL_RECOVERY'
            AND NEW.recovery_outcome = 'VOICEMAIL'
        ) THEN
        RAISE EXCEPTION 'Task source is immutable';
    END IF;
    RETURN NEW;
END
$$;
