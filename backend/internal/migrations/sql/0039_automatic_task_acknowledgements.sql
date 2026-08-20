CREATE TABLE work_task_acknowledgements (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES work_tasks(id),
    purpose text NOT NULL CHECK (purpose = 'CALLER_TASK_RECEIVED'),
    state text NOT NULL DEFAULT 'PENDING' CHECK (
        state IN ('PENDING', 'MESSAGE_QUEUED', 'NOT_NEEDED')
    ),
    safe_failure_code text,
    message_id uuid UNIQUE REFERENCES messaging_messages(id),
    next_attempt_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_id, purpose),
    CHECK (
        (state = 'PENDING' AND message_id IS NULL
            AND next_attempt_at IS NOT NULL AND completed_at IS NULL)
        OR (state = 'MESSAGE_QUEUED' AND safe_failure_code IS NULL
            AND message_id IS NOT NULL AND next_attempt_at IS NULL
            AND completed_at IS NOT NULL)
        OR (state = 'NOT_NEEDED' AND safe_failure_code IS NOT NULL
            AND message_id IS NULL AND next_attempt_at IS NULL
            AND completed_at IS NOT NULL)
    )
);

CREATE INDEX work_pending_task_acknowledgements_idx
    ON work_task_acknowledgements (next_attempt_at, created_at, id)
    WHERE state = 'PENDING';

CREATE FUNCTION work_create_task_acknowledgement()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO work_task_acknowledgements (
        task_id,
        purpose,
        next_attempt_at,
        created_at,
        updated_at
    ) VALUES (
        NEW.id,
        'CALLER_TASK_RECEIVED',
        NEW.created_at,
        NEW.created_at,
        NEW.created_at
    );
    RETURN NEW;
END
$$;

CREATE TRIGGER work_create_task_acknowledgement
AFTER INSERT ON work_tasks
FOR EACH ROW EXECUTE FUNCTION work_create_task_acknowledgement();

ALTER TABLE messaging_messages
    ADD COLUMN created_by_kind text CHECK (
        created_by_kind IS NULL OR created_by_kind IN ('HUMAN', 'SERVICE')
    );

-- The migration is applied before traffic leaves the prior portal revision.
-- That revision supplies the human subject but not created_by_kind, while the
-- new revision always supplies the kind explicitly. Keep both revisions and a
-- traffic rollback compatible without weakening the stored-row invariant.
CREATE FUNCTION messaging_default_legacy_outbound_creator_kind()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.direction = 'OUTBOUND'
        AND NEW.created_by_kind IS NULL
        AND NEW.created_by_subject IS NOT NULL THEN
        NEW.created_by_kind = 'HUMAN';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER messaging_default_legacy_outbound_creator_kind
BEFORE INSERT ON messaging_messages
FOR EACH ROW EXECUTE FUNCTION messaging_default_legacy_outbound_creator_kind();

UPDATE messaging_messages
SET created_by_kind = 'HUMAN'
WHERE direction = 'OUTBOUND' AND created_by_subject IS NOT NULL;

ALTER TABLE messaging_messages
    DROP CONSTRAINT messaging_messages_check,
    ADD CONSTRAINT messaging_messages_creator_check CHECK (
        (
            direction = 'OUTBOUND'
            AND created_by_kind IS NOT NULL
            AND created_by_kind IN ('HUMAN', 'SERVICE')
            AND created_by_subject IS NOT NULL
        )
        OR (
            direction = 'INBOUND'
            AND created_by_kind IS NULL
            AND created_by_subject IS NULL
        )
    );

CREATE OR REPLACE FUNCTION messaging_preserve_message_content()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        OLD.thread_id,
        OLD.practice_id,
        OLD.location_id,
        OLD.direction,
        OLD.body,
        OLD.sender,
        OLD.destination,
        OLD.retry_of_message_id,
        OLD.created_by_kind,
        OLD.created_by_subject,
        OLD.created_at
    ) IS DISTINCT FROM ROW(
        NEW.thread_id,
        NEW.practice_id,
        NEW.location_id,
        NEW.direction,
        NEW.body,
        NEW.sender,
        NEW.destination,
        NEW.retry_of_message_id,
        NEW.created_by_kind,
        NEW.created_by_subject,
        NEW.created_at
    ) THEN
        RAISE EXCEPTION 'Message content is immutable';
    END IF;
    RETURN NEW;
END
$$;
