SET LOCAL lock_timeout = '1s';

CREATE FUNCTION human_calling_advance_call_version() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.version := OLD.version;
    IF NOT EXISTS (
        SELECT 1 FROM human_calling_calls
        WHERE id = OLD.id AND xmin = pg_current_xact_id()::xid
    ) THEN
        NEW.version := OLD.version + 1;
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION human_calling_publish_call_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' OR NEW.version <> OLD.version THEN
        PERFORM pg_notify(
            'acuity_calling_hints',
            json_build_object('practiceId', NEW.practice_id::text)::text
        );
    END IF;
    RETURN NULL;
END;
$$;

CREATE FUNCTION human_calling_touch_call() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    child jsonb := to_jsonb(NEW);
BEGIN
    IF TG_OP = 'UPDATE' AND child - TG_ARGV = to_jsonb(OLD) - TG_ARGV THEN
        RETURN NULL;
    END IF;
    UPDATE human_calling_calls
    SET updated_at = GREATEST(updated_at, (child->>'updated_at')::timestamptz)
    WHERE id = (child->>'call_id')::uuid
        AND xmin <> pg_current_xact_id()::xid;
    RETURN NULL;
END;
$$;

CREATE FUNCTION human_calling_publish_softphone_takeover() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'acuity_calling_hints',
        json_build_object('practiceId', scope.practice_id::text)::text
    )
    FROM access_operational_scopes scope
    WHERE scope.user_subject = NEW.user_subject;
    RETURN NULL;
END;
$$;

CREATE TRIGGER human_calling_calls_version
BEFORE UPDATE ON human_calling_calls
FOR EACH ROW EXECUTE FUNCTION human_calling_advance_call_version();

CREATE TRIGGER human_calling_calls_publish
AFTER INSERT OR UPDATE ON human_calling_calls
FOR EACH ROW EXECUTE FUNCTION human_calling_publish_call_change();

CREATE CONSTRAINT TRIGGER human_calling_call_legs_touch_call
AFTER INSERT OR UPDATE ON human_calling_call_legs
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION human_calling_touch_call(
    'updated_at', 'staff_session_id', 'reconciliation_checked_at',
    'reconciliation_next_attempt_at', 'reconciliation_attempts',
    'reconciliation_error_code'
);

CREATE CONSTRAINT TRIGGER human_calling_staff_transfers_touch_call
AFTER INSERT OR UPDATE ON human_calling_staff_transfers
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION human_calling_touch_call('updated_at');

CREATE CONSTRAINT TRIGGER human_calling_voicemails_touch_call
AFTER INSERT OR UPDATE ON human_calling_voicemails
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION human_calling_touch_call('updated_at');

CREATE CONSTRAINT TRIGGER human_calling_call_recordings_touch_call
AFTER INSERT OR UPDATE ON human_calling_call_recordings
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION human_calling_touch_call(
    'updated_at', 'reconciliation_attempts', 'reconciliation_claimed_at',
    'next_reconciliation_attempt_at', 'reconciliation_error_code',
    'deletion_attempts', 'deletion_claimed_at', 'next_deletion_attempt_at',
    'deletion_error_code'
);

CREATE CONSTRAINT TRIGGER human_calling_provider_commands_insert_touch_call
AFTER INSERT ON human_calling_provider_commands
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW WHEN (NEW.action IN (
    'START_RING_WINDOW', 'DIAL_OUTBOUND_STAFF',
    'SPEAK_VOICEMAIL', 'START_VOICEMAIL_RECORDING'
))
EXECUTE FUNCTION human_calling_touch_call();

CREATE CONSTRAINT TRIGGER human_calling_provider_commands_update_touch_call
AFTER UPDATE ON human_calling_provider_commands
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW WHEN (
    NEW.action IN (
        'START_RING_WINDOW', 'DIAL_OUTBOUND_STAFF',
        'SPEAK_VOICEMAIL', 'START_VOICEMAIL_RECORDING'
    )
    AND (
        OLD.sent_at IS DISTINCT FROM NEW.sent_at
        OR (OLD.state IN ('SENT', 'RECONCILED')) <> (NEW.state IN ('SENT', 'RECONCILED'))
        OR (OLD.state = 'FAILED') <> (NEW.state = 'FAILED')
    )
)
EXECUTE FUNCTION human_calling_touch_call();

CREATE CONSTRAINT TRIGGER human_calling_timeline_touch_call
AFTER INSERT ON human_calling_timeline
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW WHEN (NEW.kind = 'call.hangup.requested')
EXECUTE FUNCTION human_calling_touch_call();

CREATE TRIGGER human_calling_softphone_leases_publish_takeover
AFTER UPDATE ON human_calling_softphone_leases
FOR EACH ROW WHEN (OLD.session_id IS DISTINCT FROM NEW.session_id)
EXECUTE FUNCTION human_calling_publish_softphone_takeover();
