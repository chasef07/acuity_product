CREATE TABLE work_text_replies (
    message_id uuid PRIMARY KEY REFERENCES messaging_messages(id),
    task_id uuid NOT NULL REFERENCES work_tasks(id),
    task_version bigint NOT NULL CHECK (task_version > 0),
    actor_subject text NOT NULL,
    actor_email text NOT NULL,
    completed_version bigint CHECK (completed_version = task_version + 1)
);
CREATE INDEX work_text_replies_task_idx ON work_text_replies(task_id, task_version);
