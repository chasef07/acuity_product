-- Preserve GPT Live seconds and delegator cache writes in compact usage evidence.
CREATE OR REPLACE FUNCTION ai_cost_usage_entries(value jsonb) RETURNS jsonb
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
            'output_tokens', 'outputTokens', 'session_duration', 'input_cache_creation_tokens',
            'audio_duration', 'audio_duration_ms', 'audioDurationMs',
            'characters_count', 'charactersCount'
        ])
    ) projected
$$;

