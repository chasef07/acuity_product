-- A Location may play a different voicemail greeting from 6 PM to 8 AM
-- Eastern. NULL keeps the daytime greeting at all hours.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE human_calling_location_voice_numbers
    ADD COLUMN after_hours_voicemail_greeting text CHECK (
        after_hours_voicemail_greeting = btrim(after_hours_voicemail_greeting)
        AND char_length(after_hours_voicemail_greeting) BETWEEN 1 AND 2000
    );
