-- Media JWTs are issued synchronously and never stored. These rows had no reader,
-- and a failed issue left one PENDING forever at the front of the worker's queue index.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

DELETE FROM human_calling_provider_commands WHERE action = 'CREATE_JWT';
