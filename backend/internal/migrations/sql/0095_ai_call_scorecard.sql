-- One yes/no scorecard answer per call per question (ACU-99), and the human
-- review golden set that measures the judge questions against people (ACU-93).
SET LOCAL lock_timeout = '1s';

CREATE TABLE ai_interaction_scorecard_answers (
    interaction_id uuid NOT NULL,
    practice_id uuid NOT NULL,
    question text NOT NULL CHECK (question ~ '^[a-z][a-z_]{0,59}$'),
    source text NOT NULL CHECK (source IN ('judge', 'code')),
    answer boolean NOT NULL,
    probability double precision CHECK (probability BETWEEN 0 AND 1),
    scorecard_version text NOT NULL CHECK (char_length(scorecard_version) BETWEEN 1 AND 100),
    judge_model text CHECK (char_length(judge_model) <= 100),
    agent_version text CHECK (char_length(agent_version) <= 100),
    detail jsonb,
    computed_at timestamptz NOT NULL,
    PRIMARY KEY (interaction_id, question),
    FOREIGN KEY (practice_id, interaction_id) REFERENCES ai_interactions(practice_id, id) ON DELETE CASCADE,
    CHECK ((source = 'judge') = (judge_model IS NOT NULL))
);

CREATE TABLE ai_call_review_assignments (
    practice_id uuid NOT NULL,
    review_date date NOT NULL,
    reviewer text NOT NULL,
    reviewer_email text NOT NULL,
    interaction_id uuid NOT NULL,
    sample text NOT NULL CHECK (sample IN ('random', 'flagged')),
    overlap boolean NOT NULL,
    position smallint NOT NULL,
    excluded boolean NOT NULL DEFAULT false,
    note text NOT NULL DEFAULT '' CHECK (char_length(note) <= 2000),
    completed_at timestamptz,
    PRIMARY KEY (practice_id, review_date, reviewer, interaction_id),
    FOREIGN KEY (practice_id, interaction_id) REFERENCES ai_interactions(practice_id, id) ON DELETE CASCADE
);

CREATE TABLE ai_call_reviews (
    interaction_id uuid NOT NULL,
    practice_id uuid NOT NULL,
    question text NOT NULL CHECK (question ~ '^[a-z][a-z_]{0,59}$'),
    reviewer text NOT NULL,
    reviewer_email text NOT NULL,
    answer boolean NOT NULL,
    note text NOT NULL DEFAULT '' CHECK (char_length(note) <= 2000),
    review_date date NOT NULL,
    sample text NOT NULL CHECK (sample IN ('random', 'flagged', 'manual', 'booking_pick')),
    scorecard_version text NOT NULL,
    judge_answer boolean,
    judge_probability double precision CHECK (judge_probability BETWEEN 0 AND 1),
    judge_version text,
    reviewed_at timestamptz NOT NULL,
    PRIMARY KEY (interaction_id, question, reviewer),
    FOREIGN KEY (practice_id, interaction_id) REFERENCES ai_interactions(practice_id, id) ON DELETE CASCADE
);
