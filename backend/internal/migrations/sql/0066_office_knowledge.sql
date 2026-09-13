-- Existing PostgreSQL instance, exact scoped comparison; no ANN index.
CREATE EXTENSION IF NOT EXISTS vector;

-- Keep retired-route import evidence. Access owns route reconciliation and
-- denies reads when a route is absent; both import and search validate it.
CREATE TABLE knowledge_corpora (
 practice_id uuid NOT NULL REFERENCES access_practices(id),
 office_key text NOT NULL,
 revision_id uuid,
 PRIMARY KEY (practice_id, office_key)
);
CREATE TABLE knowledge_revisions (
 id uuid PRIMARY KEY,
 practice_id uuid NOT NULL,
 office_key text NOT NULL,
 content_hash text NOT NULL,
 embedding_model text NOT NULL CHECK (embedding_model = 'text-multilingual-embedding-002'),
 embedding_dimensions integer NOT NULL CHECK (embedding_dimensions = 768),
 provenance text NOT NULL,
 reason text NOT NULL,
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 previous_revision_id uuid,
 UNIQUE (practice_id, office_key, id),
 FOREIGN KEY (practice_id, office_key) REFERENCES knowledge_corpora(practice_id,office_key)
);
ALTER TABLE knowledge_corpora ADD FOREIGN KEY (practice_id, office_key, revision_id) REFERENCES knowledge_revisions(practice_id,office_key,id);
CREATE TABLE knowledge_passages (
 revision_id uuid NOT NULL REFERENCES knowledge_revisions(id),
 section_id text NOT NULL,
 title text NOT NULL,
 text text NOT NULL,
 embedding vector(768) NOT NULL,
 PRIMARY KEY(revision_id,section_id)
);
-- Only identifiers/outcomes/timings: never queries, caller data, or query vectors.
CREATE TABLE knowledge_retrieval_observations (
 id uuid PRIMARY KEY,
 practice_id uuid NOT NULL,
 office_key text NOT NULL,
 revision_id uuid REFERENCES knowledge_revisions(id),
 section_ids text[] NOT NULL,
 outcome text NOT NULL CHECK(outcome IN ('found','no_relevant_information','temporary_failure')),
 elapsed_millis bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(practice_id,office_key) REFERENCES knowledge_corpora(practice_id,office_key)
);
CREATE INDEX knowledge_observations_scope ON knowledge_retrieval_observations(practice_id,office_key,created_at DESC);
-- Runtime roles receive no INSERT/UPDATE permission on imported corpus content.
-- Imports use the existing operator/migration credential and record attribution.
