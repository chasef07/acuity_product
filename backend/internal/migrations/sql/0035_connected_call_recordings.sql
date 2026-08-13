ALTER TABLE access_practices
    ADD COLUMN connected_call_recording_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN connected_call_recording_retention_days integer,
    ADD CONSTRAINT access_practices_recording_retention_check CHECK (
        connected_call_recording_retention_days IS NULL
        OR connected_call_recording_retention_days BETWEEN 1 AND 3650
    ),
    ADD CONSTRAINT access_practices_recording_policy_check CHECK (
        NOT connected_call_recording_enabled
        OR connected_call_recording_retention_days IS NOT NULL
    );

UPDATE access_practices
SET connected_call_recording_enabled = true,
    connected_call_recording_retention_days = 90
WHERE provisioning_key = 'abita-eye-group';

CREATE TABLE human_calling_call_recordings (
    call_id uuid PRIMARY KEY REFERENCES human_calling_calls(id) ON DELETE CASCADE,
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    audio_state text NOT NULL CHECK (
        audio_state IN ('PROCESSING', 'READY', 'UNAVAILABLE', 'DELETED')
    ),
    provider_recording_id text UNIQUE,
    retention_days integer NOT NULL CHECK (retention_days BETWEEN 1 AND 3650),
    recording_started_at timestamptz,
    recording_ended_at timestamptz,
    content_expires_at timestamptz,
    duration_millis bigint CHECK (duration_millis IS NULL OR duration_millis > 0),
    last_error_code text,
    content_deleted_at timestamptz,
    deletion_attempts integer NOT NULL DEFAULT 0 CHECK (deletion_attempts >= 0),
    deletion_claimed_at timestamptz,
    next_deletion_attempt_at timestamptz,
    deletion_error_code text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id),
    CHECK (
        (
            audio_state = 'PROCESSING'
            AND provider_recording_id IS NULL
            AND recording_started_at IS NULL
            AND recording_ended_at IS NULL
            AND content_expires_at IS NULL
            AND duration_millis IS NULL
            AND last_error_code IS NULL
            AND content_deleted_at IS NULL
            AND deletion_claimed_at IS NULL
            AND next_deletion_attempt_at IS NULL
            AND deletion_error_code IS NULL
        )
        OR (
            audio_state = 'READY'
            AND provider_recording_id IS NOT NULL
            AND recording_started_at IS NOT NULL
            AND recording_ended_at IS NOT NULL
            AND content_expires_at IS NOT NULL
            AND content_expires_at > recording_ended_at
            AND duration_millis IS NOT NULL
            AND last_error_code IS NULL
            AND content_deleted_at IS NULL
        )
        OR (
            audio_state = 'UNAVAILABLE'
            AND provider_recording_id IS NULL
            AND recording_started_at IS NULL
            AND recording_ended_at IS NULL
            AND content_expires_at IS NULL
            AND duration_millis IS NULL
            AND last_error_code IS NOT NULL
            AND content_deleted_at IS NULL
            AND deletion_claimed_at IS NULL
            AND next_deletion_attempt_at IS NULL
            AND deletion_error_code IS NULL
        )
        OR (
            audio_state = 'DELETED'
            AND provider_recording_id IS NOT NULL
            AND recording_started_at IS NOT NULL
            AND recording_ended_at IS NOT NULL
            AND content_expires_at IS NOT NULL
            AND content_expires_at > recording_ended_at
            AND duration_millis IS NOT NULL
            AND last_error_code IS NULL
            AND content_deleted_at IS NOT NULL
            AND deletion_claimed_at IS NULL
            AND next_deletion_attempt_at IS NULL
            AND deletion_error_code IS NULL
        )
    )
);

CREATE INDEX human_calling_call_recordings_retention_idx
    ON human_calling_call_recordings (
        content_expires_at,
        next_deletion_attempt_at,
        updated_at,
        call_id
    )
    WHERE audio_state = 'READY';
