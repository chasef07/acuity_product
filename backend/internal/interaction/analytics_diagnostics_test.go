package interaction

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestToolExecutionDurationRequiresRecordedEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name         string
		call, output map[string]any
		want         *int
	}{
		{"seconds", map[string]any{"created_at": 1788508800.0}, map[string]any{"created_at": 1788508801.25}, diagnosticInt(1250)},
		{"milliseconds", map[string]any{"createdAt": 1788508800000.0}, map[string]any{"createdAt": 1788508800700.0}, diagnosticInt(700)},
		{"zero measured", map[string]any{"created_at": "2026-09-04T12:00:00Z"}, map[string]any{"created_at": "2026-09-04T12:00:00Z"}, diagnosticInt(0)},
		{"missing output", map[string]any{"created_at": 1788508800.0}, nil, nil},
		{"missing start", map[string]any{}, map[string]any{"created_at": 1788508801.0}, nil},
		{"missing end", map[string]any{"created_at": 1788508800.0}, map[string]any{}, nil},
		{"reversed", map[string]any{"created_at": 1788508802.0}, map[string]any{"created_at": 1788508801.0}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := toolDuration(tc.call, tc.output)
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("duration=%v want=%v", got, tc.want)
			}
		})
	}
}
func TestDiagnosticsRetainSamplesAndBoundedEvidence(t *testing.T) {
	summary := AnalyticsSummary{}
	for i := 0; i < 12; i++ {
		// Primary E2E observations override transcript E2E; STT falls back per stage.
		p := analyticsProjection{call: AnalyticsCall{ID: fmt.Sprintf("call-%02d", i), StartedAt: time.Date(2026, 9, 4, 12, i, 0, 0, time.UTC)}, transcript: json.RawMessage(`{"items":[{"id":"reply","role":"assistant","metrics":{"e2eLatencyMs":99999,"sttMs":250}},{"type":"function_call","call_id":"tool","name":"get_availability","created_at":1788508800},{"type":"function_call_output","call_id":"tool","created_at":1788508801,"is_error":true},{"type":"function_call","call_id":"pending","name":"get_availability"}]}`), closeoutPayload: json.RawMessage(`{"domainOutcomes":[],"turnMetrics":[{"itemId":"reply","metrics":{"e2eLatencyMs":500}}]}`)}
		projectAnalyticsEvidence(&p)
		summarizeAnalyticsProjection(&summary, p)
	}
	finalizeAnalyticsSummary(&summary)
	e2e := summary.Diagnostics.Stages[0]
	if e2e.SampleCount != 12 || e2e.MeasuredCalls != 12 || *e2e.P95Ms != 500 || *summary.P50TotalLatencyMs != 500 {
		t.Fatalf("E2E=%+v", e2e)
	}
	if e2e.Buckets[2].Count != 12 || len(e2e.Buckets[2].Examples) != 5 || e2e.Buckets[2].Examples[0].InteractionID != "call-11" || e2e.Buckets[2].Examples[0].ItemID != "reply" {
		t.Fatalf("bucket evidence=%+v", e2e.Buckets)
	}
	if summary.Diagnostics.Stages[1].Buckets[1].Count != 12 {
		t.Fatal("STT fallback or lower-inclusive bucket boundary lost")
	}
	if len(e2e.Trend) != 1 || e2e.Trend[0].SampleCount != 12 || *e2e.Trend[0].P95Ms != 500 {
		t.Fatal("trend differs from raw observations")
	}
	tool := summary.Diagnostics.Tools[0]
	if tool.ExecutionCount != 24 || tool.ErrorCount != 12 || tool.IncompleteCount != 12 || tool.SampleCount != 12 || *tool.P95Ms != 1000 || len(tool.Errors) != 5 || tool.Examples[0].CallID != "tool" {
		t.Fatalf("tool=%+v", tool)
	}
}
func TestDiagnosticsMissingTimingIsNotZero(t *testing.T) {
	p := analyticsProjection{call: AnalyticsCall{ID: "missing"}, transcript: json.RawMessage(`{"items":[{"type":"function_call","call_id":"tool","name":"lookup"},{"type":"function_call_output","call_id":"tool","is_error":false}]}`), closeoutPayload: json.RawMessage(`{"domainOutcomes":[]}`)}
	projectAnalyticsEvidence(&p)
	d := newDiagnosticsAccumulator()
	d.add(p)
	result := d.finish()
	if result.Tools[0].SampleCount != 0 || result.Tools[0].P50Ms != nil || len(result.Tools[0].Examples) != 0 {
		t.Fatal("unknown tool timing became a measurement")
	}
	for _, stage := range result.Stages {
		if stage.SampleCount != 0 || stage.P95Ms != nil {
			t.Fatal("unknown stage timing became a measurement")
		}
	}
}

func diagnosticInt(value int) *int { return &value }

func TestDiagnosticsTrendPreservesUnmeasuredDates(t *testing.T) {
	d := newDiagnosticsAccumulator()
	d.from = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	d.through = d.from.Add(48 * time.Hour)
	d.stages["e2e"].days["2026-09-02"] = []float64{500}
	trend := d.finish().Stages[0].Trend
	if len(trend) != 3 || trend[0].SampleCount != 0 || trend[0].P50Ms != nil || trend[2].P95Ms != nil || *trend[1].P95Ms != 500 {
		t.Fatalf("missing dates must remain unknown: %+v", trend)
	}
}
