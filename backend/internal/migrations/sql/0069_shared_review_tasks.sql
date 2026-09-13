ALTER TABLE work_tasks ADD COLUMN source_review_key text;
CREATE UNIQUE INDEX work_tasks_source_review_key_idx ON work_tasks(source_review_key) WHERE source_review_key IS NOT NULL;
CREATE UNIQUE INDEX work_tasks_open_message_review_idx ON work_tasks(message_thread_id) WHERE origin='INBOUND_MESSAGE_REVIEW' AND state='OPEN';
-- Staff follow-up and automatic review are separate needs for the same Message.
DROP INDEX work_tasks_message_source_idx;
CREATE UNIQUE INDEX work_tasks_message_source_idx ON work_tasks(source_message_id) WHERE origin='STAFF_MESSAGE_FOLLOW_UP';

ALTER TABLE work_tasks DROP CONSTRAINT work_tasks_origin_check;
ALTER TABLE work_tasks ADD CONSTRAINT work_tasks_origin_check CHECK(origin IN ('HUMAN_CALL_FOLLOW_UP','ABITA_AI','STAFF_MESSAGE_FOLLOW_UP','VOICEMAIL_RECOVERY','MISSED_CALL_RECOVERY','APPOINTMENT_REVIEW','INBOUND_MESSAGE_REVIEW'));
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='work_tasks'::regclass AND conname='work_tasks_origin_source_check';
 ALTER TABLE work_tasks DROP CONSTRAINT work_tasks_origin_source_check;
 EXECUTE 'ALTER TABLE work_tasks ADD CONSTRAINT work_tasks_origin_source_check CHECK (' || substring(definition FROM 7) || $check$
 OR (origin='APPOINTMENT_REVIEW' AND call_id IS NULL AND created_by_kind='SERVICE' AND created_by_email IS NULL AND source_call_id IS NOT NULL AND source_review_key IS NOT NULL AND source_message_id IS NULL AND message_thread_id IS NULL AND ai_idempotency_key IS NULL AND ai_input_fingerprint IS NULL AND recovery_outcome IS NULL)
 OR (origin='INBOUND_MESSAGE_REVIEW' AND call_id IS NULL AND created_by_kind='SERVICE' AND created_by_email IS NULL AND source_call_id IS NULL AND source_review_key IS NULL AND source_message_id IS NOT NULL AND message_thread_id IS NOT NULL AND ai_idempotency_key IS NULL AND ai_input_fingerprint IS NULL AND recovery_outcome IS NULL))$check$;
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='work_task_activities'::regclass AND conname='work_task_activities_kind_check';
 ALTER TABLE work_task_activities DROP CONSTRAINT work_task_activities_kind_check;
 EXECUTE 'ALTER TABLE work_task_activities ADD CONSTRAINT work_task_activities_kind_check CHECK (' || substring(definition FROM 7) || ' OR kind=''SOURCE_UPDATED'')';
 -- The exact-source deduplication index is for original follow-up needs. Review
 -- identity is the durable outcome key or the open Message Thread above.
 SELECT pg_get_indexdef(indexrelid) INTO definition FROM pg_index WHERE indexrelid='work_tasks_one_exact_open_need_idx'::regclass;
 DROP INDEX work_tasks_one_exact_open_need_idx;
 EXECUTE definition || ' AND origin NOT IN (''APPOINTMENT_REVIEW'',''INBOUND_MESSAGE_REVIEW'')';
END $$;
CREATE FUNCTION work_preserve_review_source() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF OLD.source_review_key IS DISTINCT FROM NEW.source_review_key THEN RAISE EXCEPTION 'Task review source is immutable'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER work_preserve_review_source BEFORE UPDATE ON work_tasks FOR EACH ROW EXECUTE FUNCTION work_preserve_review_source();

-- Preserve all outstanding personal attention as shared work, without an age
-- cutoff or rewriting source records. Personal reads never resolve shared work.
WITH seeded AS (
 INSERT INTO work_tasks(practice_id,location_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_at,updated_at,source_call_id,category,source_review_key,source_message)
 SELECT i.practice_id,i.location_id,i.phone,
 CASE i.appointment_action WHEN 'BOOKED' THEN 'Review booked appointment' WHEN 'CANCELLED' THEN 'Review cancelled appointment' ELSE 'Review appointment change' END,
 'OPEN','APPOINTMENT_REVIEW','normal','SERVICE','appointment-review',i.appointment_occurred_at,i.appointment_occurred_at,i.source_call_id,'appointments',
 i.id::text || ':' || to_char(i.appointment_occurred_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS') || CASE WHEN extract(microseconds FROM i.appointment_occurred_at)::bigint % 1000000 = 0 THEN '' ELSE '.' || rtrim(to_char(i.appointment_occurred_at AT TIME ZONE 'UTC','US'),'0') END || 'Z',
 concat_ws(E'\n', CASE WHEN i.appointment_outcome='PARTIAL' THEN 'Review the unfinished appointment change and complete the remaining follow-up. Check which booking or cancellation actions succeeded before making further changes.' WHEN i.appointment_action='CANCELLED' THEN 'Confirm the cancellation is reflected in the appointment system.' ELSE 'Verify insurance and provider for this appointment.' END,
 CASE WHEN COALESCE(i.booking_result->>'appointmentDate','')<>'' THEN 'Date: ' || (i.booking_result->>'appointmentDate') END,
 CASE WHEN COALESCE(i.booking_result->>'appointmentTime','')<>'' THEN 'Time: ' || (i.booking_result->>'appointmentTime') END,
 CASE WHEN COALESCE(i.booking_result->>'providerName','')<>'' THEN 'Provider: ' || (i.booking_result->>'providerName') END)
 FROM ai_interactions i WHERE i.appointment_action IN ('BOOKED','CANCELLED','RESCHEDULED') AND i.appointment_occurred_at IS NOT NULL
 AND EXISTS(SELECT 1 FROM ai_interaction_attention a WHERE a.interaction_id=i.id AND a.reviewed_at IS NULL)
 ON CONFLICT DO NOTHING RETURNING id,created_at
) INSERT INTO work_task_activities(task_id,task_version,kind,actor_kind,actor_subject,occurred_at) SELECT id,1,'TASK_CREATED','SERVICE','appointment-review',created_at FROM seeded;

WITH seeded AS (
 INSERT INTO work_tasks(practice_id,location_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_at,updated_at,source_message_id,message_thread_id)
 SELECT t.practice_id,t.location_id,t.external_phone,'Review inbound text','OPEN','INBOUND_MESSAGE_REVIEW','normal','SERVICE','inbound-message-review',m.created_at,m.created_at,m.id,t.id
 FROM messaging_threads t JOIN LATERAL (
 SELECT id,body,created_at FROM messaging_messages WHERE thread_id=t.id AND direction='INBOUND' AND upper(btrim(COALESCE(body,''))) NOT IN ('STOP','STOPALL','UNSUBSCRIBE','CANCEL','END','QUIT','START','UNSTOP') ORDER BY created_at DESC,id DESC LIMIT 1
 ) m ON true
 WHERE m.created_at >= (SELECT min(u.unread_since) FROM messaging_thread_unreads u WHERE u.thread_id=t.id)
 AND upper(btrim(COALESCE(m.body,''))) NOT IN ('STOP','STOPALL','UNSUBSCRIBE','CANCEL','END','QUIT','START','UNSTOP')
 ON CONFLICT DO NOTHING RETURNING id,created_at
) INSERT INTO work_task_activities(task_id,task_version,kind,actor_kind,actor_subject,occurred_at) SELECT id,1,'TASK_CREATED','SERVICE','inbound-message-review',created_at FROM seeded;
