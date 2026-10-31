package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const analyticsFactsVersion = 5

const analyticsFactsBatchSize = 100

const analyticsStaleFactsLimit = 500

type analyticsFacts struct {
	executions []ToolExecution
	evaluation evaluationReading
	usage      *tokenUsage
	footprint  *ContextFootprint
	jury       juryFacts
}

type analyticsEvidence struct {
	transcript, closeout, evaluation, footprint, usage json.RawMessage
}

func (evidence analyticsEvidence) facts(startedAt time.Time) analyticsFacts {
	return analyticsFacts{
		executions: normalizeToolExecutions(evidence.transcript, evidence.closeout, startedAt),
		evaluation: readEvaluation(evidence.evaluation),
		usage:      callTokenUsage(evidence.usage),
		footprint:  contextFootprint(evidence.footprint),
		jury:       readJuryFacts(evidence.evaluation),
	}
}

func (m *Module) ProjectNextAnalyticsFacts(ctx context.Context) (bool, error) {
	if m.database == nil {
		return false, errUnavailable
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin AI analytics facts projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '1s'`); err != nil {
		return false, fmt.Errorf("bound AI analytics facts projection: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text FROM ai_interactions
		WHERE analytics_facts_version IS NULL OR analytics_facts_version < $1
		ORDER BY started_at DESC
		LIMIT $2
		FOR NO KEY UPDATE SKIP LOCKED
	`, analyticsFactsVersion, analyticsFactsBatchSize)
	if err != nil {
		return false, fmt.Errorf("claim AI analytics facts: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return false, fmt.Errorf("claim AI analytics facts: %w", err)
	}
	if len(ids) == 0 {
		return false, nil
	}
	if err := projectAnalyticsFacts(ctx, tx, ids); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit AI analytics facts: %w", err)
	}
	return true, nil
}

func projectAnalyticsFacts(ctx context.Context, tx pgx.Tx, ids []string) error {
	rows, err := tx.Query(ctx, `
		SELECT
			id::text,
			started_at,
			analytics_evidence -> 'transcript',
			analytics_evidence -> 'closeout',
			closeout_payload -> 'evaluation',
			closeout_payload -> 'contextFootprint',
			cost_usage_evidence
		FROM ai_interactions
		WHERE id = ANY($1::uuid[])
	`, ids)
	if err != nil {
		return fmt.Errorf("read AI analytics evidence: %w", err)
	}
	type source struct {
		id        string
		startedAt time.Time
		evidence  analyticsEvidence
	}
	sources, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (source, error) {
		var value source
		err := row.Scan(&value.id, &value.startedAt, &value.evidence.transcript, &value.evidence.closeout, &value.evidence.evaluation, &value.evidence.footprint, &value.evidence.usage)
		return value, err
	})
	if err != nil {
		return fmt.Errorf("read AI analytics evidence: %w", err)
	}
	for _, source := range sources {
		if err := writeAnalyticsFacts(ctx, tx, source.id, source.evidence.facts(source.startedAt)); err != nil {
			return err
		}
	}
	return nil
}

func writeAnalyticsFacts(ctx context.Context, tx pgx.Tx, id string, facts analyticsFacts) error {
	names := make([]string, 0, len(facts.executions))
	callIDs := make([]string, 0, len(facts.executions))
	statuses := make([]string, 0, len(facts.executions))
	durations := make([]*int, 0, len(facts.executions))
	for _, execution := range facts.executions {
		names = append(names, execution.Name)
		callIDs = append(callIDs, execution.CallID)
		statuses = append(statuses, execution.Status)
		durations = append(durations, execution.DurationMs)
	}
	flagged := make([]string, 0, len(facts.evaluation.Flags))
	for _, flag := range facts.evaluation.Flags {
		flagged = append(flagged, flag.Check)
	}
	var input, cached, output *float64
	if usage := facts.usage; usage != nil {
		input, cached, output = &usage.input, &usage.cached, &usage.output
	}
	var footprint []int
	if value := facts.footprint; value != nil {
		footprint = []int{value.SpeakerPromptTokens, value.ThinkerPromptTokens, value.ToolSchemaTokens, value.ToolCount}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ai_interactions SET
			analytics_tool_names = $2,
			analytics_tool_call_ids = $3,
			analytics_tool_statuses = $4,
			analytics_tool_durations_ms = $5,
			analytics_evaluated = $6,
			analytics_scored_checks = $7,
			analytics_flagged_checks = $8,
			analytics_sentiment = $9,
			analytics_input_tokens = $10,
			analytics_cached_tokens = $11,
			analytics_output_tokens = $12,
			analytics_footprint = $13,
			analytics_jury_models = $14,
			analytics_jury_results = $15,
			analytics_jury_votes = $16,
			analytics_facts_version = $17
		WHERE id = $1
	`, id, names, callIDs, statuses, durations,
		facts.evaluation.Evaluated, append([]string{}, facts.evaluation.Scored...), flagged, facts.evaluation.Sentiment,
		input, cached, output, footprint,
		facts.jury.Models, facts.jury.Results, facts.jury.Votes,
		analyticsFactsVersion,
	); err != nil {
		return fmt.Errorf("write AI analytics facts: %w", err)
	}
	return nil
}

type analyticsFactsRow struct {
	version                                 *int
	toolNames, toolCallIDs, toolStatuses    []string
	toolDurations                           []*int
	evaluated                               bool
	scored, flagged                         []string
	sentiment                               *float64
	inputTokens, cachedTokens, outputTokens *float64
	footprint                               []int
	juryModels, juryResults, juryVotes      []string
	staleEvidence                           analyticsEvidence
}

const analyticsFactsColumns = `
	interaction.analytics_facts_version,
	interaction.analytics_tool_names,
	interaction.analytics_tool_call_ids,
	interaction.analytics_tool_statuses,
	interaction.analytics_tool_durations_ms,
	COALESCE(interaction.analytics_evaluated, false),
	interaction.analytics_scored_checks,
	interaction.analytics_flagged_checks,
	interaction.analytics_sentiment,
	interaction.analytics_input_tokens,
	interaction.analytics_cached_tokens,
	interaction.analytics_output_tokens,
	interaction.analytics_footprint,
	interaction.analytics_jury_models,
	interaction.analytics_jury_results,
	interaction.analytics_jury_votes,
	CASE WHEN interaction.analytics_facts_version IS DISTINCT FROM $6::smallint THEN interaction.analytics_evidence -> 'transcript' END,
	CASE WHEN interaction.analytics_facts_version IS DISTINCT FROM $6::smallint THEN interaction.analytics_evidence -> 'closeout' END,
	CASE WHEN interaction.analytics_facts_version IS DISTINCT FROM $6::smallint THEN interaction.closeout_payload -> 'evaluation' END,
	CASE WHEN interaction.analytics_facts_version IS DISTINCT FROM $6::smallint THEN interaction.closeout_payload -> 'contextFootprint' END,
	CASE WHEN interaction.analytics_facts_version IS DISTINCT FROM $6::smallint THEN interaction.cost_usage_evidence END`

func (row *analyticsFactsRow) targets() []any {
	return []any{
		&row.version, &row.toolNames, &row.toolCallIDs, &row.toolStatuses, &row.toolDurations,
		&row.evaluated, &row.scored, &row.flagged, &row.sentiment,
		&row.inputTokens, &row.cachedTokens, &row.outputTokens, &row.footprint,
		&row.juryModels, &row.juryResults, &row.juryVotes,
		&row.staleEvidence.transcript, &row.staleEvidence.closeout, &row.staleEvidence.evaluation, &row.staleEvidence.footprint, &row.staleEvidence.usage,
	}
}

var errAnalyticsFactsIncomplete = errors.New("operator AI analytics facts are incomplete")

func (row analyticsFactsRow) facts(judgeQuestions int) (analyticsFacts, error) {
	if row.version == nil || *row.version != analyticsFactsVersion {
		return analyticsFacts{}, errAnalyticsFactsIncomplete
	}
	executions := len(row.toolNames)
	if len(row.toolCallIDs) != executions || len(row.toolStatuses) != executions || len(row.toolDurations) != executions ||
		row.footprint != nil && len(row.footprint) != 4 ||
		row.juryModels == nil && (row.juryResults != nil || row.juryVotes != nil) ||
		row.juryModels != nil && (len(row.juryResults) != judgeQuestions || len(row.juryVotes) != judgeQuestions) {
		return analyticsFacts{}, errors.New("operator AI analytics facts are malformed")
	}
	facts := analyticsFacts{
		executions: make([]ToolExecution, 0, executions),
		evaluation: evaluationReading{Evaluated: row.evaluated, Scored: row.scored, Flags: []evaluationFlag{}, Sentiment: row.sentiment},
		jury:       juryFacts{Models: row.juryModels, Results: row.juryResults, Votes: row.juryVotes},
	}
	for i := range executions {
		facts.executions = append(facts.executions, ToolExecution{Name: row.toolNames[i], CallID: row.toolCallIDs[i], Status: row.toolStatuses[i], DurationMs: row.toolDurations[i]})
	}
	for _, check := range row.flagged {
		facts.evaluation.Flags = append(facts.evaluation.Flags, evaluationFlag{Check: check})
	}
	if row.inputTokens != nil && row.cachedTokens != nil && row.outputTokens != nil {
		facts.usage = &tokenUsage{input: *row.inputTokens, cached: *row.cachedTokens, output: *row.outputTokens}
	}
	if row.footprint != nil {
		facts.footprint = &ContextFootprint{SpeakerPromptTokens: row.footprint[0], ThinkerPromptTokens: row.footprint[1], ToolSchemaTokens: row.footprint[2], ToolCount: row.footprint[3]}
	}
	for i, result := range row.juryResults {
		switch result {
		case juryQuestionUnavailable, juryQuestionUnknown, juryQuestionNotApplicable:
		case juryQuestionNoQuorum, juryQuestionQuorumMet, juryQuestionAttemptedUnavailable:
			if len(row.juryVotes[i]) != len(row.juryModels) || strings.Trim(row.juryVotes[i], string([]byte{juryVoteYes, juryVoteNo, juryVoteMissing})) != "" {
				return analyticsFacts{}, errors.New("operator AI analytics facts are malformed")
			}
		default:
			return analyticsFacts{}, errors.New("operator AI analytics facts are malformed")
		}
	}
	return facts, nil
}
