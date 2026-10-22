SET LOCAL lock_timeout = '1s';

CREATE FUNCTION human_calling_leg_state_rank(state text) RETURNS integer
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE state
        WHEN 'PENDING' THEN 1
        WHEN 'DIALING' THEN 2
        WHEN 'RINGING' THEN 3
        WHEN 'ANSWERED' THEN 4
        WHEN 'BRIDGE_PENDING' THEN 5
        WHEN 'BRIDGED' THEN 6
        WHEN 'ENDING' THEN 7
        WHEN 'FAILED' THEN 8
        WHEN 'ENDED' THEN 9
    END
$$;

CREATE FUNCTION human_calling_call_outcome_advances(from_outcome text, to_outcome text)
RETURNS boolean
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT from_outcome IS NOT DISTINCT FROM to_outcome
        OR (COALESCE(from_outcome, 'NULL'), COALESCE(to_outcome, 'NULL')) IN (
            ('NULL', 'ABANDONED'),
            ('NULL', 'ENDED'),
            ('NULL', 'MISSED'),
            ('NULL', 'ROUTING_FAILED'),
            ('NULL', 'UNANSWERED'),
            ('NULL', 'VOICEMAIL'),
            ('ABANDONED', 'ENDED'),
            ('UNANSWERED', 'ENDED'),
            ('MISSED', 'VOICEMAIL'),
            ('ROUTING_FAILED', 'VOICEMAIL'),
            ('ENDED', 'RESOLVED'),
            ('ENDED', 'FOLLOW_UP_REQUIRED')
        )
$$;

CREATE FUNCTION human_calling_guard_call_leg_state() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF human_calling_leg_state_rank(NEW.state) < human_calling_leg_state_rank(OLD.state) THEN
        RAISE WARNING 'HC001 CallLeg % cannot move from % to %', OLD.id, OLD.state, NEW.state;
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION human_calling_guard_call_outcome() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT human_calling_call_outcome_advances(OLD.terminal_outcome, NEW.terminal_outcome) THEN
        RAISE WARNING 'HC002 Call % cannot move terminal_outcome from % to %', OLD.id,
            COALESCE(OLD.terminal_outcome, 'NULL'), COALESCE(NEW.terminal_outcome, 'NULL');
    END IF;
    IF OLD.disposition_outcome IS NOT NULL
        AND NEW.disposition_outcome IS DISTINCT FROM OLD.disposition_outcome THEN
        RAISE WARNING 'HC002 Call % cannot move disposition_outcome from % to %', OLD.id,
            OLD.disposition_outcome, COALESCE(NEW.disposition_outcome, 'NULL');
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER human_calling_call_legs_state_guard
BEFORE UPDATE OF state ON human_calling_call_legs
FOR EACH ROW WHEN (OLD.state IS DISTINCT FROM NEW.state)
EXECUTE FUNCTION human_calling_guard_call_leg_state();

CREATE TRIGGER human_calling_calls_outcome_guard
BEFORE UPDATE OF terminal_outcome, disposition_outcome ON human_calling_calls
FOR EACH ROW WHEN (
    OLD.terminal_outcome IS DISTINCT FROM NEW.terminal_outcome
    OR OLD.disposition_outcome IS DISTINCT FROM NEW.disposition_outcome
)
EXECUTE FUNCTION human_calling_guard_call_outcome();
