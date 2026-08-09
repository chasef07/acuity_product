DELETE FROM access_memberships membership
USING access_platform_operators operator
WHERE membership.email = operator.email;

DELETE FROM access_grants access_grant
USING access_platform_operators operator
WHERE access_grant.email = operator.email;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM access_memberships membership
        WHERE membership.invitation_id IS NOT NULL
            AND NOT EXISTS (
                SELECT 1
                FROM access_grants access_grant
                WHERE access_grant.practice_id = membership.practice_id
                    AND access_grant.email = membership.email
                    AND access_grant.role = membership.role
                    AND access_grant.location_scope = membership.location_scope
                    AND access_grant.revoked_at IS NULL
                    AND (
                        access_grant.claimed_by_subject IS NULL
                        OR access_grant.claimed_by_subject = membership.user_subject
                    )
                    AND NOT EXISTS (
                        SELECT location_id
                        FROM access_membership_locations
                        WHERE membership_id = membership.id
                        EXCEPT
                        SELECT location_id
                        FROM access_grant_locations
                        WHERE access_grant_id = access_grant.id
                    )
                    AND NOT EXISTS (
                        SELECT location_id
                        FROM access_grant_locations
                        WHERE access_grant_id = access_grant.id
                        EXCEPT
                        SELECT location_id
                        FROM access_membership_locations
                        WHERE membership_id = membership.id
                    )
            )
    ) THEN
        RAISE EXCEPTION 'legacy invitation Membership has no compatible Access Grant';
    END IF;
END
$$;

UPDATE access_grants access_grant
SET claimed_at = COALESCE(access_grant.claimed_at, now()),
    claimed_by_subject = membership.user_subject
FROM access_memberships membership
WHERE membership.invitation_id IS NOT NULL
    AND access_grant.practice_id = membership.practice_id
    AND access_grant.email = membership.email
    AND access_grant.role = membership.role
    AND access_grant.location_scope = membership.location_scope
    AND access_grant.revoked_at IS NULL
    AND (
        access_grant.claimed_by_subject IS NULL
        OR access_grant.claimed_by_subject = membership.user_subject
    )
    AND NOT EXISTS (
        SELECT location_id
        FROM access_membership_locations
        WHERE membership_id = membership.id
        EXCEPT
        SELECT location_id
        FROM access_grant_locations
        WHERE access_grant_id = access_grant.id
    )
    AND NOT EXISTS (
        SELECT location_id
        FROM access_grant_locations
        WHERE access_grant_id = access_grant.id
        EXCEPT
        SELECT location_id
        FROM access_membership_locations
        WHERE membership_id = membership.id
    );

UPDATE access_memberships membership
SET access_grant_id = access_grant.id,
    invitation_id = NULL
FROM access_grants access_grant
WHERE membership.invitation_id IS NOT NULL
    AND access_grant.practice_id = membership.practice_id
    AND access_grant.email = membership.email
    AND access_grant.role = membership.role
    AND access_grant.location_scope = membership.location_scope
    AND access_grant.revoked_at IS NULL
    AND access_grant.claimed_by_subject = membership.user_subject
    AND NOT EXISTS (
        SELECT location_id
        FROM access_membership_locations
        WHERE membership_id = membership.id
        EXCEPT
        SELECT location_id
        FROM access_grant_locations
        WHERE access_grant_id = access_grant.id
    )
    AND NOT EXISTS (
        SELECT location_id
        FROM access_grant_locations
        WHERE access_grant_id = access_grant.id
        EXCEPT
        SELECT location_id
        FROM access_membership_locations
        WHERE membership_id = membership.id
    );

ALTER TABLE access_memberships
    DROP CONSTRAINT access_memberships_origin_check,
    ALTER COLUMN access_grant_id SET NOT NULL,
    DROP COLUMN invitation_id;

DROP TABLE access_invitation_locations;
DROP TABLE access_invitations;

CREATE VIEW access_operational_scopes AS
SELECT
	membership.user_subject,
	membership.practice_id,
	membership.id AS membership_id,
	membership.role,
	membership.location_scope
FROM access_memberships membership
WHERE membership.revoked_at IS NULL
UNION ALL
SELECT
	operator.user_subject,
	practice.id,
	NULL::uuid,
	NULL::access_membership_role,
	'ALL'::access_location_scope
FROM access_platform_operators operator
CROSS JOIN access_practices practice
WHERE operator.user_subject IS NOT NULL;

CREATE OR REPLACE VIEW access_operational_users AS
SELECT DISTINCT operational_scope.user_subject
FROM access_operational_scopes operational_scope;

CREATE VIEW access_calling_scopes AS
SELECT
	operational_scope.user_subject,
	operational_scope.practice_id,
	operational_scope.membership_id,
	operational_scope.location_scope
FROM access_operational_scopes operational_scope
WHERE operational_scope.role = 'STAFF'
	OR operational_scope.role IS NULL;
