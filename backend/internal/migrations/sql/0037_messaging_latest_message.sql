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
