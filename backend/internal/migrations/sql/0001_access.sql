CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE access_membership_role AS ENUM ('ADMIN', 'STAFF');
CREATE TYPE access_location_scope AS ENUM ('ALL', 'SELECTED');

CREATE TABLE access_practices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provisioning_key text NOT NULL UNIQUE,
    name text NOT NULL,
    workspace_version bigint NOT NULL DEFAULT 1 CHECK (workspace_version > 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE access_locations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    provisioning_key text NOT NULL,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (practice_id, provisioning_key),
    UNIQUE (practice_id, id)
);

CREATE TABLE access_invitations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provisioning_key text NOT NULL,
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    token_hash bytea NOT NULL UNIQUE,
    email text NOT NULL,
    role access_membership_role NOT NULL,
    location_scope access_location_scope NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    accepted_at timestamptz,
    accepted_by_subject text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (practice_id, provisioning_key),
    CHECK (email = lower(email)),
    CHECK (role <> 'ADMIN' OR location_scope = 'ALL'),
    CHECK (
        (accepted_at IS NULL AND accepted_by_subject IS NULL)
        OR (accepted_at IS NOT NULL AND accepted_by_subject IS NOT NULL)
    )
);

CREATE TABLE access_invitation_locations (
    invitation_id uuid NOT NULL REFERENCES access_invitations(id) ON DELETE CASCADE,
    location_id uuid NOT NULL,
    practice_id uuid NOT NULL,
    PRIMARY KEY (invitation_id, location_id),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id)
);

CREATE TABLE access_memberships (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_subject text NOT NULL,
    email text NOT NULL,
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    role access_membership_role NOT NULL,
    location_scope access_location_scope NOT NULL,
    invitation_id uuid NOT NULL UNIQUE REFERENCES access_invitations(id),
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_subject, practice_id),
    UNIQUE (email, practice_id),
    CHECK (email = lower(email)),
    CHECK (role <> 'ADMIN' OR location_scope = 'ALL')
);

CREATE TABLE access_membership_locations (
    membership_id uuid NOT NULL REFERENCES access_memberships(id) ON DELETE CASCADE,
    location_id uuid NOT NULL,
    practice_id uuid NOT NULL,
    PRIMARY KEY (membership_id, location_id),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id)
);

CREATE TABLE access_audit_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_type text NOT NULL CHECK (actor_type IN ('PROVISIONER', 'HUMAN', 'PLATFORM_OPERATOR')),
    actor_subject text NOT NULL,
    practice_id uuid REFERENCES access_practices(id),
    support_session_id uuid,
    action text NOT NULL,
    reason text,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX access_locations_practice_id_idx
    ON access_locations (practice_id, name, id);
CREATE INDEX access_invitations_email_idx
    ON access_invitations (email, expires_at)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
CREATE INDEX access_memberships_subject_idx
    ON access_memberships (user_subject, practice_id)
    WHERE revoked_at IS NULL;
