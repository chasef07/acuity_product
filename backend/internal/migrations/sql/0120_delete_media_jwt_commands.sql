-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.human_calling_provider_commands_depends_on_idx')
        AND indisvalid AND indisready
) AND NOT EXISTS (
    SELECT 1 FROM human_calling_provider_commands WHERE action = 'CREATE_JWT'
);

-- acuity:next-statement
SET lock_timeout = '15s';

-- acuity:next-statement
SET statement_timeout = '60s';

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_provider_commands_depends_on_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_provider_commands_depends_on_idx
    ON human_calling_provider_commands (depends_on_command_id)
    WHERE depends_on_command_id IS NOT NULL;

-- acuity:next-statement
-- Media JWTs are issued synchronously and never stored. These rows had no reader,
-- and a failed issue left one PENDING forever at the front of the worker's queue index.
-- Each deleted row checks the self-referencing depends_on_command_id foreign key,
-- which needs the index above to avoid a full scan per row.
DELETE FROM human_calling_provider_commands WHERE action = 'CREATE_JWT';

-- acuity:next-statement
RESET statement_timeout;

-- acuity:next-statement
RESET lock_timeout;
