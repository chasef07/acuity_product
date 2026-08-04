-- One Task may be supported by multiple communication Interactions. Keep the
-- original work_tasks.call_id as immutable source compatibility while this
-- relation becomes the authoritative attachment set.
CREATE TABLE work_task_interactions (
    task_id uuid NOT NULL REFERENCES work_tasks(id),
    call_id uuid NOT NULL UNIQUE REFERENCES human_calling_calls(id),
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (task_id, call_id),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id),
    FOREIGN KEY (task_id, practice_id, location_id)
        REFERENCES work_tasks(id, practice_id, location_id)
);

CREATE INDEX work_task_interactions_task_time_idx
    ON work_task_interactions (task_id, occurred_at, call_id);

INSERT INTO work_task_interactions (
    task_id,
    call_id,
    practice_id,
    location_id,
    occurred_at
)
SELECT
    task.id,
    task.call_id,
    task.practice_id,
    task.location_id,
    call.created_at
FROM work_tasks task
JOIN human_calling_calls call ON call.id = task.call_id
WHERE task.call_id IS NOT NULL;

ALTER TABLE human_calling_voicemails
    DROP CONSTRAINT human_calling_voicemails_task_id_key;

ALTER TABLE work_task_activities
    DROP CONSTRAINT work_task_activities_kind_check,
    ADD CONSTRAINT work_task_activities_kind_check CHECK (kind IN (
        'TASK_CREATED',
        'TITLE_CHANGED',
        'TASK_COMPLETED',
        'TASK_REOPENED',
        'INTERACTION_ATTACHED'
    ));

-- A missed-call recovery source may be enriched by later voicemail evidence.
-- Every other Task source remains immutable.
CREATE OR REPLACE FUNCTION work_preserve_task_source()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        OLD.practice_id,
        OLD.location_id,
        OLD.call_id,
        OLD.phone,
        OLD.urgency,
        OLD.created_by_kind,
        OLD.created_by_subject,
        OLD.created_by_email,
        OLD.created_at,
        OLD.caller_name,
        OLD.source_call_id,
        OLD.source_message,
        OLD.category,
        OLD.ai_idempotency_key,
        OLD.ai_input_fingerprint,
        OLD.source_message_id,
        OLD.message_thread_id
    ) IS DISTINCT FROM ROW(
        NEW.practice_id,
        NEW.location_id,
        NEW.call_id,
        NEW.phone,
        NEW.urgency,
        NEW.created_by_kind,
        NEW.created_by_subject,
        NEW.created_by_email,
        NEW.created_at,
        NEW.caller_name,
        NEW.source_call_id,
        NEW.source_message,
        NEW.category,
        NEW.ai_idempotency_key,
        NEW.ai_input_fingerprint,
        NEW.source_message_id,
        NEW.message_thread_id
    ) THEN
        RAISE EXCEPTION 'Task source is immutable';
    END IF;
    IF ROW(OLD.origin, OLD.recovery_outcome) IS DISTINCT FROM
        ROW(NEW.origin, NEW.recovery_outcome)
        AND NOT (
            OLD.origin = 'MISSED_CALL_RECOVERY'
            AND OLD.recovery_outcome = 'MISSED_CALL'
            AND NEW.origin = 'VOICEMAIL_RECOVERY'
            AND NEW.recovery_outcome = 'VOICEMAIL'
        ) THEN
        RAISE EXCEPTION 'Task source is immutable';
    END IF;
    RETURN NEW;
END
$$;

-- The retired migration consolidated duplicate recovery Tasks destructively.
-- A forward restoration must never decide which accountable Task to delete.
-- Refuse to migrate if new duplicates appeared after rollback so they can be
-- reconciled deliberately with an audit trail before retrying deployment.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM work_tasks
        WHERE state = 'OPEN'
            AND origin IN ('VOICEMAIL_RECOVERY', 'MISSED_CALL_RECOVERY')
        GROUP BY practice_id, location_id, phone
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION
            'cannot restore phone workspace: duplicate open recovery Tasks require audited reconciliation';
    END IF;
END
$$;

CREATE UNIQUE INDEX work_tasks_one_open_recovery_need_idx
    ON work_tasks (practice_id, location_id, phone)
    WHERE state = 'OPEN'
        AND origin IN ('VOICEMAIL_RECOVERY', 'MISSED_CALL_RECOVERY');
