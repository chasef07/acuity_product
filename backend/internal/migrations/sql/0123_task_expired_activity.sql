-- The worker closes a text review 120 hours after its last update with close
-- reason EXPIRED and records the close as a TASK_EXPIRED activity.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO STRICT definition FROM pg_constraint WHERE conrelid='work_task_activities'::regclass AND conname='work_task_activities_kind_check';
 ALTER TABLE work_task_activities DROP CONSTRAINT work_task_activities_kind_check;
 EXECUTE 'ALTER TABLE work_task_activities ADD CONSTRAINT work_task_activities_kind_check CHECK (' || substring(definition FROM 7) || ' OR kind=''TASK_EXPIRED'')';
END $$;
