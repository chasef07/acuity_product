-- Staff may explain a flagged call in their own words. The note becomes
-- optional; the previous release still copies the reason into note, which
-- Product reads as no explanation.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE ai_interaction_issues ALTER COLUMN note DROP NOT NULL;
