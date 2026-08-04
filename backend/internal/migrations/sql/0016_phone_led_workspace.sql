-- acuity:retired
-- PR #44 was rolled back. Current runners record this migration without
-- executing it; the body remains available to model already-deployed schemas.
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

-- Consolidate already-compatible open recovery work before enforcing the
-- invariant for new writes. The oldest Task remains the accountable object;
-- richer voicemail evidence promotes its presentation.
CREATE TEMP TABLE work_recovery_task_merge ON COMMIT DROP AS
SELECT duplicate.id AS duplicate_id, canonical.id AS canonical_id
FROM work_tasks duplicate
JOIN LATERAL (
    SELECT candidate.id
    FROM work_tasks candidate
    WHERE candidate.practice_id = duplicate.practice_id
        AND candidate.location_id = duplicate.location_id
        AND candidate.phone = duplicate.phone
        AND candidate.state = 'OPEN'
        AND candidate.origin IN ('VOICEMAIL_RECOVERY', 'MISSED_CALL_RECOVERY')
    ORDER BY candidate.created_at, candidate.id
    LIMIT 1
) canonical ON true
WHERE duplicate.state = 'OPEN'
    AND duplicate.origin IN ('VOICEMAIL_RECOVERY', 'MISSED_CALL_RECOVERY')
    AND duplicate.id <> canonical.id;

UPDATE human_calling_voicemails voicemail
SET task_id = merge.canonical_id
FROM work_recovery_task_merge merge
WHERE voicemail.task_id = merge.duplicate_id;

UPDATE work_task_interactions interaction
SET task_id = merge.canonical_id
FROM work_recovery_task_merge merge
WHERE interaction.task_id = merge.duplicate_id;

UPDATE work_tasks canonical
SET
    title = 'Review voicemail',
    origin = 'VOICEMAIL_RECOVERY',
    recovery_outcome = 'VOICEMAIL'
WHERE canonical.id IN (
    SELECT DISTINCT merge.canonical_id
    FROM work_recovery_task_merge merge
    JOIN work_tasks duplicate ON duplicate.id = merge.duplicate_id
    WHERE duplicate.origin = 'VOICEMAIL_RECOVERY'
);

DELETE FROM work_task_activities activity
USING work_recovery_task_merge merge
WHERE activity.task_id = merge.duplicate_id;

DELETE FROM work_tasks task
USING work_recovery_task_merge merge
WHERE task.id = merge.duplicate_id;

CREATE UNIQUE INDEX work_tasks_one_open_recovery_need_idx
    ON work_tasks (practice_id, location_id, phone)
    WHERE state = 'OPEN'
        AND origin IN ('VOICEMAIL_RECOVERY', 'MISSED_CALL_RECOVERY');

-- Same-location warm transfers keep the current staff member authoritative
-- until a recipient's provider leg is confirmed bridged.
CREATE TABLE human_calling_staff_transfers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    call_id uuid NOT NULL REFERENCES human_calling_calls(id) ON DELETE CASCADE,
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    requested_by_subject text NOT NULL,
    requested_by_session_id text NOT NULL,
    recipient_subject text NOT NULL,
    recipient_session_id text,
    handoff_note text NOT NULL CHECK (char_length(handoff_note) <= 500),
    state text NOT NULL CHECK (state IN (
        'REQUESTED', 'ACCEPTED', 'COMPLETED', 'DECLINED',
        'CANCELLED', 'EXPIRED', 'FAILED'
    )),
    expires_at timestamptz NOT NULL,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id),
    CHECK (requested_by_subject <> recipient_subject)
);

CREATE UNIQUE INDEX human_calling_one_active_staff_transfer_idx
    ON human_calling_staff_transfers (call_id)
    WHERE state IN ('REQUESTED', 'ACCEPTED');

CREATE INDEX human_calling_staff_transfer_recipient_idx
    ON human_calling_staff_transfers (recipient_subject, state, expires_at);

ALTER TABLE human_calling_connection_attempts
    ADD COLUMN staff_transfer_id uuid
        REFERENCES human_calling_staff_transfers(id);

CREATE UNIQUE INDEX human_calling_staff_transfer_attempt_idx
    ON human_calling_connection_attempts (staff_transfer_id)
    WHERE staff_transfer_id IS NOT NULL;
