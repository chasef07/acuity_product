-- Staff close a call the judge flagged for missing office knowledge, either
-- after answering it in the office's knowledge or because no answer is needed.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE ai_interaction_knowledge_gap_reviews (
    interaction_id uuid PRIMARY KEY REFERENCES ai_interactions(id),
    resolution text NOT NULL CHECK (resolution IN ('ANSWERED', 'NOT_NEEDED')),
    reviewed_by text NOT NULL,
    reviewed_at timestamptz NOT NULL
);
