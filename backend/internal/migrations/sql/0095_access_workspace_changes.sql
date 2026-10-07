SET LOCAL lock_timeout = '1s';

CREATE TABLE access_workspace_changes (
    practice_id uuid NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    task_ids uuid[],
    PRIMARY KEY (practice_id, version)
);
