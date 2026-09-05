-- Usage is source evidence, not a priced estimate. Keep pricing in the Go
-- normalizer and project only metadata and quantities, never conversation or
-- arbitrary nested payloads. SQL NULL identifies rows awaiting the backfill;
-- an empty array represents a report without usable usage entries.
SET LOCAL lock_timeout = '1s';
ALTER TABLE ai_interactions ADD COLUMN cost_usage_evidence jsonb;

CREATE FUNCTION ai_cost_usage_entries(value jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT COALESCE(jsonb_agg(projected.fields ORDER BY entry.position), '[]'::jsonb)
    FROM jsonb_array_elements(CASE WHEN jsonb_typeof(value) = 'array' THEN value ELSE '[]'::jsonb END)
        WITH ORDINALITY entry(value, position)
    CROSS JOIN LATERAL (
        SELECT COALESCE(jsonb_object_agg(field.key,
            CASE
                WHEN field.key IN ('type', 'provider', 'model') AND jsonb_typeof(field.value) = 'string' THEN field.value
                WHEN field.key NOT IN ('type', 'provider', 'model') AND jsonb_typeof(field.value) = 'number' THEN field.value
                ELSE 'null'::jsonb
            END), '{}'::jsonb) AS fields
        FROM jsonb_each(CASE WHEN jsonb_typeof(entry.value) = 'object' THEN entry.value ELSE '{}'::jsonb END) field
        WHERE field.key = ANY(ARRAY[
            'type', 'provider', 'model',
            'input_tokens', 'inputTokens', 'input_cached_tokens', 'inputCachedTokens',
            'output_tokens', 'outputTokens',
            'audio_duration', 'audio_duration_ms', 'audioDurationMs',
            'characters_count', 'charactersCount'
        ])
    ) projected
$$;

CREATE FUNCTION ai_interactions_project_cost_usage() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.cost_usage_evidence := ai_cost_usage_entries(NEW.transcript -> 'usage');
    RETURN NEW;
END;
$$;

-- Legacy and current writers both correct the authoritative source. Deriving
-- on every source change replaces prior usage instead of accumulating it.
CREATE TRIGGER ai_interactions_cost_usage
BEFORE INSERT OR UPDATE OF transcript, cost_usage_evidence
ON ai_interactions FOR EACH ROW
EXECUTE FUNCTION ai_interactions_project_cost_usage();
