-- Per-call facts the operator AI analytics range summary aggregates, derived
-- once in Go from the compact evidence so range reads decode no call JSON.
-- analytics_facts_version NULL means the facts are missing or stale; the
-- worker re-derives them and the summary refuses to report until it has.
SET LOCAL lock_timeout = '1s';
ALTER TABLE ai_interactions
    ADD COLUMN analytics_facts_version smallint,
    ADD COLUMN analytics_tool_names text[],
    ADD COLUMN analytics_tool_call_ids text[],
    ADD COLUMN analytics_tool_statuses text[],
    ADD COLUMN analytics_tool_durations_ms bigint[],
    ADD COLUMN analytics_evaluated boolean,
    ADD COLUMN analytics_scored_checks text[],
    ADD COLUMN analytics_flagged_checks text[],
    ADD COLUMN analytics_sentiment double precision,
    ADD COLUMN analytics_input_tokens double precision,
    ADD COLUMN analytics_cached_tokens double precision,
    ADD COLUMN analytics_output_tokens double precision,
    ADD COLUMN analytics_footprint bigint[],
    ADD COLUMN analytics_jury_models text[],
    ADD COLUMN analytics_jury_results text[],
    ADD COLUMN analytics_jury_votes text[];

CREATE FUNCTION ai_interactions_mark_analytics_facts_stale() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.analytics_facts_version := NULL;
    RETURN NEW;
END;
$$;

-- Any writer that changes a fact source (including SQL corrections) marks the
-- facts stale instead of leaving them wrong. Named to fire after the evidence
-- and cost-usage projections it compares.
CREATE TRIGGER ai_interactions_stale_analytics_facts
BEFORE UPDATE ON ai_interactions FOR EACH ROW
WHEN (
    OLD.started_at IS DISTINCT FROM NEW.started_at
    OR OLD.analytics_evidence IS DISTINCT FROM NEW.analytics_evidence
    OR OLD.cost_usage_evidence IS DISTINCT FROM NEW.cost_usage_evidence
    OR OLD.closeout_payload -> 'evaluation' IS DISTINCT FROM NEW.closeout_payload -> 'evaluation'
    OR OLD.closeout_payload -> 'contextFootprint' IS DISTINCT FROM NEW.closeout_payload -> 'contextFootprint'
)
EXECUTE FUNCTION ai_interactions_mark_analytics_facts_stale();
