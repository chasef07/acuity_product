-- A terminated connected call has a durable, worker-owned disposition window.
-- The surfaced recovery task is a snapshot of the exact-phone open need that
-- was present when the inbound call connected; it is never a merge key.
ALTER TABLE human_calling_calls
    ADD COLUMN disposition_deadline timestamptz,
    ADD COLUMN surfaced_task_id uuid REFERENCES work_tasks(id);

ALTER TABLE human_calling_calls
    ADD CONSTRAINT human_calling_calls_disposition_deadline_check CHECK (
        (state = 'NEEDS_DISPOSITION' AND disposition_deadline IS NOT NULL)
        OR (state <> 'NEEDS_DISPOSITION' AND disposition_deadline IS NULL)
    ) NOT VALID;

CREATE INDEX human_calling_pending_dispositions_idx
    ON human_calling_calls (disposition_deadline, id)
    WHERE state = 'NEEDS_DISPOSITION';
