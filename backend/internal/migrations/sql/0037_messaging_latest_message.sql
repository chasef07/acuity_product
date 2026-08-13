-- Replace per-Thread latest-Message scans with one maintained reference.
ALTER TABLE messaging_threads
    ADD COLUMN latest_message_id uuid;

WITH latest AS (
    SELECT DISTINCT ON (message.thread_id)
        message.thread_id,
        message.id
    FROM messaging_messages message
    ORDER BY message.thread_id, message.created_at DESC, message.id DESC
)
UPDATE messaging_threads thread
SET latest_message_id = latest.id
FROM latest
WHERE latest.thread_id = thread.id;

ALTER TABLE messaging_threads
    ADD CONSTRAINT messaging_threads_latest_message_fkey
    FOREIGN KEY (latest_message_id, id)
    REFERENCES messaging_messages(id, thread_id);

-- Migrations precede backend rollout. Keep the maintained reference correct
-- while old binaries that only insert messaging_messages are still serving.
CREATE FUNCTION public.messaging_advance_thread_latest_message()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
    UPDATE public.messaging_threads thread
    SET latest_message_id = NEW.id
    WHERE thread.id = NEW.thread_id
        AND (
            thread.latest_message_id IS NULL
            OR (NEW.created_at, NEW.id) > (
                SELECT current.created_at, current.id
                FROM public.messaging_messages current
                WHERE current.id = thread.latest_message_id
                    AND current.thread_id = thread.id
            )
        );
    RETURN NULL;
END;
$$;

REVOKE ALL
ON FUNCTION public.messaging_advance_thread_latest_message()
FROM PUBLIC;

CREATE TRIGGER messaging_messages_advance_thread_latest
AFTER INSERT ON messaging_messages
FOR EACH ROW
EXECUTE FUNCTION public.messaging_advance_thread_latest_message();
