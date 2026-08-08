CREATE TABLE access_grants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provisioning_key text NOT NULL,
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    email text NOT NULL,
    role access_membership_role NOT NULL,
    location_scope access_location_scope NOT NULL,
    revoked_at timestamptz,
    claimed_at timestamptz,
    claimed_by_subject text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (practice_id, provisioning_key),
    UNIQUE (practice_id, email),
    CHECK (email = lower(email)),
    CHECK (role <> 'ADMIN' OR location_scope = 'ALL'),
    CHECK (
        (claimed_at IS NULL AND claimed_by_subject IS NULL)
        OR (claimed_at IS NOT NULL AND claimed_by_subject IS NOT NULL)
    )
);

CREATE TABLE access_grant_locations (
    access_grant_id uuid NOT NULL REFERENCES access_grants(id) ON DELETE CASCADE,
    location_id uuid NOT NULL,
    practice_id uuid NOT NULL,
    PRIMARY KEY (access_grant_id, location_id),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id)
);

ALTER TABLE access_memberships
    ADD COLUMN access_grant_id uuid UNIQUE REFERENCES access_grants(id),
    ALTER COLUMN invitation_id DROP NOT NULL,
    ADD CONSTRAINT access_memberships_origin_check
        CHECK (num_nonnulls(invitation_id, access_grant_id) = 1);

CREATE INDEX access_grants_email_idx
    ON access_grants (email)
    WHERE claimed_at IS NULL AND revoked_at IS NULL;
