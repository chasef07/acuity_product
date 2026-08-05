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
