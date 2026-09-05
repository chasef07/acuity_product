-- Attachment metadata owns storage before any write and remains the durable
-- cleanup intent until deletion succeeds. Tokens fence a replaced worker.
SET LOCAL lock_timeout = '1s';
ALTER TABLE messaging_attachments
    ADD COLUMN storage_token uuid,
    ADD COLUMN content_sha256 bytea CHECK (octet_length(content_sha256) = 32);

CREATE INDEX messaging_outbound_attachment_expiry_idx
    ON messaging_attachments (expires_at, id)
    WHERE direction = 'OUTBOUND' AND message_id IS NULL;

-- No cascading FK: cleanup must outlive an expired/replaced attachment row.
-- An unfinished write retains its intent after deletion because a mounted
-- filesystem syscall may complete after its caller's deadline or lease.
CREATE TABLE messaging_attachment_cleanup (
    object_key text PRIMARY KEY,
    attachment_id uuid NOT NULL,
    write_finished boolean NOT NULL DEFAULT false,
    cleanup_after timestamptz NOT NULL,
    cleanup_token uuid
);
CREATE INDEX messaging_attachment_cleanup_due_idx
    ON messaging_attachment_cleanup (cleanup_after, object_key);

-- Overlapping older revisions do not understand storage tokens. They may read
-- completed attachments, but must not claim or finalize a token-owned write.
CREATE FUNCTION messaging_fence_attachment_storage() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.state IN ('PENDING', 'STORED') AND NEW.storage_token IS NOT NULL THEN
        RAISE EXCEPTION 'Attachment storage must finalize its claim';
    END IF;
    IF OLD.storage_token IS NOT NULL
        AND NEW.storage_token IS NOT DISTINCT FROM OLD.storage_token
        AND NEW.copy_started_at IS DISTINCT FROM OLD.copy_started_at THEN
        RAISE EXCEPTION 'Attachment storage claim must be replaced atomically';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER messaging_fence_attachment_storage
BEFORE UPDATE ON messaging_attachments FOR EACH ROW
EXECUTE FUNCTION messaging_fence_attachment_storage();
