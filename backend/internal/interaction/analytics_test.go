package interaction

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeTimelineOmitsSystemMessagesAndNormalizesLiveKitItems(t *testing.T) {
	startedAt := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	transcript := json.RawMessage(`{
		"chat_history":{"items":[
			{"id":"system-1","type":"message","role":"system","content":["private prompt"],"created_at":1786352400000},
			{"id":"caller-1","type":"message","role":"user","content":["Please reschedule me."],"created_at":1786352401000,"metrics":{"transcription_delay":0.2}},
			{"id":"tool-1","type":"function_call","name":"reschedule_appointment","call_id":"call-1","arguments":"{\"appointmentId\":\"old-1\"}","created_at":1786352402000},
			{"id":"result-1","type":"function_call_output","name":"reschedule_appointment","call_id":"call-1","output":"{\"status\":\"error\",\"reason\":\"conflict\"}","is_error":true,"created_at":1786352403000},
			{"id":"agent-1","type":"message","role":"assistant","content":["I could not move it."],"created_at":1786352404000,"metrics":{"llm_node_ttft":0.4,"tts_node_ttfb":0.1,"e2e_latency":1.2}}
		]}
	}`)
	closeout := json.RawMessage(`{
		"turnMetrics":[{"itemId":"agent-1","metrics":{"e2eLatency":1.4}}],
		"toolExecutions":[{"callId":"call-1","createdAt":"2026-08-10T09:00:02Z","outputClass":"middleware_error","status":"error","toolName":"reschedule_appointment"}]
	}`)

	timeline, samples := normalizeTimeline(transcript, closeout, startedAt)
	if len(timeline) != 4 {
		t.Fatalf("normalized timeline length = %d, want 4: %#v", len(timeline), timeline)
	}
	if timeline[0].Kind != TimelineCallerMessage ||
		timeline[0].Text != "Please reschedule me." ||
		timeline[0].SttMs == nil || *timeline[0].SttMs != 200 {
		t.Fatalf("caller timeline item = %#v", timeline[0])
	}
	if timeline[1].Kind != TimelineToolCall ||
		timeline[1].Name != "reschedule_appointment" ||
		timeline[1].Payload["appointmentId"] != "old-1" {
		t.Fatalf("tool call timeline item = %#v", timeline[1])
	}
	if timeline[2].Kind != TimelineToolResult || timeline[2].Error == "" ||
		timeline[2].Payload["reason"] != "conflict" {
		t.Fatalf("tool result timeline item = %#v", timeline[2])
	}
	if timeline[3].Kind != TimelineAgentMessage ||
		timeline[3].TotalLatencyMs == nil || *timeline[3].TotalLatencyMs != 1400 {
		t.Fatalf("agent timeline item = %#v", timeline[3])
	}
	if got := medianMilliseconds(samples.total); got == nil || *got != 1400 {
		t.Fatalf("total latency p50 = %v, want 1400", got)
	}
	if got := medianMilliseconds(samples.stt); got == nil || *got != 200 {
		t.Fatalf("STT latency p50 = %v, want 200", got)
	}
	if got := medianMilliseconds(samples.ttft); got == nil || *got != 400 {
		t.Fatalf("TTFT latency p50 = %v, want 400", got)
	}
	if got := medianMilliseconds(samples.ttsTtfb); got == nil || *got != 100 {
		t.Fatalf("TTS TTFB latency p50 = %v, want 100", got)
	}
	for _, item := range timeline {
		if item.Text == "private prompt" {
			t.Fatal("normalized timeline exposed system prompt")
		}
	}

	executions := normalizeToolExecutions(closeout, startedAt)
	if len(executions) != 1 || executions[0].Status != "ERROR" ||
		executions[0].OutputClass != "middleware_error" {
		t.Fatalf("tool executions = %#v", executions)
	}
}

func TestProjectAnalyticsCallMergesCloseoutAndTranscriptLatency(t *testing.T) {
	startedAt := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(time.Minute)
	projection := analyticsProjection{
		call: AnalyticsCall{StartedAt: startedAt, EndedAt: &endedAt},
		transcript: json.RawMessage(`{
			"items":[
				{"id":"caller","metrics":{"transcription_delay":0.2}},
				{"id":"agent","metrics":{"llm_node_ttft":0.4,"tts_node_ttfb":0.1,"e2e_latency":1.2}}
			]
		}`),
		turnMetrics: json.RawMessage(`[
			{"itemId":"agent","metrics":{"e2eLatency":1.4}}
		]`),
	}

	projectAnalyticsCall(&projection, endedAt)
	if projection.call.P50SttMs == nil || *projection.call.P50SttMs != 200 ||
		projection.call.P50TtftMs == nil || *projection.call.P50TtftMs != 400 ||
		projection.call.P50TtsTtfbMs == nil || *projection.call.P50TtsTtfbMs != 100 ||
		projection.call.P50TotalLatencyMs == nil || *projection.call.P50TotalLatencyMs != 1400 {
		t.Fatalf("projected latency = %#v", projection.call)
	}
}

func TestProjectAnalyticsCallDerivesTransferOnlyFromEscalatedStatus(t *testing.T) {
	startedAt := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(2 * time.Minute)
	projection := analyticsProjection{
		call: AnalyticsCall{
			StartedAt: startedAt,
			EndedAt:   &endedAt,
			Status:    CallCompleted,
		},
		tools: json.RawMessage(`[{"callId":"transfer-1","toolName":"transfer_call","status":"success"}]`),
	}
	projectAnalyticsCall(&projection, endedAt)
	if projection.call.Transferred {
		t.Fatal("successful transfer_call attempt marked a completed call transferred")
	}
	projection.call.Status = CallEscalated
	projectAnalyticsCall(&projection, endedAt)
	if !projection.call.Transferred {
		t.Fatal("ESCALATED call was not marked transferred")
	}
}

func TestSummarizeAnalyticsProjectionIncludesOutcomesAndPipelineLatency(t *testing.T) {
	summary := AnalyticsSummary{}
	projection := analyticsProjection{
		appointmentOutcome: OutcomeReschedule,
		call: AnalyticsCall{
			Transferred:    true,
			ToolCallCount:  3,
			ToolErrorCount: 1,
		},
		latencySamples: latencyValueSet{
			stt:     []float64{100, 300},
			ttft:    []float64{400, 600},
			ttsTtfb: []float64{200, 400},
			total:   []float64{1000, 1400},
		},
	}

	summarizeAnalyticsProjection(&summary, projection)
	finalizeAnalyticsSummary(&summary)

	if summary.TotalCalls != 1 || summary.RescheduleCount != 1 ||
		summary.BookingCount != 0 || summary.CancellationCount != 0 ||
		summary.TransferCount != 1 || summary.TransferRate != 1 ||
		summary.ToolCallCount != 3 || summary.ToolErrorCount != 1 ||
		summary.ToolFailureRate != 1.0/3.0 {
		t.Fatalf("analytics totals = %#v", summary)
	}
	if summary.P50SttMs == nil || *summary.P50SttMs != 200 ||
		summary.P90SttMs == nil || *summary.P90SttMs != 300 ||
		summary.P99SttMs == nil || *summary.P99SttMs != 300 ||
		summary.P50TtftMs == nil || *summary.P50TtftMs != 500 ||
		summary.P90TtftMs == nil || *summary.P90TtftMs != 600 ||
		summary.P99TtftMs == nil || *summary.P99TtftMs != 600 ||
		summary.P50TtsTtfbMs == nil || *summary.P50TtsTtfbMs != 300 ||
		summary.P90TtsTtfbMs == nil || *summary.P90TtsTtfbMs != 400 ||
		summary.P99TtsTtfbMs == nil || *summary.P99TtsTtfbMs != 400 ||
		summary.P50TotalLatencyMs == nil || *summary.P50TotalLatencyMs != 1200 ||
		summary.P90TotalLatencyMs == nil || *summary.P90TotalLatencyMs != 1400 ||
		summary.P99TotalLatencyMs == nil || *summary.P99TotalLatencyMs != 1400 {
		t.Fatalf("analytics pipeline latency = %#v", summary)
	}
}
