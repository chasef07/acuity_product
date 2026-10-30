-- Each Practice's insurance list is a chain of immutable whole-catalog
-- revisions; insurance_catalogs points at the one the middleware serves.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE insurance_catalogs (
    practice_id uuid PRIMARY KEY REFERENCES access_practices(id),
    revision_id uuid
);

CREATE TABLE insurance_revisions (
    id uuid PRIMARY KEY,
    practice_id uuid NOT NULL REFERENCES insurance_catalogs(practice_id),
    catalog jsonb NOT NULL CHECK (jsonb_typeof(catalog) = 'object'),
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    reason text NOT NULL CHECK (btrim(reason) <> '' AND char_length(reason) <= 2000),
    created_by text NOT NULL CHECK (btrim(created_by) <> ''),
    provenance text NOT NULL CHECK (provenance IN ('import', 'portal', 'restore')),
    previous_revision_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (practice_id, id),
    FOREIGN KEY (practice_id, previous_revision_id) REFERENCES insurance_revisions(practice_id, id)
);

ALTER TABLE insurance_catalogs
    ADD FOREIGN KEY (practice_id, revision_id) REFERENCES insurance_revisions(practice_id, id);
