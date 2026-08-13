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
        digest(COALESCE(source_message, ''), 'sha256')
    )
    WHERE state = 'OPEN'
        AND origin NOT IN ('MISSED_CALL_RECOVERY', 'VOICEMAIL_RECOVERY');

-- Seed the latest authoritative resolution for each Practice and phone.
WITH resolutions AS (
    SELECT
        call.practice_id,
        COALESCE(handoff.phone, call.caller_phone) AS phone,
        staff.bridged_at AS resolved_at,
        'INBOUND_CALL'::text AS kind,
        call.id::text AS source_id
    FROM human_calling_calls call
    LEFT JOIN human_calling_handoffs handoff
        ON handoff.id = call.source_handoff_id
    JOIN human_calling_call_legs staff
        ON staff.call_id = call.id
        AND staff.role = 'STAFF'
        AND staff.bridged_at IS NOT NULL
    WHERE call.direction = 'INBOUND'

    UNION ALL

    SELECT
        interaction.practice_id,
        interaction.phone,
        interaction.appointment_occurred_at,
        'BOOKING'::text,
        interaction.id::text
    FROM ai_interactions interaction
    WHERE interaction.appointment_outcome = 'BOOKING'
        AND lower(COALESCE(interaction.booking_result ->> 'status', '')) = 'booked'
        AND interaction.appointment_occurred_at IS NOT NULL
), latest AS (
    SELECT DISTINCT ON (practice_id, phone)
        practice_id,
        phone,
        resolved_at,
        kind,
        source_id
    FROM resolutions
    WHERE phone ~ '^\+[1-9][0-9]{7,14}$'
    ORDER BY practice_id, phone, resolved_at DESC, source_id DESC
)
INSERT INTO work_recovery_resolution_checkpoints (
    practice_id,
    phone,
    resolved_at,
    kind,
    source_id,
    updated_at
)
SELECT practice_id, phone, resolved_at, kind, source_id, now()
FROM latest;

WITH eligible AS (
    SELECT
        task.id,
        checkpoint.kind,
        checkpoint.resolved_at
    FROM work_tasks task
    JOIN work_recovery_resolution_checkpoints checkpoint
        ON checkpoint.practice_id = task.practice_id
        AND checkpoint.phone = task.phone
    WHERE task.state = 'OPEN'
        AND task.origin IN ('MISSED_CALL_RECOVERY', 'VOICEMAIL_RECOVERY')
        AND checkpoint.resolved_at > (
            SELECT max(interaction.occurred_at)
            FROM work_task_interactions interaction
            WHERE interaction.task_id = task.id
        )
        AND checkpoint.resolved_at > COALESCE(
            (
                SELECT max(activity.occurred_at)
                FROM work_task_activities activity
                WHERE activity.task_id = task.id
                    AND activity.kind = 'TASK_REOPENED'
            ),
            '-infinity'::timestamptz
        )
), completed AS (
    UPDATE work_tasks task
    SET
        state = 'COMPLETED',
        completed_by_kind = 'SERVICE',
        completed_by_subject = 'work-recovery-resolution',
        completed_by_email = NULL,
        completed_at = eligible.resolved_at,
        version = task.version + 1,
        updated_at = GREATEST(task.updated_at, eligible.resolved_at)
    FROM eligible
    WHERE task.id = eligible.id
    RETURNING task.id, task.practice_id, task.version, task.completed_at, eligible.kind
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
        CASE completed.kind
            WHEN 'BOOKING' THEN 'TASK_AUTO_COMPLETED_BOOKING'
            ELSE 'TASK_AUTO_COMPLETED_INBOUND_CALL'
        END,
        'SERVICE',
        'work-recovery-resolution',
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
