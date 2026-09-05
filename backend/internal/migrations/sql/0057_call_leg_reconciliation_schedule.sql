-- Observation scheduling is operational state, not evidence of a CallLeg
-- transition. In particular, a failed read must not advance updated_at, which
-- anchors the provider event-history window.
SET LOCAL lock_timeout = '5s';

ALTER TABLE human_calling_call_legs
    ADD COLUMN reconciliation_checked_at timestamptz,
    ADD COLUMN reconciliation_next_attempt_at timestamptz,
    ADD COLUMN reconciliation_attempts integer NOT NULL DEFAULT 0
        CHECK (reconciliation_attempts BETWEEN 0 AND 5),
    ADD COLUMN reconciliation_error_code text;
