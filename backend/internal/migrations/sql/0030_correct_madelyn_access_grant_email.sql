DO $migration$
DECLARE
    abita_practice_id uuid;
    madelyn_grant_id uuid;
    current_email text;
    grant_claimed_at timestamptz;
    grant_revoked_at timestamptz;
BEGIN
    SELECT id
    INTO abita_practice_id
    FROM access_practices
    WHERE provisioning_key = 'abita-eye-group'
    FOR UPDATE;

    IF abita_practice_id IS NULL THEN
        RETURN;
    END IF;

    SELECT id, email, claimed_at, revoked_at
    INTO madelyn_grant_id, current_email, grant_claimed_at, grant_revoked_at
    FROM access_grants
    WHERE practice_id = abita_practice_id
        AND provisioning_key = 'madelyn'
    FOR UPDATE;

    IF madelyn_grant_id IS NULL OR current_email = 'madelyn@abitaeye.com' THEN
        RETURN;
    END IF;

    IF current_email <> 'madylen@abitaeye.com'
        OR grant_claimed_at IS NOT NULL
        OR grant_revoked_at IS NOT NULL
        OR EXISTS (
            SELECT 1
            FROM access_memberships
            WHERE access_grant_id = madelyn_grant_id
        )
        OR EXISTS (
            SELECT 1
            FROM access_grants
            WHERE practice_id = abita_practice_id
                AND email = 'madelyn@abitaeye.com'
                AND id <> madelyn_grant_id
        )
    THEN
        RAISE EXCEPTION
            'Madelyn email correction found incompatible Access Grant state';
    END IF;

    UPDATE access_grants
    SET email = 'madelyn@abitaeye.com'
    WHERE id = madelyn_grant_id;

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
        'migration:0030_correct_madelyn_access_grant_email',
        abita_practice_id,
        'access.grant_email_corrected',
        jsonb_build_object(
            'grantKey', 'madelyn',
            'previousEmail', current_email,
            'email', 'madelyn@abitaeye.com'
        )
    );
END
$migration$;
