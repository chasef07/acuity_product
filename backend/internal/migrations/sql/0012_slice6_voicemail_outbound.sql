CREATE TABLE human_calling_location_voice_numbers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    phone text NOT NULL CHECK (phone ~ '^\+1[2-9][0-9]{9}$'),
    enabled boolean NOT NULL DEFAULT true,
    voicemail_greeting_url text CHECK (
        voicemail_greeting_url IS NULL
        OR (
            voicemail_greeting_url = btrim(voicemail_greeting_url)
            AND char_length(voicemail_greeting_url) BETWEEN 1 AND 2048
        )
    ),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (practice_id, location_id, phone),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id)
);

ALTER TABLE human_calling_calls
    DROP CONSTRAINT human_calling_calls_state_check,
    DROP CONSTRAINT human_calling_calls_handoff_id_key,
    ALTER COLUMN handoff_id DROP NOT NULL,
    ALTER COLUMN caller_call_control_id DROP NOT NULL,
    ALTER COLUMN caller_call_leg_id DROP NOT NULL,
    ALTER COLUMN call_session_id DROP NOT NULL,
    ADD CONSTRAINT human_calling_calls_handoff_id_key UNIQUE (handoff_id),
    ADD COLUMN direction text NOT NULL DEFAULT 'INBOUND'
        CHECK (direction IN ('INBOUND', 'OUTBOUND')),
    ADD COLUMN entry_point text NOT NULL DEFAULT 'AI_HANDOFF'
        CHECK (entry_point IN ('AI_HANDOFF', 'TASK', 'STANDALONE')),
    ADD COLUMN task_id uuid REFERENCES work_tasks(id),
    ADD COLUMN destination_phone text CHECK (
        destination_phone IS NULL
        OR destination_phone ~ '^\+1[2-9][0-9]{9}$'
    ),
    ADD COLUMN outbound_caller_id text CHECK (
        outbound_caller_id IS NULL
        OR outbound_caller_id ~ '^\+1[2-9][0-9]{9}$'
    ),
    ADD COLUMN initiating_subject text,
    ADD COLUMN outbound_idempotency_key text,
    ADD COLUMN outbound_input_fingerprint bytea,
    ADD COLUMN retry_of_call_id uuid REFERENCES human_calling_calls(id),
    ADD COLUMN prior_availability_intent boolean,
    ADD COLUMN destination_call_control_id text UNIQUE,
    ADD COLUMN destination_call_leg_id text UNIQUE,
    ADD COLUMN disposition_outcome text CHECK (
        disposition_outcome IN (
            'RESOLVED',
            'FOLLOW_UP_REQUIRED',
            'COMPLETE_TASK',
            'KEEP_OPEN',
            'CREATE_TASK',
            'NO_FOLLOW_UP'
        )
    ),
    ADD COLUMN voicemail_failure_deadline timestamptz,
    ADD COLUMN voicemail_failure_event_id text,
    ADD CONSTRAINT human_calling_calls_state_check CHECK (state IN (
        'OFFERING',
        'PREPARING',
        'RINGING',
        'CONNECTING',
        'CONNECTED',
        'RECONCILING',
        'UNANSWERED',
        'VOICEMAIL',
        'MISSED',
        'NEEDS_DISPOSITION',
        'RESOLVED',
        'FOLLOW_UP_REQUIRED'
    )),
    ADD CONSTRAINT human_calling_calls_direction_source_check CHECK (
        (
            direction = 'INBOUND'
            AND entry_point = 'AI_HANDOFF'
            AND handoff_id IS NOT NULL
            AND task_id IS NULL
            AND destination_phone IS NULL
            AND outbound_caller_id IS NULL
            AND initiating_subject IS NULL
            AND outbound_idempotency_key IS NULL
            AND outbound_input_fingerprint IS NULL
            AND retry_of_call_id IS NULL
            AND prior_availability_intent IS NULL
            AND (
                (voicemail_failure_deadline IS NULL AND voicemail_failure_event_id IS NULL)
                OR (
                    voicemail_failure_deadline IS NOT NULL
                    AND voicemail_failure_event_id IS NOT NULL
                )
            )
        )
        OR (
            direction = 'OUTBOUND'
            AND entry_point IN ('TASK', 'STANDALONE')
            AND handoff_id IS NULL
            AND caller_call_control_id IS NULL
            AND caller_call_leg_id IS NULL
            AND destination_phone IS NOT NULL
            AND outbound_caller_id IS NOT NULL
            AND initiating_subject IS NOT NULL
            AND outbound_idempotency_key IS NOT NULL
            AND octet_length(outbound_input_fingerprint) = 32
            AND prior_availability_intent IS NOT NULL
            AND voicemail_failure_deadline IS NULL
            AND voicemail_failure_event_id IS NULL
            AND (
                (entry_point = 'TASK' AND task_id IS NOT NULL)
                OR (entry_point = 'STANDALONE' AND task_id IS NULL)
            )
        )
    );

ALTER TABLE human_calling_connection_attempts
    ADD COLUMN staff_answered_at timestamptz,
    ADD COLUMN media_ready_at timestamptz,
    ADD CONSTRAINT human_calling_attempt_media_ready_check CHECK (
        media_ready_at IS NULL OR staff_answered_at IS NOT NULL
    );

CREATE INDEX human_calling_pending_voicemail_failures_idx
    ON human_calling_calls (voicemail_failure_deadline, id)
    WHERE voicemail_failure_deadline IS NOT NULL;

DROP INDEX human_calling_one_live_call_per_user_idx;

CREATE UNIQUE INDEX human_calling_one_live_call_per_user_idx
    ON human_calling_calls (claimant_subject)
    WHERE state IN (
        'PREPARING',
        'RINGING',
        'CONNECTING',
        'CONNECTED',
        'RECONCILING'
    );

CREATE UNIQUE INDEX human_calling_outbound_idempotency_idx
    ON human_calling_calls (initiating_subject, outbound_idempotency_key)
    WHERE direction = 'OUTBOUND';

ALTER TABLE human_calling_provider_commands
    DROP CONSTRAINT human_calling_provider_commands_action_check,
    ADD CONSTRAINT human_calling_provider_commands_action_check CHECK (
        action IN (
            'ANSWER_CALLER',
            'START_RINGBACK',
            'DIAL_STAFF',
            'DIAL_DESTINATION',
            'PLAY_VOICEMAIL_GREETING',
            'START_VOICEMAIL_RECORDING',
            'HANGUP',
            'START_RECORDING',
            'CREATE_CREDENTIAL',
            'DISABLE_CREDENTIAL',
            'CREATE_JWT'
        )
    );

CREATE TABLE human_calling_voicemails (
    call_id uuid PRIMARY KEY REFERENCES human_calling_calls(id),
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    task_id uuid NOT NULL UNIQUE REFERENCES work_tasks(id),
    outcome text NOT NULL CHECK (outcome IN ('VOICEMAIL', 'MISSED_CALL')),
    audio_state text CHECK (
        audio_state IS NULL
        OR audio_state IN ('PROCESSING', 'READY', 'UNAVAILABLE')
    ),
    provider_recording_id text UNIQUE,
    provider_recording_url text,
    recording_started_at timestamptz,
    recording_ended_at timestamptz,
    duration_millis bigint CHECK (duration_millis IS NULL OR duration_millis > 0),
    object_key text UNIQUE,
    content_type text,
    byte_size bigint CHECK (byte_size IS NULL OR byte_size >= 0),
    copy_attempts integer NOT NULL DEFAULT 0 CHECK (copy_attempts >= 0),
    next_copy_at timestamptz,
    last_error_code text,
    copied_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id),
    CHECK (
        (
            outcome = 'MISSED_CALL'
            AND audio_state IS NULL
            AND provider_recording_id IS NULL
            AND provider_recording_url IS NULL
            AND recording_started_at IS NULL
            AND recording_ended_at IS NULL
            AND duration_millis IS NULL
            AND object_key IS NULL
            AND content_type IS NULL
            AND byte_size IS NULL
            AND next_copy_at IS NULL
            AND copied_at IS NULL
        )
        OR (
            outcome = 'VOICEMAIL'
            AND audio_state IS NOT NULL
            AND provider_recording_id IS NOT NULL
            AND recording_started_at IS NOT NULL
            AND recording_ended_at IS NOT NULL
            AND duration_millis IS NOT NULL
            AND object_key IS NOT NULL
            AND (
                (
                    audio_state = 'PROCESSING'
                    AND provider_recording_url IS NOT NULL
                    AND next_copy_at IS NOT NULL
                    AND copied_at IS NULL
                )
                OR (
                    audio_state = 'READY'
                    AND provider_recording_url IS NULL
                    AND content_type IS NOT NULL
                    AND byte_size IS NOT NULL
                    AND next_copy_at IS NULL
                    AND copied_at IS NOT NULL
                )
                OR (
                    audio_state = 'UNAVAILABLE'
                    AND provider_recording_url IS NULL
                    AND next_copy_at IS NULL
                    AND copied_at IS NULL
                )
            )
        )
    )
);

CREATE INDEX human_calling_pending_voicemail_copies_idx
    ON human_calling_voicemails (next_copy_at, created_at, call_id)
    WHERE audio_state = 'PROCESSING';

ALTER TABLE work_tasks
    DROP CONSTRAINT work_tasks_origin_source_check,
    DROP CONSTRAINT work_tasks_origin_check,
    ADD COLUMN recovery_outcome text CHECK (
        recovery_outcome IS NULL
        OR recovery_outcome IN ('VOICEMAIL', 'MISSED_CALL')
    ),
    ADD CONSTRAINT work_tasks_origin_check CHECK (
        origin IN (
            'HUMAN_CALL_FOLLOW_UP',
            'ABITA_AI',
            'STAFF_MESSAGE_FOLLOW_UP',
            'VOICEMAIL_RECOVERY',
            'MISSED_CALL_RECOVERY'
        )
    ),
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
            AND source_message_id IS NULL
            AND message_thread_id IS NULL
            AND recovery_outcome IS NULL
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
            AND source_message_id IS NULL
            AND message_thread_id IS NULL
            AND recovery_outcome IS NULL
        )
        OR (
            origin = 'STAFF_MESSAGE_FOLLOW_UP'
            AND call_id IS NULL
            AND urgency = 'normal'
            AND created_by_kind = 'HUMAN'
            AND created_by_email IS NOT NULL
            AND caller_name IS NULL
            AND source_call_id IS NULL
            AND source_message IS NULL
            AND category IS NULL
            AND ai_idempotency_key IS NULL
            AND ai_input_fingerprint IS NULL
            AND source_message_id IS NOT NULL
            AND message_thread_id IS NOT NULL
            AND recovery_outcome IS NULL
        )
        OR (
            origin IN ('VOICEMAIL_RECOVERY', 'MISSED_CALL_RECOVERY')
            AND call_id IS NOT NULL
            AND urgency = 'normal'
            AND created_by_kind = 'SERVICE'
            AND created_by_email IS NULL
            AND source_call_id IS NULL
            AND source_message IS NULL
            AND category IS NULL
            AND ai_idempotency_key IS NULL
            AND ai_input_fingerprint IS NULL
            AND source_message_id IS NULL
            AND message_thread_id IS NULL
            AND (
                (origin = 'VOICEMAIL_RECOVERY' AND recovery_outcome = 'VOICEMAIL')
                OR (
                    origin = 'MISSED_CALL_RECOVERY'
                    AND recovery_outcome = 'MISSED_CALL'
                )
            )
        )
    );

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
