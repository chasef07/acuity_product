CREATE TABLE human_calling_rejected_provider_legs (
    call_control_id text NOT NULL,
    call_leg_id text NOT NULL,
    call_session_id text NOT NULL,
    initiated_event_id text NOT NULL UNIQUE
        REFERENCES human_calling_provider_receipts(event_id),
    rejected_at timestamptz NOT NULL,
    PRIMARY KEY (call_control_id, call_leg_id, call_session_id)
);

WITH rejected_receipts AS (
    SELECT
        receipt.event_id,
        COALESCE(receipt.projected_at, receipt.last_attempt_at, receipt.received_at)
            AS rejected_at,
        convert_from(receipt.raw_body, 'UTF8')::jsonb #> '{data,payload}' AS payload
    FROM human_calling_provider_receipts receipt
    WHERE receipt.event_type = 'call.initiated'
        AND receipt.state = 'FAILED'
        AND receipt.projection_error_code = 'HANDOFF_REJECTED'
)
INSERT INTO human_calling_rejected_provider_legs (
    call_control_id,
    call_leg_id,
    call_session_id,
    initiated_event_id,
    rejected_at
)
SELECT
    payload->>'call_control_id',
    payload->>'call_leg_id',
    payload->>'call_session_id',
    event_id,
    rejected_at
FROM rejected_receipts
WHERE COALESCE(payload->>'call_control_id', '') <> ''
    AND COALESCE(payload->>'call_leg_id', '') <> ''
    AND COALESCE(payload->>'call_session_id', '') <> ''
ON CONFLICT DO NOTHING;

WITH retrying_lifecycle_receipts AS (
    SELECT
        receipt.event_id,
        convert_from(receipt.raw_body, 'UTF8')::jsonb #> '{data,payload}' AS payload
    FROM human_calling_provider_receipts receipt
    WHERE receipt.event_type IN ('call.answered', 'call.bridged', 'call.hangup')
        AND receipt.state IN ('PENDING', 'PROCESSING', 'QUARANTINED')
), exact_rejected_matches AS (
    SELECT lifecycle.event_id
    FROM retrying_lifecycle_receipts lifecycle
    JOIN human_calling_rejected_provider_legs rejected
        ON rejected.call_control_id = lifecycle.payload->>'call_control_id'
        AND rejected.call_leg_id = lifecycle.payload->>'call_leg_id'
        AND rejected.call_session_id = lifecycle.payload->>'call_session_id'
)
UPDATE human_calling_provider_receipts receipt
SET
    state = 'FAILED',
    projection_error_code = 'RELATED_HANDOFF_REJECTED',
    processing_started_at = NULL,
    projected_at = COALESCE(receipt.projected_at, now()),
    quarantined_at = NULL
FROM exact_rejected_matches matched
WHERE receipt.event_id = matched.event_id;
