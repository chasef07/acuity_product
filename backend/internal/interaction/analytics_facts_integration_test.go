package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
)

func TestAnalyticsSummaryFromFactsMatchesSummaryFromEvidence(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	pool := db.Owner
	var practiceID, locationID, otherLocationID string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('facts-parity','Facts Parity') RETURNING id::text`).Scan(&practiceID); err != nil {
		t.Fatal(err)
	}
	for i, target := range []*string{&locationID, &otherLocationID} {
		if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,$2,'Synthetic') RETURNING id::text`, practiceID, fmt.Sprint("location-", i)).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

	transcripts := []string{
		`{}`,
		`{"items":[{"type":"function_call","call_id":"a","name":"get_availability","created_at":1789900000},{"type":"function_call_output","call_id":"a","created_at":1789900001.25,"is_error":false},{"type":"function_call","call_id":"b","name":"book_appointment","created_at":"2026-09-20T11:00:00Z"},{"type":"function_call_output","call_id":"b","created_at":"2026-09-20T11:00:02.5Z","is_error":true},{"type":"function_call","call_id":"c","name":"get_availability"}],"usage":[{"type":"llm_usage","input_tokens":12000,"input_cached_tokens":8000,"output_tokens":300},{"type":"llm_usage","inputTokens":100,"inputCachedTokens":10,"outputTokens":5}]}`,
		`{"chat_history":{"items":[{"type":"FUNCTION_CALL","callId":"x","name":"transfer_call","createdAt":1789900100000},{"type":"function_call_output","callId":"x","isError":false,"createdAt":1789900100400}]},"usage":[{"type":"tts_usage","characters_count":20}]}`,
		`{"items":[{"type":"function_call","call_id":"legacy","name":"ignored"}]}`,
		`{"items":[{"type":"function_call","call_id":"far","name":"lookup","created_at":1},{"type":"function_call_output","call_id":"far","created_at":1789900001,"is_error":false}]}`,
		`{"items":[{"type":"function_call","call_id":"y","name":"lookup","created_at":1789900200},{"type":"function_call_output","call_id":"y","created_at":1789900199,"is_error":"no"}],"usage":[{"input_tokens":-1,"input_cached_tokens":0,"output_tokens":1}]}`,
	}
	closeouts := []string{
		`{}`,
		`{"domainOutcomes":[]}`,
		`{"toolExecutions":[{"toolName":"one","callId":"1","status":"error","createdAt":1789900300000},{"tool_name":"two","call_id":"2","status":"success"},{"name":"bad","id":"3","status":"UNKNOWN"}]}`,
		`{"domainOutcomes":[],"contextFootprint":{"speakerPromptTokens":700,"thinkerPromptTokens":3200,"toolSchemaTokens":1800,"toolCount":17}}`,
		`{"domainOutcomes":[],"contextFootprint":{"speakerPromptTokens":3000000000,"thinkerPromptTokens":1,"toolSchemaTokens":2,"toolCount":3}}`,
		`{"domainOutcomes":[],"contextFootprint":{"speakerPromptTokens":900,"thinkerPromptTokens":3000,"toolSchemaTokens":-1,"toolCount":17}}`,
	}
	evaluations := append(paritySampleJuries(), []string{
		`null`,
		`{"evaluatorVersion":"typesafe-scorecard-v8","status":"complete","jurors":["a","a"],"results":{}}`,
		`{"evaluatorVersion":"typesafe-scorecard-v6","status":"complete","jurors":["a","b","c"],"results":{"need_understood":{"verdict":false,"probability":0.1,"votes":{"a":0.1,"b":0.1,"c":0.2}}}}`,
		`{"evaluatorVersion":"typesafe-scorecard-v4","status":"complete","results":{"request_understood":{"answers":{"request_understood":{"type":"noul","noul":0.2}}},"conversation_responsive":{"answers":{"conversation_responsive":{"type":"noul","noul":0.9}}},"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":1.2}}}}}`,
		`{"evaluatorVersion":"typesafe-trace-v4","status":"complete","results":{"outcome":{"answers":{"claims_supported":{"type":"boolean","probability":0.1}}},"reaction":{"answers":{"reports_unresolved":{"type":"boolean","probability":0.9}}}}}`,
	}...)
	statuses := []CallStatus{CallCompleted, CallEscalated, CallInProgress, CallCompleted}
	outcomes := []AppointmentOutcome{OutcomeBooking, OutcomeCancellation, OutcomeReschedule, OutcomeIndeterminate}
	issues := []string{"", "PENDING", "CONFIRMED", "NOT_AN_ISSUE"}
	intents := []string{"", "new_patient_booking", "speak_to_staff"}
	for i := 0; i < 90; i++ {
		started := now.Add(-time.Duration(i) * 7 * time.Hour).Add(-time.Duration(i%3) * time.Minute)
		status := statuses[i%len(statuses)]
		var ended any
		if status != CallInProgress {
			ended = started.Add(time.Duration(i%7+1) * time.Minute)
		}
		location := locationID
		if i%5 == 0 {
			location = otherLocationID
		}
		closeout := decodeRecord(json.RawMessage(closeouts[i%len(closeouts)]))
		closeout["evaluation"] = json.RawMessage(evaluations[i%len(evaluations)])
		closeout["versions"] = map[string]any{"agent": fmt.Sprint("0.", 10+i/30), "prompts": fmt.Sprint("p", i%2), "knowledge": fmt.Sprint("k", i/45)}
		closeoutJSON, _ := json.Marshal(closeout)
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,appointment_outcome,lifecycle_stage,transcript,closeout_payload) VALUES('agent',$1,$2,$3,'+15555550101','+15555550102',$4,$5,$6,$7,3,$8,$9) RETURNING id::text`,
			practiceID, location, fmt.Sprint("parity-", i), started, ended, status, outcomes[i%len(outcomes)], []byte(transcripts[i%len(transcripts)]), closeoutJSON).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if issue := issues[i%len(issues)]; issue != "" {
			if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_issues(interaction_id,reported_by,note,review_outcome,reviewed_by,reviewed_at) VALUES($1,'synthetic-staff','Synthetic report',NULLIF($2,'PENDING'),CASE WHEN $2='PENDING' THEN NULL ELSE 'synthetic-operator' END,CASE WHEN $2='PENDING' THEN NULL ELSE now() END)`, id, issue); err != nil {
				t.Fatal(err)
			}
		}
		if intent := intents[i%len(intents)]; intent != "" {
			if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_classifications(interaction_id,intent,outcome,status) VALUES($1,$2,'resolved','failed') ON CONFLICT(interaction_id) DO UPDATE SET intent=EXCLUDED.intent,outcome=EXCLUDED.outcome,status=EXCLUDED.status`, id, intent); err != nil {
				t.Fatal(err)
			}
		}
	}
	drainAnalyticsFacts(t, New(db.As(testdb.Worker), nil, nil))

	for _, command := range []QueryAnalyticsCommand{
		{PracticeID: practiceID},
		{PracticeID: practiceID, Intent: "speak_to_staff"},
	} {
		for _, days := range []int{1, 7, 30} {
			from := now.Add(-time.Duration(days) * 24 * time.Hour)
			locations := []string{locationID, otherLocationID}
			fromFacts, fromEvidence := paritySummaries(t, pool, command, locations, from, now)
			if fromFacts.TotalCalls == 0 || fromFacts.ToolCallCount == 0 || fromFacts.Judges.ReportedCalls == 0 || fromFacts.Quality.EvaluatedCalls == 0 {
				t.Fatalf("%dd fixture exercised too little: %+v", days, fromFacts)
			}
			if !reflect.DeepEqual(fromFacts, fromEvidence) {
				left, _ := json.MarshalIndent(fromFacts, "", " ")
				right, _ := json.MarshalIndent(fromEvidence, "", " ")
				t.Fatalf("%dd intent=%q summary from facts differs from evidence\nfacts=%s\nevidence=%s", days, command.Intent, left, right)
			}
		}
	}
}

func TestAnalyticsFactsGoStaleWhenTheirSourceChanges(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	pool := db.Owner
	var practiceID, locationID string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('facts-stale','Facts Stale') RETURNING id::text`).Scan(&practiceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practiceID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload) VALUES('agent',$1,$2,'stale','+15555550101','+15555550102',$3,$3,'COMPLETED',3,'{"items":[]}','{"domainOutcomes":[]}') RETURNING id::text`, practiceID, locationID, now.Add(-time.Hour)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	worker := New(db.As(testdb.Worker), nil, nil)
	summarize := func() (AnalyticsSummary, error) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		return queryAnalyticsSummary(ctx, tx, QueryAnalyticsCommand{PracticeID: practiceID}, []string{locationID}, now.Add(-24*time.Hour), now)
	}
	if _, err := summarize(); !errors.Is(err, errAnalyticsFactsIncomplete) {
		t.Fatalf("summary without facts = %v, want incomplete", err)
	}
	drainAnalyticsFacts(t, worker)
	if summary, err := summarize(); err != nil || summary.ToolCallCount != 0 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}

	for _, update := range []string{
		`transcript='{"items":[{"type":"function_call","call_id":"a","name":"lookup"}]}'`,
		`closeout_payload='{"domainOutcomes":[],"evaluation":{"evaluatorVersion":"typesafe-trace-v4"}}'`,
		`closeout_payload='{"domainOutcomes":[],"evaluation":{"evaluatorVersion":"typesafe-trace-v4"},"contextFootprint":{}}'`,
		`started_at=started_at - interval '1 second'`,
		`analytics_facts_version=0`,
	} {
		if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET `+update+` WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := summarize(); !errors.Is(err, errAnalyticsFactsIncomplete) {
			t.Fatalf("after %s summary error = %v, want incomplete", update, err)
		}
		drainAnalyticsFacts(t, worker)
		if _, err := summarize(); err != nil {
			t.Fatalf("after %s and reprojection: %v", update, err)
		}
	}
	if summary, err := summarize(); err != nil || summary.ToolCallCount != 1 {
		t.Fatalf("reprojected facts ignored the corrected transcript: %+v %v", summary, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET summary='unrelated', lifecycle_stage=3 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := summarize(); err != nil {
		t.Fatalf("an update that changes no fact source invalidated facts: %v", err)
	}
}

func paritySampleJuries() []string {
	results := []map[string]any{
		{"verdict": true, "probability": 0.8, "votes": map[string]any{"a": 0.9, "b": 0.7, "c": 0.8}},
		{"verdict": false, "probability": 0.2, "votes": map[string]any{"a": 0.1, "b": 0.9, "c": 0.2}},
		{"status": "unknown", "votes": map[string]any{"a": 0.9}},
		{"status": "not_applicable"},
		{"verdict": true, "probability": 0.6, "votes": map[string]any{"a": 0.9, "b": "bad", "c": 1.5}},
		{"verdict": "yes", "probability": 0.6, "votes": map[string]any{"a": 0.9, "b": 0.1}},
		{"verdict": true, "probability": 0.5, "votes": map[string]any{"a": 0.5, "b": 0.6}},
		{"verdict": true, "probability": 0.7},
		{"status": "other", "votes": map[string]any{"a": 0.9}},
	}
	errorsByResult := []map[string]any{
		{"cause": "no_quorum", "votes": map[string]any{"a": 0.9}},
		{"cause": "no_quorum", "votes": map[string]any{"a": 0.9, "b": 0.1, "c": 0.4}},
		{"cause": "timeout", "votes": map[string]any{"a": 0.9}},
	}
	juries := []string{}
	keys := []string{}
	for _, question := range ScorecardQuestionsForVersion(ScorecardJudgeVersion) {
		if question.Source == "judge" {
			keys = append(keys, question.Key)
		}
	}
	for offset := 0; offset < len(results)+len(errorsByResult); offset++ {
		jury := map[string]any{"evaluatorVersion": ScorecardJudgeVersion, "status": "complete", "jurors": []string{"c", "a", "b"}, "results": map[string]any{}, "errors": map[string]any{}}
		if offset%2 == 1 {
			jury["jurors"] = []string{"b", "a"}
			jury["status"] = "incomplete"
		}
		for i, key := range keys {
			choice := (offset + i) % (len(results) + len(errorsByResult))
			if choice < len(results) {
				jury["results"].(map[string]any)[key] = results[choice]
			} else {
				jury["errors"].(map[string]any)[key] = errorsByResult[choice-len(results)]
			}
		}
		encoded, _ := json.Marshal(jury)
		juries = append(juries, string(encoded))
	}
	return juries
}

func paritySummaries(t *testing.T, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, command QueryAnalyticsCommand, locationIDs []string, from, to time.Time) (AnalyticsSummary, AnalyticsSummary) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fromFacts, err := queryAnalyticsSummary(ctx, tx, command, locationIDs, from, to)
	if err != nil {
		t.Fatal(err)
	}
	fromEvidence, err := evidenceAnalyticsSummary(ctx, tx, command, locationIDs, from, to)
	if err != nil {
		t.Fatal(err)
	}
	return fromFacts, fromEvidence
}

func evidenceAnalyticsSummary(ctx context.Context, tx pgx.Tx, command QueryAnalyticsCommand, locationIDs []string, from, to time.Time) (AnalyticsSummary, error) {
	versions := newVersionAccumulator()
	if err := queryVersionBaseline(ctx, tx, command.PracticeID, locationIDs, from, versions); err != nil {
		return AnalyticsSummary{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT
			interaction.id::text, interaction.started_at, interaction.ended_at, interaction.status, interaction.appointment_outcome,
			interaction.analytics_evidence -> 'transcript', interaction.analytics_evidence -> 'closeout',
			interaction.closeout_payload -> 'evaluation', interaction.closeout_payload -> 'contextFootprint', interaction.cost_usage_evidence,
			CASE WHEN issue.interaction_id IS NULL THEN '' ELSE COALESCE(issue.review_outcome, 'PENDING') END,
			interaction.location_id::text,
			COALESCE(interaction.version_agent, ''), COALESCE(interaction.version_prompts, ''), COALESCE(interaction.version_tools, ''),
			COALESCE(interaction.version_knowledge, ''), COALESCE(interaction.version_judges, ''), COALESCE(interaction.version_evaluator, ''),
			classification.intent, classification.outcome, judged.evaluated, judged.failures
		FROM ai_interactions interaction
		LEFT JOIN ai_interaction_classifications classification ON classification.interaction_id = interaction.id
		LEFT JOIN LATERAL (
			SELECT count(*) > 0 AS evaluated,
				COALESCE(array_agg(DISTINCT answer.question) FILTER (WHERE NOT answer.answer), '{}') AS failures
			FROM ai_interaction_scorecard_answers answer
			WHERE answer.interaction_id = interaction.id AND answer.source = 'judge'
				AND answer.scorecard_version || ':' || answer.question = ANY($5::text[])
		) judged ON true
		LEFT JOIN ai_interaction_issues issue ON issue.interaction_id = interaction.id
		WHERE interaction.practice_id = $1 AND interaction.location_id = ANY($2::uuid[])
			AND interaction.started_at >= $3 AND interaction.started_at <= $4
		ORDER BY interaction.started_at, interaction.id
	`, command.PracticeID, locationIDs, from, to, judgeFailureKeys(""))
	if err != nil {
		return AnalyticsSummary{}, err
	}
	defer rows.Close()
	summary := AnalyticsSummary{Daily: analyticsDays(from, to), diagnostics: newDiagnosticsAccumulator()}
	summary.quality = newQualityAccumulator(summary.Daily)
	summary.versions = versions
	distributions := newDistributionAccumulator()
	distributions.routes = map[string]*AnalyticsPathCount{}
	judges := newJudgeAccumulator()
	dailyDistributions := map[string]*distributionAccumulator{}
	for _, day := range summary.Daily {
		dailyDistributions[day.Date] = newDistributionAccumulator()
	}
	for rows.Next() {
		var projection analyticsProjection
		var evaluation, footprint, usage json.RawMessage
		var intent, outcome *string
		var evaluated bool
		var failures []string
		values := make([]string, len(versionDimensions))
		if err := rows.Scan(&projection.call.ID, &projection.call.StartedAt, &projection.call.EndedAt, &projection.call.Status, &projection.appointmentOutcome,
			&projection.transcript, &projection.closeoutPayload, &evaluation, &footprint, &usage, &projection.quality.staffFlag, &projection.versions.locationID,
			&values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &intent, &outcome, &evaluated, &failures); err != nil {
			return AnalyticsSummary{}, err
		}
		projectAnalyticsEvidence(&projection)
		projection.quality.startedAt = projection.call.StartedAt
		projection.quality.evaluation = readEvaluation(evaluation)
		judges.add(readJuryFacts(evaluation))
		projection.quality.footprint = contextFootprint(footprint)
		projection.quality.usage = callTokenUsage(usage)
		projection.versions.values = map[string]string{}
		for i, dimension := range versionDimensions {
			projection.versions.values[dimension] = values[i]
		}
		if (command.Intent == "" || intent != nil && *intent == command.Intent) &&
			(command.Outcome == "" || outcome != nil && *outcome == command.Outcome) {
			distributions.add(intent, outcome, evaluated, failures)
			dailyDistributions[projection.call.StartedAt.UTC().Format(time.DateOnly)].add(intent, outcome, evaluated, failures)
		}
		summarizeAnalyticsProjection(&summary, projection)
	}
	if err := rows.Err(); err != nil {
		return AnalyticsSummary{}, err
	}
	finalizeAnalyticsSummary(&summary)
	summary.Judges = judges.finish()
	summary.Distributions = AnalyticsDistributions{AnalyticsDistributionCounts: distributions.finish(), Daily: []AnalyticsDistributionDay{}, Paths: distributions.paths()}
	for _, day := range summary.Daily {
		summary.Distributions.Daily = append(summary.Distributions.Daily, AnalyticsDistributionDay{Date: day.Date, AnalyticsDistributionCounts: dailyDistributions[day.Date].finish()})
	}
	return summary, nil
}
