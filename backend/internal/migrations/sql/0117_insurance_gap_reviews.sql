-- Staff close a call the judge flagged for an insurance missing from the
-- office's insurance list, either after adding it or because none is needed.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE ai_interaction_insurance_gap_reviews (
    interaction_id uuid PRIMARY KEY REFERENCES ai_interactions(id),
    resolution text NOT NULL CHECK (resolution IN ('ADDED', 'NOT_NEEDED')),
    reviewed_by text NOT NULL,
    reviewed_at timestamptz NOT NULL
);
