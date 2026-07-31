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
        convert_from(receipt.raw_body, 'UTF8')::jsonb AS body
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
    body #>> '{data,payload,call_control_id}',
    body #>> '{data,payload,call_leg_id}',
    body #>> '{data,payload,call_session_id}',
    event_id,
    rejected_at
FROM rejected_receipts
WHERE COALESCE(body #>> '{data,payload,call_control_id}', '') <> ''
    AND COALESCE(body #>> '{data,payload,call_leg_id}', '') <> ''
    AND COALESCE(body #>> '{data,payload,call_session_id}', '') <> ''
ON CONFLICT DO NOTHING;
