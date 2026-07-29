ALTER TABLE human_calling_provider_receipts
    ADD COLUMN projection_attempts integer NOT NULL DEFAULT 0
        CHECK (projection_attempts >= 0),
    ADD COLUMN last_attempt_at timestamptz,
    ADD COLUMN quarantined_at timestamptz;

ALTER TABLE human_calling_provider_receipts
    DROP CONSTRAINT human_calling_provider_receipts_state_check,
    ADD CONSTRAINT human_calling_provider_receipts_state_check
        CHECK (state IN (
            'PENDING',
            'PROCESSING',
            'APPLIED',
            'UNKNOWN',
            'FAILED',
            'QUARANTINED'
        )),
    ADD CONSTRAINT human_calling_provider_receipts_attempt_visibility_check
        CHECK (
            (projection_attempts = 0 AND last_attempt_at IS NULL)
            OR (projection_attempts > 0 AND last_attempt_at IS NOT NULL)
        ),
    ADD CONSTRAINT human_calling_provider_receipts_quarantine_check
        CHECK (
            (state = 'QUARANTINED' AND quarantined_at IS NOT NULL)
            OR (state <> 'QUARANTINED' AND quarantined_at IS NULL)
        );
