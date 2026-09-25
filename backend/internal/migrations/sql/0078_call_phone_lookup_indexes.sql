-- acuity:no-transaction
-- acuity:complete-if-true
SELECT EXISTS (
    SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.human_calling_calls_destination_lookup_idx')
        AND indisvalid AND indisready
) AND EXISTS (SELECT 1 FROM pg_index
    WHERE indexrelid = to_regclass('public.human_calling_handoffs_phone_lookup_idx')
        AND indisvalid AND indisready
);

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_calls_destination_lookup_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_calls_destination_lookup_idx
    ON human_calling_calls (practice_id, destination_phone, location_id, id)
    WHERE destination_phone IS NOT NULL;

-- acuity:next-statement
DROP INDEX CONCURRENTLY IF EXISTS human_calling_handoffs_phone_lookup_idx;

-- acuity:next-statement
CREATE INDEX CONCURRENTLY human_calling_handoffs_phone_lookup_idx
    ON human_calling_handoffs (phone, id) WHERE phone IS NOT NULL;
