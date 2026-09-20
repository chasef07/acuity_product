-- acuity:no-transaction
-- Keep replacement atomic, but release its exclusive lock before validation.
ALTER TABLE human_calling_provider_receipts
    DROP CONSTRAINT human_calling_provider_receipts_state_check,
    ADD CONSTRAINT human_calling_provider_receipts_state_check CHECK (
        state IN ('PENDING', 'PROCESSING', 'APPLIED', 'IGNORED', 'UNKNOWN', 'FAILED', 'QUARANTINED')
    ) NOT VALID;

-- acuity:next-statement
ALTER TABLE human_calling_provider_receipts
    VALIDATE CONSTRAINT human_calling_provider_receipts_state_check;

-- acuity:next-statement
-- An interrupted concurrent build can leave an invalid index behind.
DROP INDEX CONCURRENTLY IF EXISTS human_calling_staff_provider_session_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_staff_provider_session_idx
    ON human_calling_call_legs (provider_call_session_id)
    WHERE role = 'STAFF';
