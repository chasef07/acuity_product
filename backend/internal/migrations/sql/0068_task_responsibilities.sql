-- Editable classification is separate from immutable ingestion evidence.
ALTER TABLE work_tasks ADD COLUMN source_category text;
UPDATE work_tasks SET source_category = category;
ALTER TABLE work_tasks DROP CONSTRAINT work_tasks_category_check;
ALTER TABLE work_tasks ADD CONSTRAINT work_tasks_category_check CHECK (
 category IS NULL OR category IN ('billing','appointments','documentation','medication','optical','referrals','other','insurance','pre_op','post_op'));
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
        OLD.source_category,
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
        NEW.source_category,
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

CREATE FUNCTION work_capture_source_category() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.source_category := NEW.category;
 IF NEW.category = 'billing' AND NEW.state = 'OPEN' THEN NEW.category := 'other'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER work_capture_source_category BEFORE INSERT ON work_tasks
FOR EACH ROW EXECUTE FUNCTION work_capture_source_category();

DROP INDEX work_tasks_one_exact_open_need_idx;
CREATE UNIQUE INDEX work_tasks_one_exact_open_need_idx
    ON work_tasks (
        practice_id,
        location_id,
        phone,
        origin,
        digest(title, 'sha256'),
        urgency,
        COALESCE(source_category, ''),
        digest(COALESCE(lower(caller_name), ''), 'sha256'),
        COALESCE(source_call_id, ''),
        COALESCE(message_thread_id::text, ''),
        digest(COALESCE(source_message, ''), 'sha256')
    )
    WHERE state = 'OPEN'
        AND origin NOT IN ('MISSED_CALL_RECOVERY', 'VOICEMAIL_RECOVERY');

ALTER TABLE work_task_activities DROP CONSTRAINT work_task_activities_kind_check;
ALTER TABLE work_task_activities ADD CONSTRAINT work_task_activities_kind_check CHECK (kind IN (
 'TASK_CREATED','TITLE_CHANGED','TASK_COMPLETED','TASK_REOPENED','INTERACTION_ATTACHED',
 'TASK_AUTO_COMPLETED_INBOUND_CALL','TASK_AUTO_COMPLETED_BOOKING','TASK_AUTO_COMPLETED_DUPLICATE',
 'CATEGORY_CHANGED','KNOWLEDGE_FEEDBACK_CHANGED'));
ALTER TABLE work_task_activities ADD COLUMN details jsonb NOT NULL DEFAULT '{}';
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='work_tasks'::regclass AND conname='work_tasks_origin_source_check';
 ALTER TABLE work_tasks DROP CONSTRAINT work_tasks_origin_source_check;
 EXECUTE 'ALTER TABLE work_tasks ADD CONSTRAINT work_tasks_origin_source_check ' || replace(definition, '(category IS NULL)', '(true)');
END $$;

ALTER TABLE work_tasks
 ADD COLUMN knowledge_flagged boolean NOT NULL DEFAULT false,
 ADD COLUMN suggested_answer text NOT NULL DEFAULT '' CHECK (char_length(suggested_answer)<=2500),
 ADD COLUMN knowledge_updated_by text,
 ADD COLUMN knowledge_updated_at timestamptz;

CREATE TABLE work_responsibility_locations (
 practice_id uuid NOT NULL,
 location_id uuid NOT NULL,
 PRIMARY KEY (practice_id,location_id),
 FOREIGN KEY (practice_id,location_id) REFERENCES access_locations(practice_id,id)
);
CREATE TABLE work_responsibilities (
 practice_id uuid NOT NULL,
 location_id uuid NOT NULL,
 account_email text NOT NULL CHECK (account_email=lower(btrim(account_email))),
 category text NOT NULL CHECK (category IN ('appointments','documentation','medication','optical','referrals','other','insurance','pre_op','post_op')),
 role text NOT NULL CHECK (role IN ('primary','backup')),
 PRIMARY KEY (practice_id,location_id,account_email,category,role),
 FOREIGN KEY (practice_id,location_id) REFERENCES work_responsibility_locations(practice_id,location_id)
);

CREATE TABLE work_reclassification_changes (
 run_id text NOT NULL,
 task_id uuid NOT NULL REFERENCES work_tasks(id),
 old_category text,
 new_category text NOT NULL,
 expected_version bigint NOT NULL,
 applied_version bigint NOT NULL,
 actor_subject text NOT NULL,
 applied_at timestamptz NOT NULL,
 PRIMARY KEY (run_id,task_id)
);
CREATE INDEX work_tasks_open_group_idx ON work_tasks(practice_id,location_id,phone,category,created_at,id) WHERE state='OPEN';
