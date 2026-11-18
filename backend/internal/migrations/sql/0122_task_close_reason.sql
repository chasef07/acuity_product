-- Every closed Task records why it closed, so finished work can be told apart
-- from work that was not needed, a patient staff could not reach, and reviews
-- that expired. closed_via_call_id names the call that closed it, when one did.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE work_tasks
    ADD COLUMN close_reason text,
    ADD COLUMN closed_via_call_id uuid;

-- The reason comes from how the Task last closed. Migration 0121 retires
-- hidden reviews under the hidden-review-retirement actor; keying on that
-- actor is right whether 0121 ran in an earlier release or one statement ago.
-- A Task that was closed as a duplicate, reopened, and closed again by staff
-- takes the reason of its latest close.
UPDATE work_tasks task
SET close_reason = CASE
        WHEN task.completed_by_subject = 'hidden-review-retirement' THEN 'EXPIRED'
        WHEN (
            SELECT activity.kind
            FROM work_task_activities activity
            WHERE activity.task_id = task.id
                AND (
                    activity.kind = 'TASK_COMPLETED'
                    OR activity.kind LIKE 'TASK\_AUTO\_COMPLETED\_%'
                )
            ORDER BY activity.task_version DESC
            LIMIT 1
        ) = 'TASK_AUTO_COMPLETED_DUPLICATE' THEN 'NOT_NEEDED'
        ELSE 'DONE'
    END
WHERE task.state = 'COMPLETED';

-- Only the value is constrained here. The release that deploys this migration
-- can roll traffic back to a backend that does not write the reason, so a
-- completed Task may have none and a reopened Task may keep one until a later
-- migration ties the reason to the state.
ALTER TABLE work_tasks
    ADD CONSTRAINT work_tasks_close_reason_check CHECK (
        close_reason IS NULL
        OR close_reason IN ('DONE', 'NOT_NEEDED', 'UNREACHABLE', 'EXPIRED')
    );

-- Added last so the lock it takes on human_calling_calls is held only for the
-- end of the transaction, not for the backfill.
ALTER TABLE work_tasks
    ADD CONSTRAINT work_tasks_closed_via_call_id_fkey
        FOREIGN KEY (closed_via_call_id) REFERENCES human_calling_calls(id);
