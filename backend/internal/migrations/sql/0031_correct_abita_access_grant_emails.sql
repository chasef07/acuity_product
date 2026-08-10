DO $migration$
DECLARE
    abita_practice_id uuid;
    correction record;
    access_grant access_grants%ROWTYPE;
    correction_count integer := 0;
BEGIN
    SELECT id
    INTO abita_practice_id
    FROM access_practices
    WHERE provisioning_key = 'abita-eye-group'
    FOR UPDATE;

    IF abita_practice_id IS NULL THEN
        RETURN;
    END IF;

    FOR correction IN
        SELECT *
        FROM (VALUES
            ('ari-nussbaum', 'anussbaum@abitaeye.com', 'ari@abitaeye.com'),
            ('sherry', 'sherry@abitaeye.com', 'lutzoptical@abitaeye.com')
        ) corrections(grant_key, previous_email, email)
    LOOP
        SELECT *
        INTO access_grant
        FROM access_grants
        WHERE practice_id = abita_practice_id
            AND provisioning_key = correction.grant_key
        FOR UPDATE;

        IF NOT FOUND OR access_grant.email = correction.email THEN
            CONTINUE;
        END IF;

        IF access_grant.email <> correction.previous_email
            OR access_grant.claimed_at IS NOT NULL
            OR access_grant.revoked_at IS NOT NULL
            OR EXISTS (
                SELECT 1
                FROM access_memberships
                WHERE access_grant_id = access_grant.id
            )
            OR EXISTS (
                SELECT 1
                FROM access_grants
                WHERE practice_id = abita_practice_id
                    AND email = correction.email
                    AND id <> access_grant.id
            )
        THEN
            RAISE EXCEPTION
                'Abita email correction found incompatible Access Grant state for %',
                correction.grant_key;
        END IF;

        UPDATE access_grants
        SET email = correction.email
        WHERE id = access_grant.id;

        INSERT INTO access_audit_events (
            actor_type,
            actor_subject,
            practice_id,
            action,
            details
        ) VALUES (
            'PROVISIONER',
            'migration:0031_correct_abita_access_grant_emails',
            abita_practice_id,
            'access.grant_email_corrected',
            jsonb_build_object(
                'grantKey', correction.grant_key,
                'previousEmail', correction.previous_email,
                'email', correction.email
            )
        );

        correction_count := correction_count + 1;
    END LOOP;

    IF correction_count > 0 THEN
        UPDATE access_practices
        SET workspace_version = workspace_version + 1
        WHERE id = abita_practice_id;
    END IF;
END
$migration$;
