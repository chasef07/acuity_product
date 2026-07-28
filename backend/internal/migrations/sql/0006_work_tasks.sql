CREATE TABLE work_tasks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    call_id uuid NOT NULL UNIQUE REFERENCES human_calling_calls(id),
    phone text NOT NULL CHECK (phone ~ '^\+[1-9][0-9]{7,14}$'),
    title text NOT NULL CHECK (
        title = btrim(title)
        AND char_length(title) BETWEEN 1 AND 500
    ),
    state text NOT NULL CHECK (state IN ('OPEN', 'COMPLETED')),
    created_by_subject text NOT NULL,
    created_by_email text NOT NULL CHECK (created_by_email = lower(created_by_email)),
    created_at timestamptz NOT NULL,
    completed_by_subject text,
    completed_by_email text,
    completed_at timestamptz,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL,
    phone_digits text GENERATED ALWAYS AS (
        regexp_replace(phone, '[^0-9]', '', 'g')
    ) STORED,
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id),
    CHECK (
        (
            state = 'OPEN'
            AND completed_by_subject IS NULL
            AND completed_by_email IS NULL
            AND completed_at IS NULL
        )
        OR (
            state = 'COMPLETED'
            AND completed_by_subject IS NOT NULL
            AND completed_by_email IS NOT NULL
            AND completed_at IS NOT NULL
        )
    ),
    CHECK (
        completed_by_email IS NULL
        OR completed_by_email = lower(completed_by_email)
    )
);

CREATE TABLE work_task_activities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES work_tasks(id),
    task_version bigint NOT NULL CHECK (task_version > 0),
    kind text NOT NULL CHECK (kind IN (
        'TASK_CREATED',
        'TITLE_CHANGED',
        'TASK_COMPLETED',
        'TASK_REOPENED'
    )),
    actor_subject text NOT NULL,
    actor_email text NOT NULL CHECK (actor_email = lower(actor_email)),
    occurred_at timestamptz NOT NULL,
    UNIQUE (task_id, task_version)
);

CREATE INDEX work_tasks_open_queue_idx
    ON work_tasks (practice_id, location_id, created_at, id)
    WHERE state = 'OPEN';

CREATE INDEX work_tasks_completed_queue_idx
    ON work_tasks (practice_id, location_id, completed_at DESC, id)
    WHERE state = 'COMPLETED';

CREATE INDEX work_task_activities_task_idx
    ON work_task_activities (task_id, task_version);

CREATE INDEX human_calling_handoffs_phone_history_idx
    ON human_calling_handoffs (practice_id, phone, location_id, id);
