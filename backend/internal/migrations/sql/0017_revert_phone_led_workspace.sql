-- Restore the schema owned by the revision before PR #44. The migration fails
-- closed when new evidence cannot be represented by that earlier schema.
DO $rollback$
BEGIN
    IF EXISTS (SELECT 1 FROM human_calling_staff_transfers) THEN
        RAISE EXCEPTION 'cannot roll back PR #44 while staff transfer evidence exists'
            USING HINT = 'Review and preserve human_calling_staff_transfers before retrying.';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM human_calling_connection_attempts
        WHERE staff_transfer_id IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'cannot roll back PR #44 while transfer attempts exist'
            USING HINT = 'Review and preserve staff_transfer_id evidence before retrying.';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM work_task_interactions interaction
        JOIN work_tasks task ON task.id = interaction.task_id
        WHERE task.call_id IS DISTINCT FROM interaction.call_id
    ) THEN
        RAISE EXCEPTION 'cannot roll back PR #44 while Tasks have additional interactions'
            USING HINT = 'Preserve additional Task interaction evidence before retrying.';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM work_task_activities
        WHERE kind = 'INTERACTION_ATTACHED'
    ) THEN
        RAISE EXCEPTION 'cannot roll back PR #44 while interaction activities exist'
            USING HINT = 'Preserve INTERACTION_ATTACHED activity evidence before retrying.';
    END IF;

    IF EXISTS (
        SELECT task_id
        FROM human_calling_voicemails
        GROUP BY task_id
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot roll back PR #44 while a Task has multiple voicemails'
            USING HINT = 'Reconcile duplicate voicemail Task ownership before retrying.';
    END IF;
END
$rollback$;

DROP TABLE work_task_interactions;

DROP INDEX work_tasks_one_open_recovery_need_idx;

ALTER TABLE human_calling_connection_attempts
    DROP COLUMN staff_transfer_id;

DROP TABLE human_calling_staff_transfers;

ALTER TABLE human_calling_voicemails
    ADD CONSTRAINT human_calling_voicemails_task_id_key UNIQUE (task_id);

ALTER TABLE work_task_activities
    DROP CONSTRAINT work_task_activities_kind_check,
    ADD CONSTRAINT work_task_activities_kind_check CHECK (kind IN (
        'TASK_CREATED',
        'TITLE_CHANGED',
        'TASK_COMPLETED',
        'TASK_REOPENED'
    ));

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
        OLD.origin,
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
        OLD.message_thread_id,
        OLD.recovery_outcome
    ) IS DISTINCT FROM ROW(
        NEW.practice_id,
        NEW.location_id,
        NEW.call_id,
        NEW.phone,
        NEW.origin,
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
        NEW.message_thread_id,
        NEW.recovery_outcome
    ) THEN
        RAISE EXCEPTION 'Task source is immutable';
    END IF;
    RETURN NEW;
END
$$;
