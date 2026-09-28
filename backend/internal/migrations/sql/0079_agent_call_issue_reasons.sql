-- Staff classify a flagged call with a fixed reason, and Platform Operators
-- record whether it is a real issue. Earlier free-text reports become OTHER.
-- The reason default and the required note keep the previous release working
-- during promotion and rollback; Product writes the reason into note.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE ai_interaction_issues
    ADD COLUMN reason text NOT NULL DEFAULT 'OTHER'
        CHECK (reason IN ('WRONG_APPOINTMENT_TYPE', 'INSURANCE_ISSUE', 'OTHER')),
    ADD COLUMN review_outcome text CHECK (review_outcome IN ('CONFIRMED', 'NOT_AN_ISSUE')),
    ADD COLUMN reviewed_by text,
    ADD COLUMN reviewed_at timestamptz,
    ADD CONSTRAINT ai_interaction_issues_review_complete CHECK (
        (review_outcome IS NULL) = (reviewed_by IS NULL)
        AND (review_outcome IS NULL) = (reviewed_at IS NULL)
    );
