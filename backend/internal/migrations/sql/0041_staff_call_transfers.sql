ALTER TABLE human_calling_call_legs
    DROP CONSTRAINT human_calling_call_legs_state_check;

ALTER TABLE human_calling_call_legs
    ADD CONSTRAINT human_calling_call_legs_state_check CHECK (state IN (
        'PENDING', 'DIALING', 'RINGING', 'ANSWERED', 'BRIDGE_PENDING',
        'BRIDGED', 'ENDING', 'ENDED', 'FAILED'
    ));

DROP INDEX human_calling_one_staff_occupancy_idx;
CREATE UNIQUE INDEX human_calling_one_staff_occupancy_idx
    ON human_calling_call_legs (staff_subject)
    WHERE role = 'STAFF' AND (
        state IN ('ANSWERED', 'BRIDGE_PENDING', 'BRIDGED')
        OR (state = 'ENDING' AND answered_at IS NOT NULL)
    );

CREATE VIEW human_calling_current_staff_owners AS
SELECT DISTINCT ON (call_id)
    call_id,
    id AS call_leg_id,
    staff_subject
FROM human_calling_call_legs
WHERE role = 'STAFF' AND bridged_at IS NOT NULL
ORDER BY call_id, (state = 'BRIDGED') DESC, sequence DESC,
    bridged_at DESC, id DESC;

ALTER TABLE human_calling_provider_commands
    DROP CONSTRAINT human_calling_provider_commands_action_check;

ALTER TABLE human_calling_provider_commands
    ADD CONSTRAINT human_calling_provider_commands_action_check CHECK (
        action IN (
            'ANSWER_CALLER', 'START_RING_WINDOW', 'DIAL_STAFF', 'BRIDGE',
            'TRANSFER_STAFF', 'STOP_RING_WINDOW', 'HANGUP_LEG',
            'SPEAK_VOICEMAIL', 'START_VOICEMAIL_RECORDING',
            'DIAL_OUTBOUND_STAFF', 'DIAL_OUTBOUND_DESTINATION',
            'CREATE_CREDENTIAL', 'DISABLE_CREDENTIAL', 'CREATE_JWT'
        )
    );

CREATE TABLE human_calling_staff_transfers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    call_id uuid NOT NULL REFERENCES human_calling_calls(id) ON DELETE CASCADE,
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    source_staff_leg_id uuid NOT NULL REFERENCES human_calling_call_legs(id),
    target_staff_leg_id uuid NOT NULL UNIQUE REFERENCES human_calling_call_legs(id),
    customer_leg_id uuid NOT NULL REFERENCES human_calling_call_legs(id),
    provider_command_id uuid NOT NULL UNIQUE
        REFERENCES human_calling_provider_commands(id)
        DEFERRABLE INITIALLY DEFERRED,
    requested_by_subject text NOT NULL,
    requested_by_session_id text NOT NULL,
    recipient_subject text NOT NULL,
    recipient_session_id text NOT NULL,
    idempotency_key text NOT NULL,
    handoff_note text NOT NULL CHECK (char_length(handoff_note) <= 500),
    state text NOT NULL CHECK (state IN (
        'REQUESTED', 'ACCEPTED', 'COMPLETED', 'DECLINED',
        'EXPIRED', 'CANCELED', 'FAILED'
    )),
    target_answered_at timestamptz,
    bridge_observed_at timestamptz,
    source_ended_at timestamptz,
    completed_at timestamptz,
    failure_code text,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id),
    CHECK (requested_by_subject <> recipient_subject),
    CHECK ((state = 'ACCEPTED') = (target_answered_at IS NOT NULL)
        OR state IN ('COMPLETED', 'DECLINED', 'EXPIRED', 'CANCELED', 'FAILED')),
    CHECK ((state = 'COMPLETED') = (completed_at IS NOT NULL)),
    UNIQUE (requested_by_subject, idempotency_key)
);

CREATE UNIQUE INDEX human_calling_one_active_staff_transfer_idx
    ON human_calling_staff_transfers (call_id)
    WHERE state IN ('REQUESTED', 'ACCEPTED');

CREATE INDEX human_calling_staff_transfer_recipient_idx
    ON human_calling_staff_transfers (recipient_subject, state, expires_at);

CREATE INDEX human_calling_staff_transfer_expiry_idx
    ON human_calling_staff_transfers (expires_at, id)
    WHERE state IN ('REQUESTED', 'ACCEPTED');
