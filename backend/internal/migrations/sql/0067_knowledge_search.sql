-- Search representation is derived from immutable source content. Existing
-- revisions gain lexical search without changing their content or embeddings.
ALTER TABLE knowledge_passages ADD COLUMN search_document tsvector
GENERATED ALWAYS AS (
    setweight(to_tsvector('english', title), 'A') ||
    setweight(to_tsvector('english', text), 'B')
) STORED;
-- Scoped office corpora are small: retrieval computes exact ranks and corpus
-- term frequencies. No approximate vector or unused full-text index is needed.
