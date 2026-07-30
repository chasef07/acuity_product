-- Slice 5 Messaging: location-scoped SMS/MMS conversations and delivery state.
ALTER TABLE work_tasks
    ADD CONSTRAINT work_tasks_id_practice_location_key
    UNIQUE (id, practice_id, location_id);

CREATE TABLE messaging_location_configurations (
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    sender text NOT NULL CHECK (sender ~ '^\+[1-9][0-9]{7,14}$'),
    messaging_profile_id text NOT NULL CHECK (
        messaging_profile_id = btrim(messaging_profile_id)
        AND char_length(messaging_profile_id) BETWEEN 1 AND 255
    ),
    active boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (practice_id, location_id),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id)
);

CREATE UNIQUE INDEX messaging_one_active_location_sender_idx
    ON messaging_location_configurations (sender)
    WHERE active;

CREATE TABLE messaging_threads (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    office_phone text NOT NULL CHECK (office_phone ~ '^\+[1-9][0-9]{7,14}$'),
    external_phone text NOT NULL CHECK (external_phone ~ '^\+[1-9][0-9]{7,14}$'),
    display_name text,
    name_source text,
    outbound_blocked boolean NOT NULL DEFAULT false,
    opt_out_evidence_at timestamptz,
    opt_out_evidence_event_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, practice_id, location_id),
    UNIQUE (practice_id, location_id, office_phone, external_phone),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id),
    CHECK (
        (display_name IS NULL AND name_source IS NULL)
        OR (
            display_name = btrim(display_name)
            AND char_length(display_name) BETWEEN 1 AND 200
            AND name_source = btrim(name_source)
            AND char_length(name_source) BETWEEN 1 AND 100
        )
    )
);

CREATE INDEX messaging_threads_location_activity_idx
    ON messaging_threads (practice_id, location_id, updated_at DESC, id DESC);

CREATE TABLE messaging_messages (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    thread_id uuid NOT NULL,
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    direction text NOT NULL CHECK (direction IN ('INBOUND', 'OUTBOUND')),
    body text CHECK (
        body IS NULL
        OR (
            body = btrim(body)
            AND char_length(body) BETWEEN 1 AND 1600
        )
    ),
    sender text NOT NULL CHECK (sender ~ '^\+[1-9][0-9]{7,14}$'),
    destination text NOT NULL CHECK (destination ~ '^\+[1-9][0-9]{7,14}$'),
    delivery_state text NOT NULL CHECK (
        delivery_state IN ('SENDING', 'SENT', 'DELIVERED', 'FAILED', 'UNKNOWN')
    ),
    safe_failure_code text,
    provider_message_id text UNIQUE,
    task_id uuid,
    retry_of_message_id uuid,
    created_by_subject text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    UNIQUE (id, thread_id),
    UNIQUE (id, practice_id, location_id),
    UNIQUE (id, thread_id, practice_id, location_id),
    UNIQUE (id, practice_id, location_id, direction),
    FOREIGN KEY (thread_id, practice_id, location_id)
        REFERENCES messaging_threads(id, practice_id, location_id),
    FOREIGN KEY (task_id, practice_id, location_id)
        REFERENCES work_tasks(id, practice_id, location_id),
    FOREIGN KEY (
        retry_of_message_id,
        thread_id,
        practice_id,
        location_id
    ) REFERENCES messaging_messages(id, thread_id, practice_id, location_id),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id),
    CHECK (
        (direction = 'OUTBOUND' AND created_by_subject IS NOT NULL)
        OR (direction = 'INBOUND' AND created_by_subject IS NULL)
    )
);

CREATE INDEX messaging_messages_thread_timeline_idx
    ON messaging_messages (thread_id, created_at, id);

CREATE TABLE messaging_attachments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    message_id uuid UNIQUE,
    direction text NOT NULL CHECK (direction IN ('INBOUND', 'OUTBOUND')),
    state text NOT NULL CHECK (
        state IN ('PENDING', 'PROCESSING', 'STORED', 'UNAVAILABLE')
    ),
    actor_subject text,
    file_name text NOT NULL CHECK (
        file_name = btrim(file_name)
        AND char_length(file_name) BETWEEN 1 AND 255
    ),
    content_type text NOT NULL CHECK (
        content_type IN (
            'image/jpeg',
            'image/png',
            'image/gif',
            'image/webp',
            'application/pdf'
        )
    ),
    byte_size integer CHECK (byte_size BETWEEN 1 AND 614400),
    object_key text UNIQUE,
    retry_idempotency_key text,
    retry_of_message_id uuid,
    provider_media_url text,
    expires_at timestamptz,
    copy_started_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id),
    FOREIGN KEY (message_id, practice_id, location_id, direction)
        REFERENCES messaging_messages(id, practice_id, location_id, direction),
    FOREIGN KEY (retry_of_message_id, practice_id, location_id)
        REFERENCES messaging_messages(id, practice_id, location_id),
    CHECK (
        (
            direction = 'OUTBOUND'
            AND actor_subject IS NOT NULL
            AND provider_media_url IS NULL
        )
        OR (
            direction = 'INBOUND'
            AND actor_subject IS NULL
            AND provider_media_url IS NOT NULL
            AND message_id IS NOT NULL
        )
    ),
    CHECK (
        (
            retry_idempotency_key IS NULL
            AND retry_of_message_id IS NULL
        )
        OR (
            direction = 'OUTBOUND'
            AND retry_idempotency_key = btrim(retry_idempotency_key)
            AND char_length(retry_idempotency_key) BETWEEN 1 AND 200
            AND retry_of_message_id IS NOT NULL
        )
    )
);

CREATE UNIQUE INDEX messaging_attachment_retry_idempotency_idx
    ON messaging_attachments (practice_id, actor_subject, retry_idempotency_key)
    WHERE retry_idempotency_key IS NOT NULL;

CREATE INDEX messaging_pending_attachment_copy_idx
    ON messaging_attachments (updated_at, id)
    WHERE direction = 'INBOUND' AND state = 'PROCESSING';

CREATE TABLE messaging_thread_unreads (
    thread_id uuid NOT NULL REFERENCES messaging_threads(id),
    user_subject text NOT NULL,
    unread_since timestamptz NOT NULL,
    latest_message_id uuid NOT NULL,
    FOREIGN KEY (latest_message_id, thread_id)
        REFERENCES messaging_messages(id, thread_id),
    PRIMARY KEY (thread_id, user_subject)
);

CREATE TABLE messaging_provider_commands (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id uuid NOT NULL UNIQUE,
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    actor_subject text NOT NULL,
    idempotency_key text NOT NULL CHECK (
        idempotency_key = btrim(idempotency_key)
        AND char_length(idempotency_key) BETWEEN 1 AND 200
    ),
    input_fingerprint bytea NOT NULL CHECK (octet_length(input_fingerprint) = 32),
    callback_token text NOT NULL UNIQUE,
    messaging_profile_id text NOT NULL,
    state text NOT NULL DEFAULT 'PENDING' CHECK (
        state IN ('PENDING', 'WRITING', 'SENT', 'UNKNOWN', 'FAILED', 'RECONCILING')
    ),
    provider_message_id text,
    write_started_at timestamptz,
    completed_at timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    reconcile_until timestamptz,
    last_error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (message_id, practice_id, location_id)
        REFERENCES messaging_messages(id, practice_id, location_id),
    UNIQUE (practice_id, actor_subject, idempotency_key)
);

CREATE INDEX messaging_pending_commands_idx
    ON messaging_provider_commands (next_attempt_at, created_at, id)
    WHERE state IN ('PENDING', 'RECONCILING');

CREATE TABLE messaging_provider_receipts (
    event_id text PRIMARY KEY,
    event_type text NOT NULL,
    callback_token text,
    occurred_at timestamptz,
    received_at timestamptz NOT NULL DEFAULT now(),
    signature_timestamp bigint NOT NULL,
    raw_body bytea NOT NULL,
    state text NOT NULL DEFAULT 'PENDING' CHECK (
        state IN ('PENDING', 'PROCESSING', 'APPLIED', 'UNKNOWN', 'FAILED')
    ),
    duplicate_count integer NOT NULL DEFAULT 0 CHECK (duplicate_count >= 0),
    processing_started_at timestamptz,
    projected_at timestamptz,
    projection_error_code text
);

CREATE INDEX messaging_pending_receipts_idx
    ON messaging_provider_receipts (received_at, event_id)
    WHERE state IN ('PENDING', 'PROCESSING');

CREATE FUNCTION messaging_preserve_message_content()
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
        NEW.created_by_subject,
        NEW.created_at
    ) THEN
        RAISE EXCEPTION 'Message content is immutable';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER messaging_preserve_message_content
BEFORE UPDATE ON messaging_messages
FOR EACH ROW
EXECUTE FUNCTION messaging_preserve_message_content();

ALTER TABLE work_tasks
    DROP CONSTRAINT work_tasks_origin_source_check,
    ADD COLUMN source_message_id uuid,
    ADD COLUMN message_thread_id uuid,
    ADD CONSTRAINT work_tasks_message_source_scope_fkey FOREIGN KEY (
        source_message_id,
        message_thread_id,
        practice_id,
        location_id
    ) REFERENCES messaging_messages(id, thread_id, practice_id, location_id);

ALTER TABLE work_tasks
    DROP CONSTRAINT work_tasks_origin_check,
    ADD CONSTRAINT work_tasks_origin_check CHECK (
        origin IN (
            'HUMAN_CALL_FOLLOW_UP',
            'ABITA_AI',
            'STAFF_MESSAGE_FOLLOW_UP'
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
        )
    );

CREATE UNIQUE INDEX work_tasks_message_source_idx
    ON work_tasks (source_message_id)
    WHERE source_message_id IS NOT NULL;

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
        OLD.message_thread_id
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
        NEW.message_thread_id
    ) THEN
        RAISE EXCEPTION 'Task source is immutable';
    END IF;
    RETURN NEW;
END
$$;
