CREATE VIEW human_calling_operational_users AS
SELECT DISTINCT membership.user_subject
FROM access_memberships membership
WHERE membership.revoked_at IS NULL
    AND NOT EXISTS (
        SELECT 1
        FROM access_platform_operators operator
        WHERE operator.user_subject = membership.user_subject
            OR operator.email = membership.email
    );

CREATE TABLE human_calling_handoffs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    service_subject text NOT NULL,
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    source_call_id text NOT NULL,
    idempotency_key text NOT NULL,
    input_fingerprint bytea NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    phone text,
    phone_source text,
    display_name text,
    name_source text,
    transfer_reason text,
    reason_source text,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (service_subject, idempotency_key),
    UNIQUE (service_subject, source_call_id),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id)
);

CREATE TABLE human_calling_calls (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    handoff_id uuid NOT NULL UNIQUE REFERENCES human_calling_handoffs(id),
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN (
        'OFFERING',
        'CONNECTING',
        'CONNECTED',
        'RECONCILING',
        'UNANSWERED',
        'NEEDS_DISPOSITION',
        'RESOLVED',
        'FOLLOW_UP_REQUIRED'
    )),
    offer_deadline timestamptz NOT NULL,
    connection_deadline timestamptz,
    caller_call_control_id text NOT NULL UNIQUE,
    caller_call_leg_id text NOT NULL UNIQUE,
    call_session_id text NOT NULL,
    claimant_subject text,
    winner_subject text,
    claimant_session_id text,
    expected_staff_call_control_id text,
    expected_staff_call_leg_id text,
    provider_termination text,
    disposition_actor_subject text,
    disposition_at timestamptz,
    connected_at timestamptz,
    ended_at timestamptz,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id),
    CHECK (
        (winner_subject IS NULL AND connected_at IS NULL)
        OR (winner_subject IS NOT NULL AND connected_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX human_calling_one_live_call_per_user_idx
    ON human_calling_calls (claimant_subject)
    WHERE state IN ('CONNECTING', 'CONNECTED', 'RECONCILING', 'NEEDS_DISPOSITION');

CREATE INDEX human_calling_current_offers_idx
    ON human_calling_calls (offer_deadline, id)
    WHERE state = 'OFFERING';

CREATE TABLE human_calling_connection_attempts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    call_id uuid NOT NULL REFERENCES human_calling_calls(id) ON DELETE CASCADE,
    claimant_subject text NOT NULL,
    claimant_session_id text NOT NULL,
    connection_deadline timestamptz NOT NULL,
    staff_call_control_id text,
    staff_call_leg_id text,
    bridge_occurred_at timestamptz,
    ended_at timestamptz,
    provider_termination text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (staff_call_control_id IS NULL AND staff_call_leg_id IS NULL)
        OR (staff_call_control_id IS NOT NULL AND staff_call_leg_id IS NOT NULL)
    )
);

CREATE INDEX human_calling_attempts_call_time_idx
    ON human_calling_connection_attempts (call_id, created_at, id);

CREATE UNIQUE INDEX human_calling_attempt_staff_leg_idx
    ON human_calling_connection_attempts (
        call_id,
        staff_call_control_id,
        staff_call_leg_id
    )
    WHERE staff_call_control_id IS NOT NULL;

ALTER TABLE human_calling_calls
    ADD COLUMN current_attempt_id uuid
        REFERENCES human_calling_connection_attempts(id)
        DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE human_calling_softphone_leases (
    user_subject text PRIMARY KEY,
    session_id text NOT NULL,
    lease_expires_at timestamptz NOT NULL,
    desired_available boolean NOT NULL DEFAULT false,
    registered boolean NOT NULL DEFAULT false,
    microphone_ready boolean NOT NULL DEFAULT false,
    audio_ready boolean NOT NULL DEFAULT false,
    session_healthy boolean NOT NULL DEFAULT false,
    readiness_updated_at timestamptz NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE human_calling_credentials (
    user_subject text PRIMARY KEY,
    provider_credential_id text UNIQUE,
    provider_sip_username text UNIQUE,
    state text NOT NULL CHECK (state IN ('PENDING', 'ACTIVE', 'DISABLING', 'DISABLED', 'FAILED')),
    last_error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE human_calling_provider_commands (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    call_id uuid REFERENCES human_calling_calls(id),
    attempt_id uuid REFERENCES human_calling_connection_attempts(id),
    user_subject text,
    action text NOT NULL CHECK (action IN (
        'ANSWER_CALLER',
        'START_RINGBACK',
        'DIAL_STAFF',
        'HANGUP',
        'START_RECORDING',
        'CREATE_CREDENTIAL',
        'DISABLE_CREDENTIAL',
        'CREATE_JWT'
    )),
    target_id text,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    state text NOT NULL DEFAULT 'PENDING' CHECK (state IN (
        'PENDING',
        'SENDING',
        'SENT',
        'AMBIGUOUS',
        'FAILED',
        'RECONCILED'
    )),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    depends_on_command_id uuid REFERENCES human_calling_provider_commands(id),
    last_error_code text,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX human_calling_pending_commands_idx
    ON human_calling_provider_commands (next_attempt_at, created_at)
    WHERE state = 'PENDING';

CREATE UNIQUE INDEX human_calling_one_active_credential_create_idx
    ON human_calling_provider_commands (user_subject)
    WHERE action = 'CREATE_CREDENTIAL'
        AND state IN ('PENDING', 'SENDING', 'AMBIGUOUS');

CREATE TABLE human_calling_provider_receipts (
    event_id text PRIMARY KEY,
    call_id uuid REFERENCES human_calling_calls(id),
    event_type text NOT NULL,
    occurred_at timestamptz,
    received_at timestamptz NOT NULL DEFAULT now(),
    signature_timestamp bigint NOT NULL,
    raw_body bytea NOT NULL,
    state text NOT NULL DEFAULT 'PENDING' CHECK (state IN (
        'PENDING',
        'PROCESSING',
        'APPLIED',
        'UNKNOWN',
        'FAILED'
    )),
    duplicate_count integer NOT NULL DEFAULT 0 CHECK (duplicate_count >= 0),
    projection_error_code text,
    processing_started_at timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    projected_at timestamptz
);

CREATE INDEX human_calling_pending_receipts_idx
    ON human_calling_provider_receipts (next_attempt_at, received_at, event_id)
    WHERE state IN ('PENDING', 'PROCESSING');

CREATE TABLE human_calling_projected_facts (
    event_id text PRIMARY KEY,
    event_type text NOT NULL,
    applied_at timestamptz NOT NULL
);

CREATE TABLE human_calling_recordings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    call_id uuid NOT NULL UNIQUE REFERENCES human_calling_calls(id),
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    provider_recording_id text UNIQUE,
    bucket text NOT NULL,
    object_key text NOT NULL,
    state text NOT NULL CHECK (state IN ('INTENDED', 'RECORDING', 'READY', 'FAILED')),
    started_at timestamptz,
    ready_at timestamptz,
    last_event_at timestamptz,
    failure_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (bucket, object_key)
);

CREATE TABLE human_calling_timeline (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    call_id uuid NOT NULL REFERENCES human_calling_calls(id),
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    kind text NOT NULL,
    actor_subject text,
    provider_event_id text,
    provider_command_id uuid,
    opaque_reference text,
    error_code text,
    occurred_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (
        call_id,
        kind,
        provider_event_id,
        provider_command_id,
        opaque_reference
    )
);

CREATE INDEX human_calling_timeline_call_idx
    ON human_calling_timeline (call_id, occurred_at, id);
