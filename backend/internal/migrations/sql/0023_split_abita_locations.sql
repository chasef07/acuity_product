DO $migration$
DECLARE
    abita_practice_id uuid;
    south_florida_medical_id uuid;
    south_florida_optical_id uuid;
    hollywood_id uuid;
    sweetwater_optical_id uuid;
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
    INTO south_florida_medical_id
    FROM access_locations
    WHERE practice_id = abita_practice_id
        AND provisioning_key = 'south-florida-medical'
    FOR UPDATE;

    SELECT id
    INTO south_florida_optical_id
    FROM access_locations
    WHERE practice_id = abita_practice_id
        AND provisioning_key = 'south-florida-optical'
    FOR UPDATE;

    IF south_florida_medical_id IS NULL
        AND south_florida_optical_id IS NULL THEN
        RETURN;
    END IF;

    IF south_florida_medical_id IS NULL OR south_florida_optical_id IS NULL THEN
        RAISE EXCEPTION
            'Abita Location split requires the reviewed four-Location topology';
    END IF;

    IF EXISTS (
        SELECT 1 FROM access_invitations
        WHERE practice_id = abita_practice_id
    ) OR EXISTS (
        SELECT 1 FROM access_memberships
        WHERE practice_id = abita_practice_id
    ) THEN
        RAISE EXCEPTION
            'Abita Location split must run before account provisioning';
    END IF;

    IF EXISTS (
        SELECT 1 FROM access_locations
        WHERE practice_id = abita_practice_id
            AND provisioning_key IN (
                'hollywood',
                'sweetwater',
                'sweetwater-optical',
                'north-miami-beach-optical'
            )
    ) THEN
        RAISE EXCEPTION
            'Abita Location split found conflicting replacement Locations';
    END IF;

    IF EXISTS (
        SELECT 1 FROM human_calling_handoffs
        WHERE practice_id = abita_practice_id
            AND location_id = south_florida_medical_id
    ) OR EXISTS (
        SELECT 1 FROM human_calling_calls
        WHERE practice_id = abita_practice_id
            AND location_id = south_florida_medical_id
    ) OR EXISTS (
        SELECT 1 FROM human_calling_voicemails
        WHERE practice_id = abita_practice_id
            AND location_id = south_florida_medical_id
    ) OR EXISTS (
        SELECT 1 FROM messaging_threads
        WHERE practice_id = abita_practice_id
            AND location_id = south_florida_medical_id
    ) OR EXISTS (
        SELECT 1 FROM work_tasks
        WHERE practice_id = abita_practice_id
            AND location_id = south_florida_medical_id
    ) THEN
        RAISE EXCEPTION
            'Abita Location split requires zero combined Hollywood/Sweetwater records';
    END IF;

    UPDATE access_locations
    SET provisioning_key = 'sweetwater', name = 'Sweetwater'
    WHERE practice_id = abita_practice_id
        AND id = south_florida_medical_id;

    UPDATE access_locations
    SET provisioning_key = 'north-miami-beach-optical',
        name = 'North Miami Beach Optical'
    WHERE practice_id = abita_practice_id
        AND id = south_florida_optical_id;

    INSERT INTO access_locations (practice_id, provisioning_key, name)
    VALUES (abita_practice_id, 'hollywood', 'Hollywood')
    RETURNING id INTO hollywood_id;

    INSERT INTO access_locations (practice_id, provisioning_key, name)
    VALUES (abita_practice_id, 'sweetwater-optical', 'Sweetwater Optical')
    RETURNING id INTO sweetwater_optical_id;

    DELETE FROM access_abita_office_locations
    WHERE practice_id = abita_practice_id
        AND office_key IN (
            'hollywood',
            'sweetwater',
            'sweetwater-optical',
            'north-miami-beach-optical'
        );

    INSERT INTO access_abita_office_locations (
        practice_id,
        office_key,
        location_id
    ) VALUES
        (abita_practice_id, 'hollywood', hollywood_id),
        (abita_practice_id, 'sweetwater', south_florida_medical_id),
        (abita_practice_id, 'sweetwater-optical', sweetwater_optical_id),
        (
            abita_practice_id,
            'north-miami-beach-optical',
            south_florida_optical_id
        );

    UPDATE access_practices
    SET workspace_version = workspace_version + 1
    WHERE id = abita_practice_id;

    INSERT INTO access_audit_events (
        actor_type,
        actor_subject,
        practice_id,
        action,
        details
    ) VALUES (
        'PROVISIONER',
        'migration:0023_split_abita_locations',
        abita_practice_id,
        'access.locations_split',
        jsonb_build_object(
            'locations',
            jsonb_build_array(
                'hollywood',
                'sweetwater',
                'sweetwater-optical',
                'north-miami-beach-optical'
            )
        )
    );
END
$migration$;
