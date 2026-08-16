ALTER TABLE work_tasks
    ADD COLUMN completed_by_kind text
        CHECK (completed_by_kind IN ('HUMAN', 'SERVICE'));

UPDATE work_tasks
SET completed_by_kind = 'HUMAN'
WHERE state = 'COMPLETED';

DO $$
DECLARE
    constraint_name text;
BEGIN
    SELECT conname
    INTO constraint_name
    FROM pg_constraint
    WHERE conrelid = 'work_tasks'::regclass
        AND contype = 'c'
        AND pg_get_constraintdef(oid) LIKE '%completed_by_subject IS NULL%'
        AND pg_get_constraintdef(oid) LIKE '%state = %OPEN%';

    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE work_tasks DROP CONSTRAINT %I', constraint_name);
    END IF;
END
$$;

ALTER TABLE work_tasks
    ADD CONSTRAINT work_tasks_completion_actor_check CHECK (
        (
            state = 'OPEN'
            AND completed_by_kind IS NULL
            AND completed_by_subject IS NULL
            AND completed_by_email IS NULL
            AND completed_at IS NULL
        )
        OR (
            state = 'COMPLETED'
            AND completed_by_kind = 'HUMAN'
            AND completed_by_subject IS NOT NULL
            AND completed_by_email IS NOT NULL
            AND completed_at IS NOT NULL
        )
        OR (
            state = 'COMPLETED'
            AND completed_by_kind = 'SERVICE'
            AND completed_by_subject IS NOT NULL
            AND completed_by_email IS NULL
            AND completed_at IS NOT NULL
        )
    );

ALTER TABLE work_task_activities
    DROP CONSTRAINT work_task_activities_kind_check,
    ADD CONSTRAINT work_task_activities_kind_check CHECK (kind IN (
        'TASK_CREATED',
        'TITLE_CHANGED',
        'TASK_COMPLETED',
        'TASK_REOPENED',
        'INTERACTION_ATTACHED',
        'TASK_AUTO_COMPLETED_INBOUND_CALL',
        'TASK_AUTO_COMPLETED_BOOKING',
        'TASK_AUTO_COMPLETED_DUPLICATE'
    ));

CREATE TABLE work_recovery_resolution_checkpoints (
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    phone text NOT NULL CHECK (phone ~ '^\+[1-9][0-9]{7,14}$'),
    resolved_at timestamptz NOT NULL,
    kind text NOT NULL CHECK (kind IN ('INBOUND_CALL', 'BOOKING')),
    source_id text NOT NULL CHECK (
        source_id = btrim(source_id)
        AND char_length(source_id) BETWEEN 1 AND 255
    ),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (practice_id, phone)
);

CREATE INDEX work_tasks_open_recovery_phone_idx
    ON work_tasks (practice_id, phone, id)
    WHERE state = 'OPEN'
        AND origin IN ('MISSED_CALL_RECOVERY', 'VOICEMAIL_RECOVERY');

-- Keep compatible recovery evidence together, but retain separate Tasks when
-- the sourced caller names show that a shared phone belongs to two people.
DROP INDEX work_tasks_one_open_recovery_need_idx;

CREATE UNIQUE INDEX work_tasks_one_open_recovery_need_idx
    ON work_tasks (
        practice_id,
        location_id,
        phone,
        COALESCE(lower(caller_name), '')
    )
    WHERE state = 'OPEN'
        AND origin IN ('MISSED_CALL_RECOVERY', 'VOICEMAIL_RECOVERY');

-- Existing exact duplicates retain their evidence but leave the open Queue.
WITH ranked AS (
    SELECT
        task.id,
        row_number() OVER (
            PARTITION BY
                task.practice_id,
                task.location_id,
                task.phone,
                task.origin,
                task.title,
                task.urgency,
                COALESCE(task.category, ''),
                COALESCE(lower(task.caller_name), ''),
                COALESCE(task.source_call_id, ''),
                COALESCE(task.message_thread_id::text, ''),
                COALESCE(task.source_message, '')
            ORDER BY task.created_at, task.id
        ) AS duplicate_number
    FROM work_tasks task
    WHERE task.state = 'OPEN'
        AND task.origin NOT IN ('MISSED_CALL_RECOVERY', 'VOICEMAIL_RECOVERY')
), completed AS (
    UPDATE work_tasks task
    SET
        state = 'COMPLETED',
        completed_by_kind = 'SERVICE',
        completed_by_subject = 'work-queue-reconciliation',
        completed_by_email = NULL,
        completed_at = now(),
        version = task.version + 1,
        updated_at = now()
    FROM ranked
    WHERE task.id = ranked.id
        AND ranked.duplicate_number > 1
    RETURNING task.id, task.practice_id, task.version, task.completed_at
), activities AS (
    INSERT INTO work_task_activities (
        task_id,
        task_version,
        kind,
        actor_kind,
        actor_subject,
        actor_email,
        occurred_at
    )
    SELECT
        completed.id,
        completed.version,
        'TASK_AUTO_COMPLETED_DUPLICATE',
        'SERVICE',
        'work-queue-reconciliation',
        NULL,
        completed.completed_at
    FROM completed
    RETURNING task_id
)
UPDATE access_practices practice
SET workspace_version = workspace_version + 1
WHERE practice.id IN (
    SELECT DISTINCT task.practice_id
    FROM work_tasks task
    JOIN activities ON activities.task_id = task.id
);

CREATE UNIQUE INDEX work_tasks_one_exact_open_need_idx
    ON work_tasks (
        practice_id,
        location_id,
        phone,
        origin,
        digest(title, 'sha256'),
        urgency,
        COALESCE(category, ''),
        digest(COALESCE(lower(caller_name), ''), 'sha256'),
        COALESCE(source_call_id, ''),
        COALESCE(message_thread_id::text, ''),
        digest(COALESCE(source_message, ''), 'sha256')
    )
    WHERE state = 'OPEN'
        AND origin NOT IN ('MISSED_CALL_RECOVERY', 'VOICEMAIL_RECOVERY');

-- Queue only the Practice and phone keys that can have stale recovery work.
-- The worker reconciles one deterministic key per short transaction so the
-- rollout is restartable and never scans or locks all historical evidence in
-- the schema migration transaction.
CREATE TABLE work_recovery_reconciliation_queue (
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    phone text NOT NULL CHECK (phone ~ '^\+[1-9][0-9]{7,14}$'),
    enqueued_at timestamptz NOT NULL,
    PRIMARY KEY (practice_id, phone)
);

INSERT INTO work_recovery_reconciliation_queue (
    practice_id,
    phone,
    enqueued_at
)
SELECT DISTINCT practice_id, phone, now()
FROM work_tasks
WHERE state = 'OPEN'
    AND origin IN ('MISSED_CALL_RECOVERY', 'VOICEMAIL_RECOVERY');

-- Every appointment change is a concise review item. Failed, escalated, and
-- partial calls without a completed appointment action remain reviewable too.
DELETE FROM ai_interaction_attention attention
USING ai_interactions interaction
WHERE attention.interaction_id = interaction.id
    AND attention.reviewed_at IS NULL
    AND interaction.appointment_action IS NULL
    AND interaction.status NOT IN ('FAILED', 'ESCALATED')
    AND interaction.appointment_outcome <> 'PARTIAL';

INSERT INTO ai_interaction_attention (
    interaction_id,
    user_subject,
    outcome_occurred_at,
    created_at
)
SELECT
    interaction.id,
    operational_scope.user_subject,
    COALESCE(
        interaction.appointment_occurred_at,
        interaction.ended_at,
        interaction.started_at
    ),
    now()
FROM ai_interactions interaction
JOIN access_operational_scopes operational_scope
    ON operational_scope.practice_id = interaction.practice_id
WHERE (
        interaction.appointment_action IN (
            'BOOKED',
            'CANCELLED',
            'RESCHEDULED'
        )
        OR interaction.status IN ('FAILED', 'ESCALATED')
        OR interaction.appointment_outcome = 'PARTIAL'
    )
    AND (
        operational_scope.location_scope = 'ALL'
        OR EXISTS (
            SELECT 1
            FROM access_membership_locations location_grant
            WHERE location_grant.membership_id = operational_scope.membership_id
                AND location_grant.practice_id = operational_scope.practice_id
                AND location_grant.location_id = interaction.location_id
        )
    )
ON CONFLICT (interaction_id, user_subject, outcome_occurred_at)
DO NOTHING;
