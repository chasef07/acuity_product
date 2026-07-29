CREATE TABLE access_abita_office_locations (
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    office_key text NOT NULL CHECK (
        office_key = btrim(office_key)
        AND char_length(office_key) BETWEEN 1 AND 100
    ),
    location_id uuid NOT NULL,
    PRIMARY KEY (practice_id, office_key),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id)
);

ALTER TABLE work_tasks
    ALTER COLUMN call_id DROP NOT NULL,
    ALTER COLUMN created_by_email DROP NOT NULL,
    ADD COLUMN origin text NOT NULL DEFAULT 'HUMAN_CALL_FOLLOW_UP'
        CHECK (origin IN ('HUMAN_CALL_FOLLOW_UP', 'ABITA_AI')),
    ADD COLUMN urgency text NOT NULL DEFAULT 'normal'
        CHECK (urgency IN ('high_priority', 'normal', 'non_urgent')),
    ADD COLUMN created_by_kind text NOT NULL DEFAULT 'HUMAN'
        CHECK (created_by_kind IN ('HUMAN', 'SERVICE')),
    ADD COLUMN caller_name text CHECK (
        caller_name IS NULL
        OR (
            caller_name = btrim(caller_name)
            AND char_length(caller_name) BETWEEN 1 AND 200
        )
    ),
    ADD COLUMN source_call_id text CHECK (
        source_call_id IS NULL
        OR (
            source_call_id = btrim(source_call_id)
            AND char_length(source_call_id) BETWEEN 1 AND 255
        )
    ),
    ADD COLUMN source_message text CHECK (
        source_message IS NULL
        OR (
            source_message = btrim(source_message)
            AND char_length(source_message) BETWEEN 1 AND 2500
        )
    ),
    ADD COLUMN category text CHECK (
        category IS NULL
        OR category IN (
            'billing',
            'appointments',
            'documentation',
            'optical',
            'medication',
            'referrals',
            'other'
        )
    ),
    ADD COLUMN ai_idempotency_key text CHECK (
        ai_idempotency_key IS NULL
        OR (
            ai_idempotency_key = btrim(ai_idempotency_key)
            AND char_length(ai_idempotency_key) BETWEEN 1 AND 200
        )
    ),
    ADD COLUMN ai_input_fingerprint bytea;

ALTER TABLE work_tasks
    ADD CONSTRAINT work_tasks_origin_source_check CHECK (
        (
            origin = 'HUMAN_CALL_FOLLOW_UP'
            AND call_id IS NOT NULL
            AND urgency = 'normal'
            AND created_by_kind = 'HUMAN'
            AND created_by_email IS NOT NULL
            AND caller_name IS NULL
            AND source_call_id IS NULL
            AND source_message IS NULL
            AND category IS NULL
            AND ai_idempotency_key IS NULL
            AND ai_input_fingerprint IS NULL
        )
        OR (
            origin = 'ABITA_AI'
            AND call_id IS NULL
            AND created_by_kind = 'SERVICE'
            AND created_by_email IS NULL
            AND source_call_id IS NOT NULL
            AND source_message IS NOT NULL
            AND category IS NOT NULL
            AND ai_idempotency_key IS NOT NULL
            AND ai_input_fingerprint IS NOT NULL
            AND octet_length(ai_input_fingerprint) = 32
        )
    );

CREATE UNIQUE INDEX work_tasks_ai_idempotency_idx
    ON work_tasks (created_by_subject, ai_idempotency_key)
    WHERE origin = 'ABITA_AI';

CREATE INDEX work_tasks_open_priority_queue_idx
    ON work_tasks (
        practice_id,
        location_id,
        (
            CASE urgency
                WHEN 'high_priority' THEN 0
                WHEN 'normal' THEN 1
                ELSE 2
            END
        ),
        created_at,
        id
    )
    WHERE state = 'OPEN';

ALTER TABLE work_task_activities
    ALTER COLUMN actor_email DROP NOT NULL,
    ADD COLUMN actor_kind text NOT NULL DEFAULT 'HUMAN'
        CHECK (actor_kind IN ('HUMAN', 'SERVICE'));

ALTER TABLE work_task_activities
    ADD CONSTRAINT work_task_activities_actor_check CHECK (
        (actor_kind = 'HUMAN' AND actor_email IS NOT NULL)
        OR (actor_kind = 'SERVICE' AND actor_email IS NULL)
    );

CREATE FUNCTION work_preserve_task_source()
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
        OLD.ai_input_fingerprint
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
        NEW.ai_input_fingerprint
    ) THEN
        RAISE EXCEPTION 'Task source is immutable';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER work_preserve_task_source
BEFORE UPDATE ON work_tasks
FOR EACH ROW
EXECUTE FUNCTION work_preserve_task_source();
