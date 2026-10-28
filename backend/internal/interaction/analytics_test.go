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

func TestAnalyticsDailyBucketsFollowEasternDays(t *testing.T) {
	aug8At10pmEastern := time.Date(2026, time.August, 9, 2, 0, 0, 0, time.UTC)
	aug9At930pmEastern := time.Date(2026, time.August, 10, 1, 30, 0, 0, time.UTC)
	summary := AnalyticsSummary{Daily: analyticsDays(aug8At10pmEastern, aug8At10pmEastern.Add(24*time.Hour))}
	summarizeAnalyticsProjection(&summary, analyticsProjection{
		call:    AnalyticsCall{ID: "evening", StartedAt: aug9At930pmEastern},
		quality: qualitySample{startedAt: aug9At930pmEastern},
	})
	finalizeAnalyticsSummary(&summary)
	if len(summary.Daily) != 2 || summary.Daily[0].Date != "2026-08-08" || summary.Daily[1].Date != "2026-08-09" {
		t.Fatalf("daily dates = %+v, want Aug 8 and Aug 9 Eastern", summary.Daily)
	}
	if summary.Daily[1].TotalCalls != 1 || summary.Quality.Daily[1].UnevaluatedCalls != 1 {
		t.Fatalf("9:30pm Eastern call must count on Aug 9: daily=%+v quality=%+v", summary.Daily, summary.Quality.Daily)
	}
}

func TestAnalyticsDailyCountsBookedCallsByPatientIntent(t *testing.T) {
	day := time.Date(2026, time.August, 10, 14, 0, 0, 0, time.UTC)
	summary := AnalyticsSummary{Daily: analyticsDays(day, day)}
	for _, call := range []struct {
		intent  CallIntent
		outcome AppointmentOutcome
	}{
		{"new_patient_booking", OutcomeBooking},
		{"new_patient_booking", OutcomeIndeterminate},
		{"new_patient_booking", OutcomeBooking},
		{"existing_patient_booking", OutcomeReschedule},
		{"existing_patient_booking", OutcomeBooking},
		{"office_information", OutcomeBooking},
		{"", OutcomeBooking},
	} {
		summarizeAnalyticsProjection(&summary, analyticsProjection{call: AnalyticsCall{StartedAt: day}, intent: call.intent, appointmentOutcome: call.outcome})
	}
	got := summary.Daily[0]
	if got.NewPatientIntentCalls != 3 || got.NewPatientIntentBookings != 2 || got.ExistingPatientIntentCalls != 2 || got.ExistingPatientIntentBookings != 1 || got.BookingCount != 5 {
		t.Fatalf("intent bookings = %+v", got)
	}
}
