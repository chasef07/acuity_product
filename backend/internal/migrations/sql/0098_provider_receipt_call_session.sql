-- Ingress records the Telnyx call_session_id of each signed receipt so worker
-- claims can apply different Calls concurrently and each Call in arrival order.
-- Receipts stored before this column keep NULL and claim independently.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE human_calling_provider_receipts ADD COLUMN call_session_id text;
