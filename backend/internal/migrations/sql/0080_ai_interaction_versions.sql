-- Store the versions that shaped each AI call so analytics can mark when a new
-- agent, prompt, tool, judge, evaluator, or knowledge revision began serving
-- calls. Older Agent payloads only identify the package and evaluator versions.
SET LOCAL lock_timeout = '1s';
ALTER TABLE ai_interactions
    ADD COLUMN version_agent text,
    ADD COLUMN version_prompts text,
    ADD COLUMN version_tools text,
    ADD COLUMN version_judges text,
    ADD COLUMN version_evaluator text,
    ADD COLUMN version_knowledge text;

CREATE FUNCTION ai_interaction_version(closeout jsonb, dimension text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT nullif(left(btrim(CASE dimension
        WHEN 'agent' THEN COALESCE(closeout -> 'versions' ->> 'agent', closeout ->> 'agentVersion')
        WHEN 'evaluator' THEN CASE WHEN closeout -> 'evaluation' ->> 'status' IN ('complete', 'incomplete')
            THEN closeout -> 'evaluation' ->> 'evaluatorVersion' END
        ELSE closeout -> 'versions' ->> dimension
    END), 100), '')
$$;

CREATE FUNCTION ai_interactions_project_versions() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.version_agent := ai_interaction_version(NEW.closeout_payload, 'agent');
    NEW.version_prompts := ai_interaction_version(NEW.closeout_payload, 'prompts');
    NEW.version_tools := ai_interaction_version(NEW.closeout_payload, 'tools');
    NEW.version_judges := ai_interaction_version(NEW.closeout_payload, 'judges');
    NEW.version_evaluator := ai_interaction_version(NEW.closeout_payload, 'evaluator');
    NEW.version_knowledge := ai_interaction_version(NEW.closeout_payload, 'knowledge');
    RETURN NEW;
END;
$$;

-- Deriving on every source change replaces stale versions rather than keeping them.
CREATE TRIGGER ai_interactions_versions
BEFORE INSERT OR UPDATE OF closeout_payload, version_agent, version_prompts, version_tools,
    version_judges, version_evaluator, version_knowledge
ON ai_interactions FOR EACH ROW
EXECUTE FUNCTION ai_interactions_project_versions();
