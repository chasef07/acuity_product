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
    outcome_completeness smallint NOT NULL DEFAULT 0 CHECK (
        outcome_completeness BETWEEN 0 AND 20
    ),
    summary_payload jsonb,
    closeout_payload jsonb,
    completeness smallint NOT NULL CHECK (completeness BETWEEN 1 AND 3),
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
