CREATE TABLE access_platform_operators (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL UNIQUE,
    user_subject text UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (email = lower(email))
);

CREATE TABLE access_support_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    platform_operator_id uuid NOT NULL REFERENCES access_platform_operators(id),
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    reason text NOT NULL CHECK (length(trim(reason)) > 0),
    starts_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > starts_at)
);

ALTER TABLE access_audit_events
    ADD CONSTRAINT access_audit_events_support_session_id_fkey
    FOREIGN KEY (support_session_id)
    REFERENCES access_support_sessions(id);

CREATE INDEX access_support_sessions_active_idx
    ON access_support_sessions (platform_operator_id, practice_id, expires_at DESC)
    WHERE revoked_at IS NULL;
