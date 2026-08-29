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
		"domainOutcomes":[{"callId":"call-1","toolName":"reschedule_appointment","outcome":"rescheduled","status":"failed","occurredAt":"2026-08-10T09:00:03Z"}]
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

	executions := normalizeToolExecutions(transcript, closeout, startedAt)
	if len(executions) != 1 || executions[0].Status != "ERROR" ||
		executions[0].DomainOutcome != "rescheduled" ||
		executions[0].DomainStatus != "failed" {
		t.Fatalf("tool executions = %#v", executions)
	}
}

func TestNormalizeToolExecutionsConsumesNativeAgentCloseout(t *testing.T) {
	startedAt := time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		transcript string
		closeout   string
		want       []ToolExecution
	}{
		{
			name: "sequential calls and out-of-order results correlate by call ID",
			transcript: `{"chat_history":{"items":[
				{"type":"function_call_output","name":"cancel_appointment","call_id":"cancel-2","output":"Cancelled.","is_error":false,"created_at":1787648405000},
				{"type":"function_call","name":"book_appointment","call_id":"book-1","arguments":"{\"slot\":\"09:30\"}","created_at":1787648401000},
				{"type":"function_call","name":"cancel_appointment","call_id":"cancel-2","arguments":"{\"appointmentId\":\"old-2\"}","created_at":1787648404000},
				{"type":"function_call_output","name":"book_appointment","call_id":"book-1","output":"Booked.","is_error":false,"created_at":1787648402000}
			]}}`,
			closeout: `{"domainOutcomes":[
				{"callId":"cancel-2","toolName":"cancel_appointment","outcome":"cancelled","status":"success","occurredAt":"2026-08-25T09:00:05Z","evidence":{"action":"cancelled"}},
				{"callId":"book-1","toolName":"book_appointment","outcome":"booked","status":"success","occurredAt":"2026-08-25T09:00:02Z","evidence":{"action":"booked"}}
			]}`,
			want: []ToolExecution{
				{CallID: "book-1", Name: "book_appointment", OccurredAt: time.Date(2026, 8, 25, 9, 0, 1, 0, time.UTC), Status: "SUCCESS", DomainOutcome: "booked", DomainStatus: "success"},
				{CallID: "cancel-2", Name: "cancel_appointment", OccurredAt: time.Date(2026, 8, 25, 9, 0, 4, 0, time.UTC), Status: "SUCCESS", DomainOutcome: "cancelled", DomainStatus: "success"},
			},
		},
		{
			name: "domain results remain separate from successful native execution",
			transcript: `{"chat_history":{"items":[
				{"type":"function_call","name":"add_patient","call_id":"failed-1","arguments":"{}","created_at":1787648401000},
				{"type":"function_call_output","name":"add_patient","call_id":"failed-1","output":"Could not create patient.","is_error":false,"created_at":1787648402000},
				{"type":"function_call","name":"reschedule_appointment","call_id":"partial-2","arguments":"{}","created_at":1787648403000},
				{"type":"function_call_output","name":"reschedule_appointment","call_id":"partial-2","output":"New appointment booked; old appointment remains.","is_error":false,"created_at":1787648404000},
				{"type":"function_call","name":"resolve_patient","call_id":"blocked-3","arguments":"{}","created_at":1787648405000},
				{"type":"function_call_output","name":"resolve_patient","call_id":"blocked-3","output":"More identity is required.","is_error":false,"created_at":1787648406000},
				{"type":"function_call","name":"transfer_call","call_id":"ambiguous-4","arguments":"{}","created_at":1787648407000},
				{"type":"function_call_output","name":"transfer_call","call_id":"ambiguous-4","output":"Transfer status uncertain.","is_error":false,"created_at":1787648408000},
				{"type":"function_call","name":"book_appointment","call_id":"replay-5","arguments":"{}","created_at":1787648409000},
				{"type":"function_call_output","name":"book_appointment","call_id":"replay-5","output":"Already booked.","is_error":false,"created_at":1787648410000}
			]}}`,
			closeout: `{"domainOutcomes":[
				{"callId":"replay-5","toolName":"book_appointment","outcome":"booked","status":"success","occurredAt":"2026-08-25T09:00:10Z","evidence":{"replayed":true}},
				{"callId":"ambiguous-4","toolName":"transfer_call","outcome":"transfer_ambiguous","status":"ambiguous","occurredAt":"2026-08-25T09:00:08Z"},
				{"callId":"blocked-3","toolName":"resolve_patient","outcome":"patient_lookup_needs_identity","status":"blocked","occurredAt":"2026-08-25T09:00:06Z"},
				{"callId":"partial-2","toolName":"reschedule_appointment","outcome":"rescheduled","status":"partial","occurredAt":"2026-08-25T09:00:04Z","evidence":{"action":"rescheduled"}},
				{"callId":"failed-1","toolName":"add_patient","outcome":"patient_creation_failed","status":"failed","occurredAt":"2026-08-25T09:00:02Z"}
			]}`,
			want: []ToolExecution{
				{CallID: "failed-1", Name: "add_patient", OccurredAt: time.Date(2026, 8, 25, 9, 0, 1, 0, time.UTC), Status: "SUCCESS", DomainOutcome: "patient_creation_failed", DomainStatus: "failed"},
				{CallID: "partial-2", Name: "reschedule_appointment", OccurredAt: time.Date(2026, 8, 25, 9, 0, 3, 0, time.UTC), Status: "SUCCESS", DomainOutcome: "rescheduled", DomainStatus: "partial"},
				{CallID: "blocked-3", Name: "resolve_patient", OccurredAt: time.Date(2026, 8, 25, 9, 0, 5, 0, time.UTC), Status: "SUCCESS", DomainOutcome: "patient_lookup_needs_identity", DomainStatus: "blocked"},
				{CallID: "ambiguous-4", Name: "transfer_call", OccurredAt: time.Date(2026, 8, 25, 9, 0, 7, 0, time.UTC), Status: "SUCCESS", DomainOutcome: "transfer_ambiguous", DomainStatus: "ambiguous"},
				{CallID: "replay-5", Name: "book_appointment", OccurredAt: time.Date(2026, 8, 25, 9, 0, 9, 0, time.UTC), Status: "SUCCESS", DomainOutcome: "booked", DomainStatus: "success"},
			},
		},
		{
			name: "native platform failures need no semantic receipt or output parsing",
			transcript: `{"chat_history":{"items":[
				{"type":"function_call","name":"book_appointment","call_id":"invalid-1","arguments":"{}","created_at":1787648401000},
				{"type":"function_call_output","name":"book_appointment","call_id":"invalid-1","output":"Invalid arguments: private prose changes freely.","is_error":true,"created_at":1787648402000},
				{"type":"function_call","name":"reticulating_splines","call_id":"unknown-2","arguments":"{}","created_at":1787648403000},
				{"type":"function_call_output","name":"reticulating_splines","call_id":"unknown-2","output":"Unknown tool: private prose changes freely.","is_error":true,"created_at":1787648404000}
			]}}`,
			closeout: `{ "domainOutcomes": [] }`,
			want: []ToolExecution{
				{CallID: "invalid-1", Name: "book_appointment", OccurredAt: time.Date(2026, 8, 25, 9, 0, 1, 0, time.UTC), Status: "ERROR"},
				{CallID: "unknown-2", Name: "reticulating_splines", OccurredAt: time.Date(2026, 8, 25, 9, 0, 3, 0, time.UTC), Status: "ERROR"},
			},
		},
		{
			name: "domain receipt cannot manufacture a completed native execution",
			transcript: `{"chat_history":{"items":[
				{"type":"function_call","name":"create_staff_task","call_id":"task-1","arguments":"{}","created_at":1787648401000}
			]}}`,
			closeout: `{"domainOutcomes":[
				{"callId":"task-1","toolName":"create_staff_task","outcome":"staff_task_created","status":"success","occurredAt":"2026-08-25T09:00:02Z","evidence":{"taskId":"task-63"}}
			]}`,
			want: []ToolExecution{
				{CallID: "task-1", Name: "create_staff_task", OccurredAt: time.Date(2026, 8, 25, 9, 0, 1, 0, time.UTC), Status: "INCOMPLETE", DomainOutcome: "staff_task_created", DomainStatus: "success", TaskID: "task-63"},
			},
		},
		{
			name: "malformed native output without error evidence remains incomplete",
			transcript: `{"chat_history":{"items":[
				{"type":"function_call","name":"resolve_patient","call_id":"malformed-1","arguments":"{}","created_at":1787648401000},
				{"type":"function_call_output","name":"resolve_patient","call_id":"malformed-1","output":"Verified.","created_at":1787648402000}
			]}}`,
			closeout: `{ "domainOutcomes": [] }`,
			want: []ToolExecution{
				{CallID: "malformed-1", Name: "resolve_patient", OccurredAt: time.Date(2026, 8, 25, 9, 0, 1, 0, time.UTC), Status: "INCOMPLETE"},
			},
		},
		{
			name: "Staff Task success without a durable Task ID is not projected",
			transcript: `{"chat_history":{"items":[
				{"type":"function_call","name":"create_staff_task","call_id":"task-missing","arguments":"{}","created_at":1787648401000},
				{"type":"function_call_output","name":"create_staff_task","call_id":"task-missing","output":"Task sent.","is_error":false,"created_at":1787648402000}
			]}}`,
			closeout: `{"domainOutcomes":[
				{"callId":"task-missing","toolName":"create_staff_task","outcome":"staff_task_created","status":"success","occurredAt":"2026-08-25T09:00:02Z"}
			]}`,
			want: []ToolExecution{
				{CallID: "task-missing", Name: "create_staff_task", OccurredAt: time.Date(2026, 8, 25, 9, 0, 1, 0, time.UTC), Status: "SUCCESS"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeToolExecutions(
				json.RawMessage(test.transcript),
				json.RawMessage(test.closeout),
				startedAt,
			)
			if len(got) != len(test.want) {
				t.Fatalf("tool executions = %#v, want %#v", got, test.want)
			}
			for index := range test.want {
				if got[index] != test.want[index] {
					t.Fatalf("tool execution %d = %#v, want %#v", index, got[index], test.want[index])
				}
			}
		})
	}
}

func TestNormalizeToolExecutionsUsesNarrowHistoricalFallback(t *testing.T) {
	startedAt := time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)
	legacy := json.RawMessage(`{"toolExecutions":[{"callId":"legacy-1","createdAt":"2026-08-25T09:00:01Z","outputClass":"patient_verified","status":"success","toolName":"resolve_patient"}]}`)
	got := normalizeToolExecutions(
		json.RawMessage(`{"chat_history":{"items":[{"type":"message","role":"user","content":["Hello"]}]}}`),
		legacy,
		startedAt,
	)
	if len(got) != 1 || got[0].CallID != "legacy-1" || got[0].OutputClass != "patient_verified" {
		t.Fatalf("historical tool fallback = %#v", got)
	}

	native := json.RawMessage(`{"chat_history":{"items":[
		{"type":"function_call","name":"resolve_patient","call_id":"native-1","arguments":"{}","created_at":1787648401000},
		{"type":"function_call_output","name":"resolve_patient","call_id":"native-1","output":"Verified.","is_error":false,"created_at":1787648402000}
	]}}`)
	got = normalizeToolExecutions(native, legacy, startedAt)
	if len(got) != 1 || got[0].CallID != "legacy-1" || got[0].OutputClass != "patient_verified" {
		t.Fatalf("historical native-looking transcript lost legacy evidence = %#v", got)
	}

	current := json.RawMessage(`{"domainOutcomes":[],"toolExecutions":[{"callId":"legacy-1","createdAt":"2026-08-25T09:00:01Z","outputClass":"patient_verified","status":"success","toolName":"resolve_patient"}]}`)
	got = normalizeToolExecutions(native, current, startedAt)
	if len(got) != 1 || got[0].CallID != "native-1" ||
		got[0].OutputClass != "" || got[0].DomainOutcome != "" {
		t.Fatalf("current native evidence did not suppress legacy field = %#v", got)
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
		closeoutPayload: json.RawMessage(`{"turnMetrics":[
			{"itemId":"agent","metrics":{"e2eLatency":1.4}}
		]}`),
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
		closeoutPayload: json.RawMessage(`{"toolExecutions":[{"callId":"transfer-1","toolName":"transfer_call","status":"success"}]}`),
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
