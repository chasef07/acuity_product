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
