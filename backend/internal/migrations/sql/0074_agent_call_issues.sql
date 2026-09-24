CREATE TABLE ai_interaction_issues (
    interaction_id uuid PRIMARY KEY REFERENCES ai_interactions(id),
    reported_by text NOT NULL,
    note text NOT NULL CHECK (char_length(btrim(note)) BETWEEN 1 AND 2000),
    created_at timestamptz NOT NULL DEFAULT now()
);
