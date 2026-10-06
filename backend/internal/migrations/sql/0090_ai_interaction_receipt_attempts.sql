-- A receipt that keeps failing projection must not block newer receipts or
-- retry forever. Attempts and the last failure code stay on the receipt; after
-- the retry budget it is quarantined as PROJECTION_RETRY_EXHAUSTED.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE ai_interaction_receipts
    ADD COLUMN projection_attempts integer NOT NULL DEFAULT 0
        CHECK (projection_attempts >= 0),
    ADD COLUMN next_attempt_at timestamptz,
    ADD COLUMN last_attempt_at timestamptz,
    ADD COLUMN last_error_code text
        CHECK (last_error_code IS NULL OR char_length(last_error_code) BETWEEN 1 AND 100);
