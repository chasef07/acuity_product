-- Patients' phones send MMS photos larger than the 600 KB outbound limit, so
-- inbound copies get their own 5 MiB limit. Failed inbound copies, nearly all
-- over the old limit, go back to the copy queue once.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE messaging_attachments
    DROP CONSTRAINT messaging_attachments_byte_size_check,
    ADD CONSTRAINT messaging_attachments_byte_size_check CHECK (
        byte_size BETWEEN 1 AND CASE direction
            WHEN 'INBOUND' THEN 5242880
            ELSE 614400
        END
    );

UPDATE messaging_attachments
SET state = 'PROCESSING', copy_started_at = NULL, storage_token = NULL, updated_at = now()
WHERE direction = 'INBOUND' AND state = 'UNAVAILABLE';
