-- An explicit Task reference carries the same patient need through a transfer.
-- Existing handoffs remain unlinked; source calls and phones do not prove the same need.
ALTER TABLE human_calling_handoffs
    ADD COLUMN task_id uuid REFERENCES work_tasks(id);
