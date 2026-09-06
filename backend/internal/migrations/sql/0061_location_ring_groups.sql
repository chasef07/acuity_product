CREATE TABLE human_calling_location_ring_groups (
    practice_id uuid NOT NULL,
    location_id uuid NOT NULL,
    member_emails text[] NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (practice_id, location_id),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id),
    CHECK (cardinality(member_emails) > 0),
    CHECK (array_position(member_emails, NULL) IS NULL)
);

-- Keep workspace access intact; only the reviewed NMB account receives rings.
INSERT INTO human_calling_location_ring_groups (practice_id, location_id, member_emails)
SELECT location.practice_id, location.id, ARRAY[access_grant.email]
FROM access_locations location
JOIN access_practices practice ON practice.id = location.practice_id
JOIN access_grants access_grant ON access_grant.practice_id = practice.id
    AND access_grant.provisioning_key = 'bright-vu-miami'
WHERE practice.provisioning_key = 'abita-eye-group'
    AND location.provisioning_key = 'north-miami-beach-optical';

INSERT INTO access_audit_events (actor_type, actor_subject, practice_id, action, details)
SELECT 'PROVISIONER', 'migration:0061_location_ring_groups', practice_id,
    'calling.ring_group_configured', jsonb_build_object('locationId', location_id)
FROM human_calling_location_ring_groups;
