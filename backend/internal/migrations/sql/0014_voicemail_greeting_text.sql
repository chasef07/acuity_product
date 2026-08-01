-- Keep voicemail_greeting_url for one rollout so the currently serving revision
-- remains compatible while the new revision is staged and verified.
ALTER TABLE human_calling_location_voice_numbers
    ADD COLUMN voicemail_greeting text NOT NULL
        DEFAULT 'Please leave a message after the beep.'
        CHECK (
            voicemail_greeting = btrim(voicemail_greeting)
            AND char_length(voicemail_greeting) BETWEEN 1 AND 2000
        );
