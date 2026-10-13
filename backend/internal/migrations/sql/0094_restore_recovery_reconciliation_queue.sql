SET LOCAL lock_timeout = '1s';

CREATE TABLE IF NOT EXISTS work_recovery_reconciliation_queue (
    practice_id uuid NOT NULL REFERENCES access_practices(id),
    phone text NOT NULL CHECK (phone ~ '^\+[1-9][0-9]{7,14}$'),
    enqueued_at timestamptz NOT NULL,
    PRIMARY KEY (practice_id, phone)
);
