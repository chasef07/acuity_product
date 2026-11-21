-- A text review that answers a staff member's text belongs to that staff
-- member, not to everyone: routed_to_subject names them. NULL keeps the review
-- shared, as every review was before, and so does a subject with no live
-- membership.
--
-- A practice can block a phone's inbound texts. Blocked texts are still stored
-- as evidence but open no review. Telnyx offers no per-sender inbound block, so
-- the block lives here.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE work_tasks
    ADD COLUMN routed_to_subject text;

CREATE TABLE messaging_blocked_senders (
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    phone text NOT NULL,
    blocked_by_email text NOT NULL,
    blocked_at timestamptz NOT NULL,
    PRIMARY KEY (practice_id, phone)
);
