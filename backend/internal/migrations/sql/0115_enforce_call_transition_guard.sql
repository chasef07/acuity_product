SET LOCAL lock_timeout = '1s';

CREATE OR REPLACE FUNCTION human_calling_guard_call_leg_state() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF human_calling_leg_state_rank(NEW.state) < human_calling_leg_state_rank(OLD.state) THEN
        RAISE EXCEPTION 'HC001 CallLeg % cannot move from % to %', OLD.id, OLD.state, NEW.state
            USING ERRCODE = 'HC001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION human_calling_guard_call_outcome() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT human_calling_call_outcome_advances(OLD.terminal_outcome, NEW.terminal_outcome) THEN
        RAISE EXCEPTION 'HC002 Call % cannot move terminal_outcome from % to %', OLD.id,
            COALESCE(OLD.terminal_outcome, 'NULL'), COALESCE(NEW.terminal_outcome, 'NULL')
            USING ERRCODE = 'HC002';
    END IF;
    IF OLD.disposition_outcome IS NOT NULL
        AND NEW.disposition_outcome IS DISTINCT FROM OLD.disposition_outcome THEN
        RAISE EXCEPTION 'HC002 Call % cannot move disposition_outcome from % to %', OLD.id,
            OLD.disposition_outcome, COALESCE(NEW.disposition_outcome, 'NULL')
            USING ERRCODE = 'HC002';
    END IF;
    RETURN NEW;
END;
$$;
