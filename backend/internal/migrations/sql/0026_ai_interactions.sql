CREATE TABLE ai_interactions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    service_subject text NOT NULL CHECK (
        service_subject = btrim(service_subject)
        AND char_length(service_subject) BETWEEN 1 AND 255
    ),
    practice_id uuid NOT NULL,
    location_id uuid NOT NULL,
    source_call_id text NOT NULL CHECK (
        source_call_id = btrim(source_call_id)
        AND char_length(source_call_id) BETWEEN 1 AND 255
    ),
    phone text NOT NULL CHECK (phone ~ '^\+[1-9][0-9]{7,14}$'),
    office_phone text NOT NULL CHECK (office_phone ~ '^\+[1-9][0-9]{7,14}$'),
    external_patient_id text CHECK (
        external_patient_id IS NULL
        OR (
            external_patient_id = btrim(external_patient_id)
            AND char_length(external_patient_id) BETWEEN 1 AND 255
        )
    ),
    started_at timestamptz NOT NULL,
    ended_at timestamptz,
    status text NOT NULL CHECK (
        status IN ('IN_PROGRESS', 'COMPLETED', 'ESCALATED', 'FAILED')
    ),
    summary text CHECK (
        summary IS NULL
        OR char_length(summary) BETWEEN 1 AND 10000
    ),
    transcript jsonb,
    appointment_action text CHECK (
        appointment_action IS NULL
        OR appointment_action IN ('BOOKED', 'CANCELLED', 'RESCHEDULED')
    ),
    appointment_outcome text NOT NULL DEFAULT 'INDETERMINATE' CHECK (
        appointment_outcome IN (
            'BOOKING',
            'CANCELLATION',
            'RESCHEDULE',
            'PARTIAL',
            'INDETERMINATE'
        )
    ),
    appointment_occurred_at timestamptz,
    old_appointment_id text CHECK (
        old_appointment_id IS NULL
        OR char_length(old_appointment_id) BETWEEN 1 AND 255
    ),
    new_appointment_id text CHECK (
        new_appointment_id IS NULL
        OR char_length(new_appointment_id) BETWEEN 1 AND 255
    ),
    booking_result jsonb,
    cancellation_result jsonb,
    summary_payload jsonb,
    closeout_payload jsonb,
    lifecycle_stage smallint NOT NULL CHECK (lifecycle_stage BETWEEN 1 AND 3),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id),
    UNIQUE (practice_id, source_call_id),
    CHECK (ended_at IS NULL OR ended_at >= started_at)
);

CREATE INDEX ai_interactions_phone_history_idx
    ON ai_interactions (practice_id, phone, started_at DESC, id DESC);

CREATE INDEX ai_interactions_daily_outcome_idx
    ON ai_interactions (
        practice_id,
        started_at,
        appointment_outcome,
        location_id
    )
    WHERE status <> 'IN_PROGRESS';

CREATE TABLE ai_interaction_receipts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    service_subject text NOT NULL CHECK (
        service_subject = btrim(service_subject)
        AND char_length(service_subject) BETWEEN 1 AND 255
    ),
    practice_id uuid NOT NULL,
    location_id uuid NOT NULL,
    source_call_id text NOT NULL CHECK (
        source_call_id = btrim(source_call_id)
        AND char_length(source_call_id) BETWEEN 1 AND 255
    ),
    kind text NOT NULL CHECK (
        kind IN ('START', 'SUMMARY', 'CLOSEOUT', 'OUTCOME_CHECKPOINT')
    ),
    payload_fingerprint bytea NOT NULL CHECK (
        octet_length(payload_fingerprint) = 32
    ),
    payload jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    state text NOT NULL DEFAULT 'PENDING' CHECK (
        state IN ('PENDING', 'PROJECTED', 'QUARANTINED')
    ),
    interaction_id uuid REFERENCES ai_interactions(id),
    projection_error_code text,
    projected_at timestamptz,
    duplicate_count integer NOT NULL DEFAULT 0 CHECK (duplicate_count >= 0),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id),
    UNIQUE (practice_id, source_call_id, payload_fingerprint),
    CHECK (
        (state = 'PENDING' AND interaction_id IS NULL AND
            projection_error_code IS NULL AND projected_at IS NULL)
        OR (state = 'PROJECTED' AND interaction_id IS NOT NULL AND
            projection_error_code IS NULL AND projected_at IS NOT NULL)
        OR (state = 'QUARANTINED' AND interaction_id IS NULL AND
            projection_error_code IS NOT NULL AND projected_at IS NULL)
    )
);

CREATE INDEX ai_interaction_receipts_history_idx
    ON ai_interaction_receipts (practice_id, source_call_id, received_at, id);

CREATE INDEX ai_interaction_pending_receipts_idx
    ON ai_interaction_receipts (received_at, id)
    WHERE state = 'PENDING';
