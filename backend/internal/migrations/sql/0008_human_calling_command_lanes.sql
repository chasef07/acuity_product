CREATE INDEX human_calling_active_call_commands_idx
    ON human_calling_provider_commands (call_id)
    WHERE call_id IS NOT NULL
        AND state IN ('SENDING', 'AMBIGUOUS');
