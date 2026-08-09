DO $migration$
DECLARE
    target_keys CONSTANT text[] := ARRAY[
        'abel-alvarez',
        'ari-nussbaum',
        'denise-rivera',
        'katie-einsohn',
        'sasha-ojinaga'
    ];
    abita_practice_id uuid;
    hollywood_location_id uuid;
    target_count integer;
    grant_locations_added integer;
    membership_locations_added integer;
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
    INTO hollywood_location_id
    FROM access_locations
    WHERE practice_id = abita_practice_id
        AND provisioning_key = 'hollywood'
    FOR UPDATE;

    SELECT count(*)
    INTO target_count
    FROM access_grants
    WHERE practice_id = abita_practice_id
        AND provisioning_key = ANY(target_keys);

    IF target_count = 0 THEN
        RETURN;
    END IF;

    IF hollywood_location_id IS NULL OR target_count <> 5 THEN
        RAISE EXCEPTION
            'Hollywood Access expansion requires the reviewed Location and five Access Grants';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM access_grants access_grant
        LEFT JOIN (VALUES
            ('abel-alvarez', 'abel@abitaeye.com'),
            ('ari-nussbaum', 'anussbaum@abitaeye.com'),
            ('denise-rivera', 'denise@abitaeye.com'),
            ('katie-einsohn', 'mobileoptical@abitaeye.com'),
            ('sasha-ojinaga', 'sashao@abitaeye.com')
        ) reviewed(provisioning_key, email)
            ON reviewed.provisioning_key = access_grant.provisioning_key
            AND reviewed.email = access_grant.email
        WHERE access_grant.practice_id = abita_practice_id
            AND access_grant.provisioning_key = ANY(target_keys)
            AND (
                reviewed.provisioning_key IS NULL
                OR access_grant.role <> 'STAFF'
                OR access_grant.location_scope <> 'SELECTED'
                OR access_grant.revoked_at IS NOT NULL
            )
    ) THEN
        RAISE EXCEPTION
            'Hollywood Access expansion found incompatible Access Grant state';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM access_grants access_grant
        WHERE access_grant.practice_id = abita_practice_id
            AND access_grant.provisioning_key = ANY(target_keys)
            AND access_grant.claimed_at IS NOT NULL
            AND NOT EXISTS (
                SELECT 1
                FROM access_memberships membership
                WHERE membership.access_grant_id = access_grant.id
                    AND membership.revoked_at IS NULL
            )
    ) THEN
        RAISE EXCEPTION
            'Hollywood Access expansion found a claimed Grant without an active Membership';
    END IF;

    INSERT INTO access_grant_locations (
        access_grant_id,
        location_id,
        practice_id
    )
    SELECT access_grant.id, hollywood_location_id, abita_practice_id
    FROM access_grants access_grant
    WHERE access_grant.practice_id = abita_practice_id
        AND access_grant.provisioning_key = ANY(target_keys)
    ON CONFLICT DO NOTHING;
    GET DIAGNOSTICS grant_locations_added = ROW_COUNT;

    INSERT INTO access_membership_locations (
        membership_id,
        location_id,
        practice_id
    )
    SELECT membership.id, hollywood_location_id, abita_practice_id
    FROM access_memberships membership
    JOIN access_grants access_grant ON access_grant.id = membership.access_grant_id
    WHERE access_grant.practice_id = abita_practice_id
        AND access_grant.provisioning_key = ANY(target_keys)
        AND membership.revoked_at IS NULL
    ON CONFLICT DO NOTHING;
    GET DIAGNOSTICS membership_locations_added = ROW_COUNT;

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
        'migration:0028_expand_hollywood_access',
        abita_practice_id,
        'access.grants_scope_expanded',
        jsonb_build_object(
            'locationKey', 'hollywood',
            'grantKeys', to_jsonb(target_keys),
            'grantLocationsAdded', grant_locations_added,
            'membershipLocationsAdded', membership_locations_added
        )
    );
END
$migration$;
