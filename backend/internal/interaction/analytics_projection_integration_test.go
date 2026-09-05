package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestAnalyticsLargeTranscriptQueryBudget(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	var practiceID, locationID string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('analytics-scale','Synthetic Analytics') RETURNING id::text`).Scan(&practiceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practiceID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES('operator@example.test','operator')`); err != nil {
		t.Fatal(err)
	}
	transcript, _ := json.Marshal(map[string]any{"items": []any{
		map[string]any{"type": "message", "content": strings.Repeat("Synthetic transcript content. ", 4500), "metrics": map[string]any{"e2e_latency": 0.4}},
		map[string]any{"type": "function_call", "call_id": "synthetic-tool", "name": "get_availability", "arguments": strings.Repeat("Synthetic arguments. ", 1000)},
		map[string]any{"type": "function_call_output", "call_id": "synthetic-tool", "is_error": false, "output": strings.Repeat("Synthetic output. ", 1000)},
	}})
	const calls = 3000
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload)
 SELECT 'agent',$1,$2,'synthetic-'||n,'+15555550101','+15555550102',$3::timestamptz - n*interval '1 second',$3,'COMPLETED',3,$4,'{"domainOutcomes":[]}' FROM generate_series(1,$5) AS n`, practiceID, locationID, now, transcript, calls); err != nil {
		t.Fatal(err)
	}
	db, err := postgres.NewExecutor(pool, postgres.ExecutorConfig{AcquireTimeout: time.Second, OperationTimeout: 10 * time.Second, StatementTimeout: 5 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	module := New(db, access.New(db, func() time.Time { return now }), func() time.Time { return now })
	command := QueryAnalyticsCommand{Identity: access.Identity{Subject: "operator", Email: "operator@example.test", EmailVerified: true}, PracticeID: practiceID, Range: AnalyticsRange24Hours, Limit: 25}
	started := time.Now()
	page, err := module.QueryAnalytics(ctx, command)
	elapsed := time.Since(started)
	t.Logf("synthetic calls=%d raw transcript bytes/call=%d query=%s", calls, len(transcript), elapsed)
	var projectedBytes int
	if err := pool.QueryRow(ctx, `SELECT octet_length(analytics_evidence::text) FROM ai_interactions WHERE practice_id=$1 LIMIT 1`, practiceID).Scan(&projectedBytes); err != nil {
		t.Fatal(err)
	}
	t.Logf("compact evidence bytes/call=%d; raw/compact ratio=%.1f", projectedBytes, float64(len(transcript))/float64(projectedBytes))
	if projectedBytes >= len(transcript)/100 {
		t.Fatal("range-query evidence still scales with conversation payload size")
	}
	if err != nil {
		t.Fatal(err)
	}
	if page.Summary.TotalCalls != calls || page.Summary.ToolCallCount != calls || page.Summary.P50TotalLatencyMs == nil || *page.Summary.P50TotalLatencyMs != 400 || len(page.Calls) != 25 || page.NextCursor == "" {
		t.Fatalf("incorrect full-query result: summary=%+v calls=%d", page.Summary, len(page.Calls))
	}
	if elapsed > time.Second {
		t.Fatalf("large transcript query exceeded 1s budget: %s", elapsed)
	}
}

// Compare the existing semantic normalizer against the durable compact form,
// including historical formats and missing or malformed evidence. The normalizer
// remains the single owner of metric definitions.
func TestAnalyticsProjectionPreservesEvidenceAndCorrections(t *testing.T) {
	ctx := context.Background()
	pool := testdb.OpenThrough(t, "0054_classify_completed_booking_searches.sql")
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	var practiceID, locationID string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('projection','Projection') RETURNING id::text`).Scan(&practiceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practiceID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ transcript, closeout string }{
		{`{}`, `{}`},
		{`{"items":[null,{}, {"id":"a","metrics":{"sttMs":10,"ttftMs":20,"ttsTtfbMs":30,"totalLatencyMs":40}}]}`, `{}`},
		{`{"chat_history":{"items":[{"id":"a","metrics":{"transcriptionDelayMs":11,"llmNodeTtftMs":21,"ttsNodeTtfbMs":31,"e2eLatencyMs":41}}]},"items":[{"metrics":{"e2eLatencyMs":999}}]}`, `{"turnMetrics":[{"itemId":"a","metrics":{"transcription_delay":0.12}}]}`},
		{`{"chat_history":{"items":null},"chatHistory":{"items":[{"metrics":{"transcription_delay_ms":12,"llm_node_ttft_ms":22,"tts_node_ttfb_ms":32,"e2e_latency_ms":42}}]}}`, `{}`},
		{`{"items":[{"metrics":{"transcriptionDelay":0.013,"llmNodeTtft":0.023,"ttsNodeTtfb":0.033,"e2eLatency":0.043}},{"metrics":{"transcription_delay":0.014,"llm_node_ttft":0.024,"tts_node_ttfb":0.034,"e2e_latency":0.044}}]}`, `{"turnMetrics":[null,{"item_id":"b","metrics":{"e2e_latency":0.9}}]}`},
		{`{"items":[{"id":"a","metrics":{"sttMs":-1,"transcriptionDelayMs":0,"transcription_delay_ms":"19","transcriptionDelay":0.02,"ttftMs":null,"llm_node_ttft":0.2}},{"id":"a","metrics":{"e2eLatency":0.3}}]}`, `{"turnMetrics":[{"itemId":"a","metrics":{"ttsTtfbMs":30}},{"itemId":"a","metrics":{"e2eLatencyMs":500}}]}`},
		{`{"items":[{"type":"FUNCTION_CALL","callId":"two","name":"second","createdAt":1788508802000},{"type":"function_call","call_id":"one","name":"first","created_at":1788508801000},{"type":"function_call_output","call_id":"one","is_error":false},{"type":"function_call_output","callId":"one","isError":true},{"type":"function_call","call_id":"pending","name":"second"},{"type":"function_call","name":"invalid"},{"type":"function_call_output","callId":"two","is_error":"false"}]}`, `{"domainOutcomes":null,"toolExecutions":[{"name":"ignored","id":"ignored","status":"ERROR"}]}`},
		{`{"items":[{"type":"function_call","call_id":"native-looking","name":"ignored"}]}`, `{"toolExecutions":[null,{"toolName":"one","callId":"1","status":"error","createdAt":1788508803000},{"tool_name":"two","call_id":"2","status":"success","created_at":1788508801000},{"name":"three","id":"3","status":"SUCCESS"},{"name":"invalid","id":"4","status":"UNKNOWN"}]}`},
		{`[]`, `[]`},
	}
	ids := make([]string, len(cases))
	for i, c := range cases {
		if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,status,lifecycle_stage,transcript,closeout_payload) VALUES('agent',$1,$2,$3,'+15555550101','+15555550102',$4,'IN_PROGRESS',1,$5,$6) RETURNING id::text`, practiceID, locationID, fmt.Sprint(i), now, []byte(c.transcript), []byte(c.closeout)).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	// The migration is an actual upgrade with pre-existing evidence. Before its
	// backfill completes, reads fail visibly instead of silently losing calls.
	if err := migrations.ApplyThrough(ctx, pool, "0055_compact_ai_analytics_evidence.sql"); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queryAnalyticsSummary(ctx, tx, QueryAnalyticsCommand{PracticeID: practiceID}, []string{locationID}, now.Add(-time.Hour), now); err == nil {
		t.Fatal("incomplete projection was reported as complete analytics")
	}
	_ = tx.Rollback(ctx)
	if err := migrations.ApplyThrough(ctx, pool, "0056_backfill_ai_analytics_evidence.sql"); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		check := func(transcript, closeout string) {
			t.Helper()
			original := analyticsProjection{call: AnalyticsCall{StartedAt: now, Status: CallInProgress}, transcript: json.RawMessage(transcript), closeoutPayload: json.RawMessage(closeout)}
			compact := analyticsProjection{call: original.call}
			if err := pool.QueryRow(ctx, `SELECT analytics_evidence->'transcript',analytics_evidence->'closeout' FROM ai_interactions WHERE id=$1`, ids[i]).Scan(&compact.transcript, &compact.closeoutPayload); err != nil {
				t.Fatal(err)
			}
			projectAnalyticsEvidence(&original)
			projectAnalyticsEvidence(&compact)
			if !reflect.DeepEqual(original.call, compact.call) || !reflect.DeepEqual(original.latencySamples, compact.latencySamples) {
				t.Fatalf("case %d projection mismatch\noriginal=%+v samples=%+v\ncompact=%+v samples=%+v", i, original.call, original.latencySamples, compact.call, compact.latencySamples)
			}
		}
		check(c.transcript, c.closeout)
		// A legacy writer only touches source columns. Corrected evidence must
		// replace, not append to, the previous analytical projection.
		corrected := `{"items":[{"metrics":{"e2e_latency_ms":750}}]}`
		for attempt := 0; attempt < 2; attempt++ {
			if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET transcript=$2,closeout_payload='{}' WHERE id=$1`, ids[i], []byte(corrected)); err != nil {
				t.Fatal(err)
			}
			check(corrected, `{}`)
		}
	}
}

func TestAnalyticsSummaryUsesGlobalSamples(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	var practiceID, locationID string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('global-samples','Global Samples') RETURNING id::text`).Scan(&practiceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practiceID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, raw := range []string{`{"items":[{"metrics":{"e2e_latency_ms":10}}]}`, `{"items":[{"metrics":{"e2e_latency_ms":100}},{"metrics":{"e2e_latency_ms":200}},{"metrics":{"e2e_latency_ms":300}}]}`, `{}`} {
		if _, err := pool.Exec(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,status,lifecycle_stage,transcript) VALUES('agent',$1,$2,$3,'+15555550101','+15555550102',$4,'IN_PROGRESS',1,$5)`, practiceID, locationID, fmt.Sprint(i), now, []byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	summary, err := queryAnalyticsSummary(ctx, tx, QueryAnalyticsCommand{PracticeID: practiceID}, []string{locationID}, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalCalls != 3 || summary.P50TotalLatencyMs == nil || *summary.P50TotalLatencyMs != 150 || summary.P90TotalLatencyMs == nil || *summary.P90TotalLatencyMs != 300 || summary.P99TotalLatencyMs == nil || *summary.P99TotalLatencyMs != 300 || summary.P50SttMs != nil {
		t.Fatalf("global sample/unknown semantics changed: %+v", summary)
	}
}
