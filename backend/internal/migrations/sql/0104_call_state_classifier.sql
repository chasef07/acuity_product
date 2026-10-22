SET LOCAL lock_timeout = '1s';

CREATE VIEW human_calling_call_states AS
SELECT call.id AS call_id, CASE
    WHEN call.disposition_outcome IN ('FOLLOW_UP_REQUIRED', 'CREATE_TASK') THEN 'FOLLOW_UP_REQUIRED'
    WHEN call.disposition_outcome IS NOT NULL THEN 'RESOLVED'
    WHEN call.terminal_outcome = 'VOICEMAIL' THEN 'VOICEMAIL'
    WHEN call.terminal_outcome IN ('MISSED', 'ABANDONED') THEN 'MISSED'
    WHEN call.terminal_outcome IN ('RESOLVED', 'FOLLOW_UP_REQUIRED') THEN call.terminal_outcome
    WHEN call.terminal_outcome = 'ENDED' AND EXISTS (
        SELECT 1 FROM human_calling_call_legs leg
        WHERE leg.call_id = call.id AND leg.bridged_at IS NOT NULL
    ) THEN 'NEEDS_DISPOSITION'
    WHEN call.terminal_outcome IS NOT NULL THEN 'UNANSWERED'
    ELSE (
        SELECT CASE
            WHEN voicemail.recording THEN 'VOICEMAIL_RECORDING'
            WHEN voicemail.greeting THEN 'VOICEMAIL_GREETING'
            WHEN staff.state = 'BRIDGED' OR destination.state = 'BRIDGED' THEN 'CONNECTED'
            WHEN staff.state IN ('ANSWERED', 'BRIDGE_PENDING') THEN 'CONNECTING'
            WHEN staff.state IS NOT NULL THEN 'RINGING'
            WHEN call.direction = 'OUTBOUND' THEN 'PREPARING'
            ELSE 'RINGING'
        END
        FROM (
            SELECT bool_or(command.action = 'START_VOICEMAIL_RECORDING') AS recording,
                bool_or(command.action = 'SPEAK_VOICEMAIL') AS greeting
            FROM human_calling_provider_commands command
            WHERE command.call_id = call.id
                AND command.action IN ('SPEAK_VOICEMAIL', 'START_VOICEMAIL_RECORDING')
                AND command.state IN ('PENDING', 'SENDING', 'SENT', 'AMBIGUOUS', 'RECONCILED')
        ) voicemail
        LEFT JOIN LATERAL (
            SELECT leg.state FROM human_calling_call_legs leg
            WHERE leg.call_id = call.id AND leg.role = 'STAFF'
            ORDER BY CASE leg.state
                WHEN 'BRIDGED' THEN 1 WHEN 'BRIDGE_PENDING' THEN 2
                WHEN 'ENDING' THEN 3 WHEN 'RINGING' THEN 4
                WHEN 'DIALING' THEN 5 ELSE 6 END, leg.updated_at DESC
            LIMIT 1
        ) staff ON true
        LEFT JOIN LATERAL (
            SELECT leg.state FROM human_calling_call_legs leg
            WHERE leg.call_id = call.id AND leg.role = 'DESTINATION'
            ORDER BY leg.sequence DESC
            LIMIT 1
        ) destination ON true
    )
END AS state
FROM human_calling_calls call;
