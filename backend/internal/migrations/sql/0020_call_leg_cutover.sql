DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM human_calling_calls
        WHERE state IN (
            'OFFERING', 'PREPARING', 'RINGING', 'CONNECTING',
            'CONNECTED', 'RECONCILING', 'NEEDS_DISPOSITION'
        )
            OR (state = 'UNANSWERED' AND ended_at IS NULL)
    ) THEN
        RAISE EXCEPTION 'CallLeg cutover requires zero active Calls';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM human_calling_provider_commands
        WHERE state IN ('PENDING', 'SENDING', 'AMBIGUOUS')
    ) THEN
        RAISE EXCEPTION 'CallLeg cutover requires zero in-flight provider commands';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM human_calling_provider_receipts
        WHERE state IN ('PENDING', 'PROCESSING', 'QUARANTINED')
    ) THEN
        RAISE EXCEPTION 'CallLeg cutover requires zero unprojected provider receipts';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM human_calling_voicemails
        WHERE audio_state = 'PROCESSING' OR next_copy_at IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'CallLeg cutover requires zero voicemail work in flight';
    END IF;
    IF EXISTS (
        SELECT 1 FROM human_calling_calls
        WHERE (caller_call_control_id IS NULL) <> (caller_call_leg_id IS NULL)
            OR (destination_call_control_id IS NULL) <> (destination_call_leg_id IS NULL)
    ) THEN
        RAISE EXCEPTION 'CallLeg cutover found incomplete provider leg identity';
    END IF;
    IF EXISTS (
        SELECT 1 FROM human_calling_credentials
        WHERE state = 'ACTIVE'
            AND (provider_credential_id IS NULL OR provider_sip_username IS NULL)
    ) THEN
        RAISE EXCEPTION 'CallLeg cutover requires current active Staff credential mappings';
    END IF;
END
$$;

CREATE TABLE human_calling_call_legs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    call_id uuid NOT NULL REFERENCES human_calling_calls(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('CALLER', 'STAFF', 'DESTINATION')),
    sequence integer NOT NULL CHECK (sequence > 0),
    staff_subject text,
    staff_session_id text,
    state text NOT NULL CHECK (state IN (
        'PENDING', 'DIALING', 'RINGING', 'BRIDGE_PENDING',
        'BRIDGED', 'ENDING', 'ENDED', 'FAILED'
    )),
    provider_connection_id text,
    provider_call_control_id text,
    provider_call_leg_id text,
    provider_call_session_id text,
    answered_at timestamptz,
    bridge_pending_at timestamptz,
    bridged_at timestamptz,
    ending_at timestamptz,
    ended_at timestamptz,
    hangup_cause text,
    termination_source text,
    sip_cause text,
    error_code text,
    call_quality_stats jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (role = 'STAFF' AND staff_subject IS NOT NULL)
        OR (role <> 'STAFF' AND staff_subject IS NULL AND staff_session_id IS NULL)
    ),
    CHECK (call_quality_stats IS NULL OR jsonb_typeof(call_quality_stats) = 'object'),
    CHECK (bridge_pending_at IS NULL OR answered_at IS NOT NULL),
    CHECK (bridged_at IS NULL OR bridge_pending_at IS NOT NULL),
    CHECK (ending_at IS NULL OR answered_at IS NULL OR ending_at >= answered_at),
    CHECK (ended_at IS NULL OR ending_at IS NULL OR ended_at >= ending_at),
    CHECK (
        (state IN ('ENDED', 'FAILED') AND ended_at IS NOT NULL)
        OR (state NOT IN ('ENDED', 'FAILED') AND ended_at IS NULL)
    )
);

CREATE UNIQUE INDEX human_calling_one_caller_leg_idx
    ON human_calling_call_legs (call_id)
    WHERE role = 'CALLER';
CREATE UNIQUE INDEX human_calling_provider_call_control_leg_idx
    ON human_calling_call_legs (provider_call_control_id)
    WHERE provider_call_control_id IS NOT NULL;
CREATE UNIQUE INDEX human_calling_provider_call_leg_idx
    ON human_calling_call_legs (provider_call_leg_id)
    WHERE provider_call_leg_id IS NOT NULL;
CREATE UNIQUE INDEX human_calling_one_live_staff_leg_idx
    ON human_calling_call_legs (call_id, staff_subject)
    WHERE role = 'STAFF' AND state NOT IN ('ENDED', 'FAILED');
CREATE UNIQUE INDEX human_calling_one_provisional_winner_idx
    ON human_calling_call_legs (call_id)
    WHERE role = 'STAFF' AND state IN ('BRIDGE_PENDING', 'BRIDGED');
CREATE UNIQUE INDEX human_calling_one_staff_occupancy_idx
    ON human_calling_call_legs (staff_subject)
    WHERE role = 'STAFF' AND (
        state IN ('BRIDGE_PENDING', 'BRIDGED')
        OR (state = 'ENDING' AND answered_at IS NOT NULL)
    );
CREATE INDEX human_calling_call_legs_call_idx
    ON human_calling_call_legs (call_id, created_at, id);
CREATE INDEX human_calling_stale_call_legs_idx
    ON human_calling_call_legs (updated_at, id)
    WHERE state NOT IN ('ENDED', 'FAILED');

INSERT INTO human_calling_call_legs (
    id, call_id, role, sequence, state,
    provider_call_control_id, provider_call_leg_id, provider_call_session_id,
    answered_at, bridged_at, bridge_pending_at, ending_at, ended_at,
    hangup_cause, termination_source, created_at, updated_at
)
SELECT
    gen_random_uuid(), call.id, 'CALLER', 1,
    CASE
        WHEN call.ended_at IS NOT NULL THEN 'ENDED'
        WHEN call.connected_at IS NOT NULL THEN 'BRIDGED'
        ELSE 'RINGING'
    END,
    call.caller_call_control_id,
    call.caller_call_leg_id,
    call.call_session_id,
    CASE WHEN call.connected_at IS NOT NULL THEN call.connected_at END,
    call.connected_at,
    call.connected_at,
    CASE WHEN call.ended_at IS NOT NULL THEN call.ended_at END,
    call.ended_at,
    call.provider_termination,
    CASE WHEN call.provider_termination IS NOT NULL THEN 'PROVIDER' END,
    call.created_at,
    call.updated_at
FROM human_calling_calls call
;

INSERT INTO human_calling_call_legs (
    id, call_id, role, sequence, staff_subject, staff_session_id, state,
    provider_call_control_id, provider_call_leg_id, provider_call_session_id,
    answered_at, bridge_pending_at, bridged_at, ending_at, ended_at,
    hangup_cause, termination_source, created_at, updated_at
)
SELECT
    attempt.id,
    attempt.call_id,
    'STAFF',
    row_number() OVER (
        PARTITION BY attempt.call_id, attempt.claimant_subject
        ORDER BY attempt.created_at, attempt.id
    )::integer,
    attempt.claimant_subject,
    attempt.claimant_session_id,
    CASE
        WHEN attempt.ended_at IS NOT NULL THEN 'ENDED'
        WHEN attempt.bridge_occurred_at IS NOT NULL THEN 'BRIDGED'
        WHEN attempt.staff_answered_at IS NOT NULL THEN 'BRIDGE_PENDING'
        WHEN attempt.staff_call_control_id IS NOT NULL THEN 'RINGING'
        ELSE 'FAILED'
    END,
    attempt.staff_call_control_id,
    attempt.staff_call_leg_id,
    call.call_session_id,
    attempt.staff_answered_at,
    CASE
        WHEN attempt.staff_answered_at IS NOT NULL
            THEN attempt.staff_answered_at
    END,
    attempt.bridge_occurred_at,
    CASE WHEN attempt.ended_at IS NOT NULL THEN attempt.ended_at END,
    COALESCE(attempt.ended_at, CASE
        WHEN attempt.staff_call_control_id IS NULL THEN attempt.updated_at
    END),
    attempt.provider_termination,
    CASE WHEN attempt.provider_termination IS NOT NULL THEN 'PROVIDER' END,
    attempt.created_at,
    attempt.updated_at
FROM human_calling_connection_attempts attempt
JOIN human_calling_calls call ON call.id = attempt.call_id;

INSERT INTO human_calling_call_legs (
    id, call_id, role, sequence, state,
    provider_call_control_id, provider_call_leg_id, provider_call_session_id,
    answered_at, bridge_pending_at, bridged_at, ending_at, ended_at,
    hangup_cause, termination_source, created_at, updated_at
)
SELECT
    gen_random_uuid(), call.id, 'DESTINATION', 1,
    CASE
        WHEN call.ended_at IS NOT NULL THEN 'ENDED'
        WHEN call.connected_at IS NOT NULL THEN 'BRIDGED'
        ELSE 'RINGING'
    END,
    call.destination_call_control_id,
    call.destination_call_leg_id,
    call.call_session_id,
    call.connected_at,
    call.connected_at,
    call.connected_at,
    CASE WHEN call.ended_at IS NOT NULL THEN call.ended_at END,
    call.ended_at,
    call.provider_termination,
    CASE WHEN call.provider_termination IS NOT NULL THEN 'PROVIDER' END,
    call.created_at,
    call.updated_at
FROM human_calling_calls call
WHERE call.destination_call_control_id IS NOT NULL
    AND call.destination_call_leg_id IS NOT NULL;

DO $$
BEGIN
    IF (SELECT count(*) FROM human_calling_call_legs WHERE role = 'CALLER') <>
        (SELECT count(*) FROM human_calling_calls) THEN
        RAISE EXCEPTION 'CallLeg cutover Caller backfill count mismatch';
    END IF;
    IF (SELECT count(*) FROM human_calling_call_legs WHERE role = 'STAFF') <>
        (SELECT count(*) FROM human_calling_connection_attempts) THEN
        RAISE EXCEPTION 'CallLeg cutover Staff backfill count mismatch';
    END IF;
    IF (SELECT count(*) FROM human_calling_call_legs WHERE role = 'DESTINATION') <>
        (SELECT count(*) FROM human_calling_calls
            WHERE destination_call_control_id IS NOT NULL) THEN
        RAISE EXCEPTION 'CallLeg cutover Destination backfill count mismatch';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM human_calling_calls call
        WHERE call.winner_subject IS NOT NULL
            AND 1 <> (
                SELECT count(*)
                FROM human_calling_call_legs leg
                WHERE leg.call_id = call.id
                    AND leg.role = 'STAFF'
                    AND leg.staff_subject = call.winner_subject
                    AND leg.bridged_at IS NOT NULL
            )
    ) THEN
        RAISE EXCEPTION 'CallLeg cutover winner backfill mismatch';
    END IF;
END
$$;

ALTER TABLE human_calling_provider_commands
    ADD COLUMN call_leg_id uuid REFERENCES human_calling_call_legs(id),
    ADD COLUMN peer_call_leg_id uuid REFERENCES human_calling_call_legs(id);

UPDATE human_calling_provider_commands command
SET call_leg_id = command.attempt_id
WHERE command.attempt_id IS NOT NULL;

UPDATE human_calling_provider_commands command
SET call_leg_id = leg.id
FROM human_calling_call_legs leg
WHERE command.call_id = leg.call_id
    AND command.call_leg_id IS NULL
    AND command.action <> 'DIAL_DESTINATION'
    AND command.target_id = leg.provider_call_control_id;

UPDATE human_calling_provider_commands command
SET call_leg_id = destination.id,
    peer_call_leg_id = staff.id
FROM human_calling_call_legs destination
JOIN human_calling_call_legs staff
    ON staff.call_id = destination.call_id AND staff.role = 'STAFF'
WHERE command.call_id = destination.call_id
    AND command.action = 'DIAL_DESTINATION'
    AND destination.role = 'DESTINATION'
    AND command.target_id = staff.provider_call_control_id;

UPDATE human_calling_provider_commands command
SET peer_call_leg_id = caller.id
FROM human_calling_call_legs caller
WHERE command.call_id = caller.call_id
    AND command.action = 'BRIDGE'
    AND caller.role = 'CALLER';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM human_calling_provider_commands
        WHERE call_id IS NOT NULL
            AND call_leg_id IS NULL
            AND action NOT IN ('START_RECORDING')
    ) THEN
        RAISE EXCEPTION 'CallLeg cutover found unmapped provider commands';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM human_calling_provider_commands
        WHERE action IN ('BRIDGE', 'DIAL_DESTINATION')
            AND peer_call_leg_id IS NULL
    ) THEN
        RAISE EXCEPTION 'CallLeg cutover found unmapped provider command peers';
    END IF;
END
$$;

UPDATE human_calling_timeline timeline
SET
    opaque_reference = COALESCE(
        timeline.opaque_reference,
        md5(timeline.provider_command_id::text)
    ),
    provider_command_id = NULL
WHERE timeline.provider_command_id IN (
    SELECT id
    FROM human_calling_provider_commands
    WHERE action = 'START_RECORDING'
);
DELETE FROM human_calling_provider_commands WHERE action = 'START_RECORDING';

ALTER TABLE human_calling_provider_commands
    DROP CONSTRAINT human_calling_provider_commands_action_check;

UPDATE human_calling_provider_commands SET action = 'START_RING_WINDOW'
WHERE action = 'START_RINGBACK';
UPDATE human_calling_provider_commands SET action = 'SPEAK_VOICEMAIL'
WHERE action = 'PLAY_VOICEMAIL_GREETING';
UPDATE human_calling_provider_commands SET action = 'HANGUP_LEG'
WHERE action = 'HANGUP';
UPDATE human_calling_provider_commands SET action = 'DIAL_OUTBOUND_DESTINATION'
WHERE action = 'DIAL_DESTINATION';
UPDATE human_calling_provider_commands command
SET action = 'DIAL_OUTBOUND_STAFF'
FROM human_calling_calls call
WHERE command.call_id = call.id
    AND command.action = 'DIAL_STAFF'
    AND call.direction = 'OUTBOUND';

ALTER TABLE human_calling_provider_commands
    ADD CONSTRAINT human_calling_provider_commands_action_check CHECK (
        action IN (
            'ANSWER_CALLER', 'START_RING_WINDOW', 'DIAL_STAFF', 'BRIDGE',
            'STOP_RING_WINDOW', 'HANGUP_LEG', 'SPEAK_VOICEMAIL',
            'START_VOICEMAIL_RECORDING', 'DIAL_OUTBOUND_STAFF',
            'DIAL_OUTBOUND_DESTINATION', 'CREATE_CREDENTIAL',
            'DISABLE_CREDENTIAL', 'CREATE_JWT'
        )
    );

ALTER TABLE human_calling_calls
    ADD COLUMN caller_phone text,
    ADD COLUMN terminal_outcome text,
    ADD COLUMN historical_recording_evidence jsonb;
UPDATE human_calling_calls call
SET
    caller_phone = COALESCE(handoff.phone, call.destination_phone),
    terminal_outcome = CASE
        WHEN call.state IN ('RESOLVED', 'FOLLOW_UP_REQUIRED', 'MISSED', 'VOICEMAIL', 'UNANSWERED')
            THEN call.state
        WHEN call.ended_at IS NOT NULL THEN 'ENDED'
    END
FROM human_calling_handoffs handoff
WHERE handoff.id = call.handoff_id;
UPDATE human_calling_calls call
SET
    caller_phone = call.destination_phone,
    terminal_outcome = CASE
        WHEN call.state IN ('RESOLVED', 'FOLLOW_UP_REQUIRED', 'MISSED', 'VOICEMAIL', 'UNANSWERED')
            THEN call.state
        WHEN call.ended_at IS NOT NULL THEN 'ENDED'
    END
WHERE call.direction = 'OUTBOUND';
UPDATE human_calling_calls call
SET historical_recording_evidence = jsonb_build_object(
    'id', recording.id,
    'providerRecordingId', recording.provider_recording_id,
    'bucket', recording.bucket,
    'objectKey', recording.object_key,
    'state', recording.state,
    'startedAt', recording.started_at,
    'readyAt', recording.ready_at,
    'lastEventAt', recording.last_event_at,
    'failureCode', recording.failure_code,
    'createdAt', recording.created_at,
    'updatedAt', recording.updated_at
)
FROM human_calling_recordings recording
WHERE recording.call_id = call.id;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM human_calling_recordings recording
        LEFT JOIN human_calling_calls call ON call.id = recording.call_id
        WHERE call.historical_recording_evidence IS NULL
    ) THEN
        RAISE EXCEPTION 'CallLeg cutover found unmapped historical recording evidence';
    END IF;
END
$$;
ALTER TABLE human_calling_calls RENAME COLUMN handoff_id TO source_handoff_id;

DROP TABLE human_calling_connection_attempts CASCADE;
ALTER TABLE human_calling_provider_commands DROP COLUMN attempt_id;

ALTER TABLE human_calling_calls
    DROP COLUMN state CASCADE,
    DROP COLUMN offer_deadline CASCADE,
    DROP COLUMN connection_deadline CASCADE,
    DROP COLUMN claimant_subject CASCADE,
    DROP COLUMN winner_subject CASCADE,
    DROP COLUMN claimant_session_id CASCADE,
    DROP COLUMN expected_staff_call_control_id CASCADE,
    DROP COLUMN expected_staff_call_leg_id CASCADE,
    DROP COLUMN current_attempt_id CASCADE,
    DROP COLUMN caller_call_control_id CASCADE,
    DROP COLUMN caller_call_leg_id CASCADE,
    DROP COLUMN call_session_id CASCADE,
    DROP COLUMN destination_call_control_id CASCADE,
    DROP COLUMN destination_call_leg_id CASCADE,
    DROP COLUMN prior_availability_intent CASCADE,
    DROP COLUMN connected_at CASCADE,
    DROP COLUMN voicemail_failure_deadline CASCADE,
    DROP COLUMN voicemail_failure_event_id CASCADE;

ALTER TABLE human_calling_calls
    ADD CONSTRAINT human_calling_calls_terminal_outcome_check CHECK (
        terminal_outcome IS NULL OR terminal_outcome IN (
            'ENDED', 'UNANSWERED', 'VOICEMAIL', 'MISSED',
            'RESOLVED', 'FOLLOW_UP_REQUIRED', 'ROUTING_FAILED', 'ABANDONED'
        )
    ),
    ADD CONSTRAINT human_calling_calls_disposition_deadline_check CHECK (
		(disposition_deadline IS NOT NULL) = (
			(terminal_outcome = 'ENDED' AND disposition_outcome IS NULL) IS TRUE
		)
    );

CREATE INDEX human_calling_pending_dispositions_idx
    ON human_calling_calls (disposition_deadline, id)
    WHERE terminal_outcome = 'ENDED' AND disposition_outcome IS NULL;

DROP TABLE human_calling_recordings;
DROP TABLE human_calling_rejected_provider_legs;

UPDATE human_calling_voicemails
SET audio_state = 'UNAVAILABLE',
    last_error_code = COALESCE(last_error_code, 'VOICEMAIL_UNAVAILABLE')
WHERE outcome = 'MISSED_CALL';

ALTER TABLE human_calling_voicemails
    DROP CONSTRAINT human_calling_voicemails_check,
    DROP COLUMN provider_recording_url,
    DROP COLUMN object_key,
    DROP COLUMN content_type,
    DROP COLUMN byte_size,
    DROP COLUMN copy_attempts,
    DROP COLUMN next_copy_at,
    DROP COLUMN copied_at,
    ADD CONSTRAINT human_calling_voicemails_check CHECK (
        (
            outcome = 'MISSED_CALL'
            AND audio_state = 'UNAVAILABLE'
            AND provider_recording_id IS NULL
            AND recording_started_at IS NULL
            AND recording_ended_at IS NULL
            AND duration_millis IS NULL
        )
        OR (
            outcome = 'VOICEMAIL'
            AND audio_state IN ('READY', 'UNAVAILABLE')
            AND provider_recording_id IS NOT NULL
            AND recording_started_at IS NOT NULL
            AND recording_ended_at IS NOT NULL
            AND duration_millis IS NOT NULL
        )
    );
