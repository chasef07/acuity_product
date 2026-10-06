SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE work_recovery_resolution_checkpoints
    ADD COLUMN actor_subject text CHECK (
        actor_subject IS NULL
        OR (
            kind = 'CALLBACK_ATTEMPT'
            AND actor_subject = btrim(actor_subject)
            AND char_length(actor_subject) BETWEEN 1 AND 255
        )
    );
