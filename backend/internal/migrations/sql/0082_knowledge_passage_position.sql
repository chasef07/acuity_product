SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE knowledge_passages
    ADD COLUMN position integer CHECK (position >= 0);
