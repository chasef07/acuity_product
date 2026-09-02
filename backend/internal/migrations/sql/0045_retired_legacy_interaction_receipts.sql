-- Retired SUMMARY evidence is neither executable work nor an unresolved
-- quarantine. Only an audited operator action can retire it after a supported
-- CLOSEOUT has established the source Interaction's terminal outcome.
ALTER TABLE ai_interaction_receipts
    DROP CONSTRAINT ai_interaction_receipts_state_check,
    DROP CONSTRAINT ai_interaction_receipts_check,
    ADD CONSTRAINT ai_interaction_receipts_state_check CHECK (
        state IN ('PENDING', 'PROJECTED', 'QUARANTINED', 'RETIRED')
    ),
    ADD CONSTRAINT ai_interaction_receipts_check CHECK (
        (state = 'PENDING' AND interaction_id IS NULL AND
            projection_error_code IS NULL AND projected_at IS NULL)
        OR (state = 'PROJECTED' AND interaction_id IS NOT NULL AND
            projection_error_code IS NULL AND projected_at IS NOT NULL)
        OR (state = 'QUARANTINED' AND interaction_id IS NULL AND
            projection_error_code IS NOT NULL AND projected_at IS NULL)
        OR (state = 'RETIRED' AND kind = 'SUMMARY' AND interaction_id IS NOT NULL AND
            projection_error_code IS NOT NULL AND projected_at IS NULL)
    );
