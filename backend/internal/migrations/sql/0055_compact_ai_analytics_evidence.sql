-- Keep analytical evidence beside its authoritative source. The trigger also
-- covers overlapping older revisions: source corrections cannot leave stale
-- analytics. This preserves the range-summary and call-page normalizers,
-- without introducing another metric definition. Conversation text, tool arguments/results and domain payloads do
-- not belong in the range-summary read path.
SET LOCAL lock_timeout = '1s';
ALTER TABLE ai_interactions ADD COLUMN analytics_evidence jsonb;

CREATE FUNCTION ai_analytics_fields(value jsonb, fields text[]) RETURNS jsonb
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT COALESCE(jsonb_object_agg(field.key, field.value), '{}'::jsonb)
    FROM jsonb_each(CASE WHEN jsonb_typeof(value) = 'object' THEN value ELSE '{}'::jsonb END) field
    WHERE field.key = ANY(fields)
$$;

CREATE FUNCTION ai_analytics_entries(value jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT COALESCE(jsonb_agg(
        ai_analytics_fields(entry.value, ARRAY[
            'id', 'type', 'name', 'toolName', 'tool_name', 'call_id', 'callId',
            'created_at', 'createdAt', 'status', 'is_error', 'isError',
            'itemId', 'item_id'
        ]) || jsonb_build_object('metrics', ai_analytics_fields(entry.value -> 'metrics', ARRAY[
            'sttMs', 'transcriptionDelayMs', 'transcription_delay_ms',
            'transcriptionDelay', 'transcription_delay',
            'ttftMs', 'llmNodeTtftMs', 'llm_node_ttft_ms', 'llmNodeTtft', 'llm_node_ttft',
            'ttsTtfbMs', 'ttsNodeTtfbMs', 'tts_node_ttfb_ms', 'ttsNodeTtfb', 'tts_node_ttfb',
            'totalLatencyMs', 'e2eLatencyMs', 'e2e_latency_ms', 'e2eLatency', 'e2e_latency'
        ])) ORDER BY entry.position
    ), '[]'::jsonb)
    FROM jsonb_array_elements(CASE WHEN jsonb_typeof(value) = 'array' THEN value ELSE '[]'::jsonb END)
        WITH ORDINALITY entry(value, position)
$$;

CREATE FUNCTION ai_interactions_project_analytics_evidence() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    items jsonb;
    closeout jsonb;
BEGIN
    items := CASE
        WHEN jsonb_typeof(NEW.transcript #> '{chat_history,items}') = 'array' THEN NEW.transcript #> '{chat_history,items}'
        WHEN jsonb_typeof(NEW.transcript #> '{chatHistory,items}') = 'array' THEN NEW.transcript #> '{chatHistory,items}'
        WHEN jsonb_typeof(NEW.transcript -> 'items') = 'array' THEN NEW.transcript -> 'items'
    END;
    closeout := jsonb_build_object(
        'turnMetrics', ai_analytics_entries(NEW.closeout_payload -> 'turnMetrics'),
        'toolExecutions', ai_analytics_entries(NEW.closeout_payload -> 'toolExecutions')
    );
    -- Presence selects native vs historical execution semantics. Counts/actions
    -- do not read domain outcome payloads; those remain on the detail endpoint.
    IF jsonb_typeof(NEW.closeout_payload) = 'object' AND NEW.closeout_payload ? 'domainOutcomes' THEN
        closeout := closeout || '{"domainOutcomes":[]}'::jsonb;
    END IF;
    NEW.analytics_evidence := jsonb_build_object(
        'transcript', jsonb_build_object('items', ai_analytics_entries(items)),
        'closeout', closeout
    );
    RETURN NEW;
END;
$$;

CREATE TRIGGER ai_interactions_analytics_evidence
BEFORE INSERT OR UPDATE OF transcript, closeout_payload, analytics_evidence
ON ai_interactions FOR EACH ROW
EXECUTE FUNCTION ai_interactions_project_analytics_evidence();
