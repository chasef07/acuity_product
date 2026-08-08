CREATE OR REPLACE VIEW access_operational_users AS
SELECT DISTINCT membership.user_subject
FROM access_memberships membership
WHERE membership.revoked_at IS NULL
    AND (
        membership.role = 'STAFF'
        OR NOT EXISTS (
            SELECT 1
            FROM access_platform_operators operator
            WHERE operator.user_subject = membership.user_subject
                OR operator.email = membership.email
        )
    );
