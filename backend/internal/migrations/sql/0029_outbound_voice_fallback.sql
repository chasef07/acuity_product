CREATE TABLE human_calling_outbound_voice_fallbacks (
    practice_id uuid PRIMARY KEY REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id)
);

DO $migration$
DECLARE
    abita_practice_id uuid;
    sweetwater_location_id uuid;
BEGIN
    SELECT id
    INTO abita_practice_id
    FROM access_practices
    WHERE provisioning_key = 'abita-eye-group'
    FOR UPDATE;

    IF abita_practice_id IS NULL THEN
        RETURN;
    END IF;

    SELECT id
    INTO sweetwater_location_id
    FROM access_locations
    WHERE practice_id = abita_practice_id
        AND provisioning_key = 'sweetwater'
    FOR UPDATE;

    IF sweetwater_location_id IS NULL THEN
        RAISE EXCEPTION
            'Abita outbound voice fallback requires the Sweetwater Location';
    END IF;

    INSERT INTO human_calling_outbound_voice_fallbacks (
        practice_id,
        location_id
    ) VALUES (
        abita_practice_id,
        sweetwater_location_id
    );
END
$migration$;
