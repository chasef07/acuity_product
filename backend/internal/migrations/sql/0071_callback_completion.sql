ALTER TABLE work_recovery_resolution_checkpoints DROP CONSTRAINT work_recovery_resolution_checkpoints_kind_check;
ALTER TABLE work_recovery_resolution_checkpoints ADD CONSTRAINT work_recovery_resolution_checkpoints_kind_check CHECK (kind IN ('INBOUND_CALL','CALLBACK_ATTEMPT','BOOKING'));
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='work_task_activities'::regclass AND conname='work_task_activities_kind_check';
 ALTER TABLE work_task_activities DROP CONSTRAINT work_task_activities_kind_check;
 EXECUTE 'ALTER TABLE work_task_activities ADD CONSTRAINT work_task_activities_kind_check ' || replace(definition, '''TASK_AUTO_COMPLETED_INBOUND_CALL''::text', '''TASK_AUTO_COMPLETED_INBOUND_CALL''::text, ''TASK_AUTO_COMPLETED_CALLBACK_ATTEMPT''::text');
END $$;
