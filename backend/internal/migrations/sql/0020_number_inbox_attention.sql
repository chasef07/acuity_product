-- Text attention is a durable, explicit acknowledgement. Read cursors remain
-- user-specific navigation state and never resolve operational work.
CREATE TABLE messaging_thread_handled (
    thread_id uuid PRIMARY KEY REFERENCES messaging_threads(id) ON DELETE CASCADE,
    practice_id uuid NOT NULL,
    location_id uuid NOT NULL,
    handled_through timestamptz NOT NULL,
    evidence_message_id uuid NOT NULL REFERENCES messaging_messages(id),
    handled_by_subject text NOT NULL,
    handled_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (thread_id, practice_id, location_id)
        REFERENCES messaging_threads(id, practice_id, location_id)
        ON DELETE CASCADE
);

CREATE INDEX messaging_thread_handled_scope_idx
    ON messaging_thread_handled (practice_id, location_id, handled_through);

CREATE TABLE work_staff_notes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    phone text NOT NULL CHECK (phone ~ '^\+[1-9][0-9]{7,14}$'),
    body text NOT NULL CHECK (
        body = btrim(body) AND char_length(body) BETWEEN 1 AND 2500
    ),
    created_by_subject text NOT NULL,
    created_by_email text NOT NULL CHECK (created_by_email = lower(created_by_email)),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id)
);

CREATE INDEX work_staff_notes_phone_timeline_idx
    ON work_staff_notes (practice_id, phone, created_at, id);
