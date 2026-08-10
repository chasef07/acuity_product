ALTER TABLE access_locations
    ADD COLUMN time_zone text NOT NULL DEFAULT 'UTC' CHECK (
        time_zone = btrim(time_zone)
        AND char_length(time_zone) BETWEEN 1 AND 100
    );

UPDATE access_locations location
SET time_zone = 'America/New_York'
FROM access_practices practice
WHERE practice.id = location.practice_id
    AND practice.provisioning_key = 'abita-eye-group';
