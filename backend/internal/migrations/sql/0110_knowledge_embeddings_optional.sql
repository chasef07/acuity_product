SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

-- Publishing no longer embeds knowledge. Columns stay so rolled-back app
-- revisions can still write them; drop them only after no deployable revision does.
ALTER TABLE knowledge_passages ALTER COLUMN embedding DROP NOT NULL;
ALTER TABLE knowledge_revisions ALTER COLUMN embedding_model DROP NOT NULL;
ALTER TABLE knowledge_revisions ALTER COLUMN embedding_dimensions DROP NOT NULL;
