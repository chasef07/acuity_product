-- Two kinds of open review Task appear in no staff folder, so nobody can close
-- them: appointment reviews at Locations without the spring-hill Abita Office
-- Route (reviews are created and listed only with it), and text reviews whose
-- thread has had no inbound text for 120 hours (the Texts folder hides them).
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

WITH retired AS (
    UPDATE work_tasks task
    SET
        state = 'COMPLETED',
        completed_by_kind = 'SERVICE',
        completed_by_subject = 'hidden-review-retirement',
        completed_by_email = NULL,
        -- The last moment the review changed. The migration time would put
        -- every retired review at the top of the staff "Recently completed"
        -- list, which orders by completed_at. The activity details carry the
        -- real retirement time.
        completed_at = task.updated_at,
        version = task.version + 1
    WHERE task.state = 'OPEN'
        AND (
            (
                task.origin = 'APPOINTMENT_REVIEW'
                AND NOT EXISTS (
                    SELECT 1
                    FROM access_abita_office_locations route
                    WHERE route.practice_id = task.practice_id
                        AND route.location_id = task.location_id
                        AND route.office_key = 'spring-hill'
                )
            )
            OR (
                task.origin = 'INBOUND_MESSAGE_REVIEW'
                AND NOT EXISTS (
                    SELECT 1
                    FROM messaging_messages recent_message
                    WHERE recent_message.thread_id = task.message_thread_id
                        AND recent_message.direction = 'INBOUND'
                        AND recent_message.created_at >= CURRENT_TIMESTAMP - INTERVAL '120 hours'
                )
            )
        )
    RETURNING task.id, task.practice_id, task.origin, task.version, task.completed_at
), activities AS (
    INSERT INTO work_task_activities (
        task_id,
        task_version,
        kind,
        actor_kind,
        actor_subject,
        actor_email,
        occurred_at,
        details
    )
    SELECT
        retired.id,
        retired.version,
        'TASK_COMPLETED',
        'SERVICE',
        'hidden-review-retirement',
        NULL,
        retired.completed_at,
        jsonb_build_object(
            'reason', CASE retired.origin
                WHEN 'APPOINTMENT_REVIEW' THEN 'LOCATION_HAS_NO_APPOINTMENT_REVIEW_ROUTE'
                ELSE 'NO_INBOUND_TEXT_FOR_120_HOURS'
            END,
            'retiredAt', now()
        )
    FROM retired
)
UPDATE access_practices practice
SET workspace_version = workspace_version + 1
WHERE practice.id IN (SELECT retired.practice_id FROM retired);
