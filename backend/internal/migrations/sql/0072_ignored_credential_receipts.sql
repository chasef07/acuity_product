ALTER TABLE human_calling_provider_receipts
    DROP CONSTRAINT human_calling_provider_receipts_state_check,
    ADD CONSTRAINT human_calling_provider_receipts_state_check CHECK (
        state IN ('PENDING', 'PROCESSING', 'APPLIED', 'IGNORED', 'UNKNOWN', 'FAILED', 'QUARANTINED')
    );

CREATE INDEX human_calling_staff_provider_session_idx
    ON human_calling_call_legs (provider_call_session_id)
    WHERE role = 'STAFF';
