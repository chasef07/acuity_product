-- Provider-owned voicemail rows use the durable Telnyx recording identity.
-- Keep the prior object/copy columns and values as read-only legacy evidence so
-- an overlapping older revision and historical rows remain schema-compatible.
ALTER TABLE human_calling_voicemails
    DROP CONSTRAINT human_calling_voicemails_check,
    ADD CONSTRAINT human_calling_voicemails_check CHECK (
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
            AND (
                (
                    audio_state = 'READY'
                    AND provider_recording_url IS NULL
                    AND object_key IS NULL
                    AND content_type IS NULL
                    AND byte_size IS NULL
                    AND copy_attempts = 0
                    AND next_copy_at IS NULL
                    AND last_error_code IS NULL
                    AND copied_at IS NULL
                )
                OR (
                    object_key IS NOT NULL
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
        )
    );

DROP INDEX IF EXISTS human_calling_pending_voicemail_copies_idx;

COMMENT ON TABLE human_calling_recordings IS
    'Legacy connected-call recording lifecycle evidence; the active runtime does not create new rows.';
COMMENT ON COLUMN human_calling_recordings.bucket IS
    'Legacy object-store evidence only.';
COMMENT ON COLUMN human_calling_recordings.object_key IS
    'Legacy object-store evidence only.';

COMMENT ON COLUMN human_calling_voicemails.provider_recording_url IS
    'Legacy copy evidence only; new voicemail playback refreshes through provider_recording_id.';
COMMENT ON COLUMN human_calling_voicemails.object_key IS
    'Legacy object-store evidence only; new voicemail audio remains provider-owned.';
COMMENT ON COLUMN human_calling_voicemails.content_type IS
    'Legacy object-store evidence only.';
COMMENT ON COLUMN human_calling_voicemails.byte_size IS
    'Legacy object-store evidence only.';
COMMENT ON COLUMN human_calling_voicemails.copy_attempts IS
    'Legacy copy evidence only; new voicemail rows remain zero.';
COMMENT ON COLUMN human_calling_voicemails.next_copy_at IS
    'Legacy copy evidence only; no runtime consumes this schedule.';
COMMENT ON COLUMN human_calling_voicemails.last_error_code IS
    'Legacy copy evidence only.';
COMMENT ON COLUMN human_calling_voicemails.copied_at IS
    'Legacy object-store evidence only.';
