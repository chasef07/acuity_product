CREATE TABLE ai_manual_tags (
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    name text NOT NULL CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 60),
    key text NOT NULL CHECK (key = lower(name)),
    PRIMARY KEY (practice_id, key)
);

ALTER TABLE ai_interactions ADD CONSTRAINT ai_interactions_practice_id_id_key UNIQUE (practice_id, id);

CREATE TABLE ai_interaction_manual_tags (
    interaction_id uuid NOT NULL,
    practice_id uuid NOT NULL,
    tag_key text NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (interaction_id, tag_key),
    FOREIGN KEY (practice_id, interaction_id) REFERENCES ai_interactions(practice_id, id) ON DELETE CASCADE,
    FOREIGN KEY (practice_id, tag_key) REFERENCES ai_manual_tags(practice_id, key)
);
CREATE INDEX ai_interaction_manual_tags_filter_idx
    ON ai_interaction_manual_tags (practice_id, tag_key, interaction_id);
