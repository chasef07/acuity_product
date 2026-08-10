CREATE TABLE ai_interaction_attention (
    interaction_id uuid NOT NULL REFERENCES ai_interactions(id) ON DELETE CASCADE,
    user_subject text NOT NULL,
    outcome_occurred_at timestamptz NOT NULL,
    reviewed_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (interaction_id, user_subject, outcome_occurred_at)
);

CREATE INDEX ai_interaction_attention_unreviewed_idx
    ON ai_interaction_attention (user_subject, outcome_occurred_at DESC, interaction_id)
    WHERE reviewed_at IS NULL;
