-- Rename the existing demo Location, preserving every foreign-key reference.
-- Keep the legacy office route for old agent jobs and delayed receipt delivery.
DO $migration$
DECLARE
    demo_practice_id uuid;
    demo_location_id uuid;
BEGIN
    SELECT id INTO demo_practice_id
    FROM access_practices
    WHERE provisioning_key = 'acuity-demo'
    FOR UPDATE;

    IF demo_practice_id IS NULL THEN
        RETURN;
    END IF;

    SELECT id INTO demo_location_id
    FROM access_locations
    WHERE practice_id = demo_practice_id
        AND provisioning_key = 'mental-health-demo'
    FOR UPDATE;

    IF demo_location_id IS NULL THEN
        RETURN;
    END IF;

    IF EXISTS (
        SELECT 1 FROM access_locations
        WHERE practice_id = demo_practice_id
            AND provisioning_key = 'new-tampa-demo'
    ) OR EXISTS (
        SELECT 1 FROM access_abita_office_locations
        WHERE practice_id = demo_practice_id
            AND office_key IN ('mental-health-demo', 'new-tampa-demo')
            AND location_id <> demo_location_id
    ) THEN
        RAISE EXCEPTION 'New Tampa rename found a conflicting Location or office route';
    END IF;

    UPDATE access_locations
    SET provisioning_key = 'new-tampa-demo', name = 'New Tampa Eye Institute'
    WHERE id = demo_location_id;

    INSERT INTO access_abita_office_locations (practice_id, office_key, location_id)
    VALUES
        (demo_practice_id, 'new-tampa-demo', demo_location_id),
        (demo_practice_id, 'mental-health-demo', demo_location_id)
    ON CONFLICT (practice_id, office_key) DO NOTHING;

    UPDATE access_practices
    SET workspace_version = workspace_version + 1
    WHERE id = demo_practice_id;

    INSERT INTO access_audit_events (
        actor_type, actor_subject, practice_id, action, details
    ) VALUES (
        'PROVISIONER', 'migration:0062_new_tampa_demo_key', demo_practice_id,
        'access.location_renamed',
        jsonb_build_object(
            'location_id', demo_location_id,
            'previous_key', 'mental-health-demo',
            'key', 'new-tampa-demo'
        )
    );
END
$migration$;
