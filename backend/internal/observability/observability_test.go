package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLoggerBoundsLabelsAndExcludesIdentifiers(t *testing.T) {
	var output bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimeRole("patient@example.test"),
		"patient@example.test",
		slog.New(slog.NewJSONHandler(&output, nil)),
	)

	observer.Observe(observability.ProviderCommandCompleted(
		observability.CommandAction("+15555550100"),
		observability.CommandOutcome("call-123"),
		2*time.Second,
		500*time.Millisecond,
	))

	for _, forbidden := range []string{
		"patient@example.test",
		"+15555550100",
		"call-123",
	} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("metrics contain unbounded value %q: %s", forbidden, output.String())
		}
	}
	entry := findMetric(t, entries(t, output.String()),
		"acuity_call_center_provider_command")
	if entry["runtime_role"] != "other" ||
		entry["revision"] != "unknown" ||
		entry["action"] != "other" ||
		entry["outcome"] != "other" {
		t.Fatalf("bounded provider metric = %#v", entry)
	}
}

func TestLoggerEmitsFixedConvergenceCapacityAndCoordinationContract(t *testing.T) {
	var output bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimeWorker,
		"worker-00018-a1b",
		slog.New(slog.NewJSONHandler(&output, nil)),
	)

	observer.Observe(observability.WebhookAcknowledged(
		observability.WebhookDuplicate, 18*time.Millisecond))
	observer.Observe(observability.ReceiptQueue(12, 7*time.Second, 1))
	observer.Observe(observability.TerminalCleanup(2, 90*time.Second, 3, 2*time.Minute))
	observer.Observe(observability.ReceiptProcessed(
		observability.ReceiptQuarantined, 7*time.Second, 80*time.Millisecond))
	observer.Observe(observability.DatabasePoolState(4, 1, 4))
	observer.Observe(observability.DatabaseExecuted(
		observability.DatabaseDeadlock,
		12*time.Millisecond,
	))
	observer.Observe(observability.BackendRequest(
		observability.AvailabilityRoute("/v1/patients/patient-123"),
		observability.AvailabilityOutcome("patient@example.test"),
		observability.FailureStage("+15555550100"),
		20*time.Millisecond,
	))
	observer.Observe(observability.SSEStreamOpened())
	observer.Observe(observability.SSEStreamClosed(observability.SSEClientClosed))
	observer.Observe(observability.SSEListenerConnected(true))
	observer.Observe(observability.StaffAnswered(observability.StaffAnswerLostRace))
	observer.Observe(observability.CallLegBridged(1250 * time.Millisecond))
	observer.Observe(observability.VoicemailPlayback(
		observability.VoicemailPlaybackRateLimited,
		250*time.Millisecond,
	))

	logs := entries(t, output.String())
	assertField(t, logs, "acuity_call_center_webhook_acknowledgement",
		"seconds", 0.018)
	assertField(t, logs, "acuity_call_center_receipt_queue", "depth", float64(12))
	assertField(t, logs, "acuity_call_center_receipt_queue",
		"quarantined_depth", float64(1))
	assertField(t, logs, "acuity_call_center_terminal_cleanup",
		"staff_occupancy", float64(2))
	assertField(t, logs, "acuity_call_center_terminal_cleanup",
		"unresolved_hangups", float64(3))
	assertField(t, logs, "acuity_call_center_receipt_processing",
		"outcome", "quarantined")
	assertField(t, logs, "acuity_call_center_database_pool",
		"saturation_ratio", float64(1))
	assertField(t, logs, "acuity_backend_database_execution",
		"cause", "deadlock")
	availability := findMetric(t, logs, "acuity_backend_availability")
	if availability["route"] != "other" ||
		availability["outcome"] != "other" ||
		availability["failure_stage"] != "other" {
		t.Fatalf("bounded availability metric = %#v", availability)
	}
	assertField(t, logs, "acuity_call_center_sse_stream", "active", float64(0))
	assertField(t, logs, "acuity_call_center_sse_listener",
		"state", "connected")
	assertField(t, logs, "acuity_call_center_staff_answer",
		"outcome", "lost_race")
	assertField(t, logs, "acuity_call_center_answer_to_bridge",
		"seconds", 1.25)
	assertField(t, logs, "acuity_call_center_voicemail_playback",
		"outcome", "rate_limited")
}

func TestPoolTracerClassifiesBoundedAcquisitionOutcome(t *testing.T) {
	var output bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimePortalAPI,
		"portal-api-00009",
		slog.New(slog.NewJSONHandler(&output, nil)),
	)
	tracer := observability.NewPoolTracer(observer)
	ctx := tracer.TraceAcquireStart(
		context.Background(),
		nil,
		pgxpool.TraceAcquireStartData{},
	)
	tracer.TraceAcquireEnd(ctx, nil, pgxpool.TraceAcquireEndData{
		Err: context.DeadlineExceeded,
	})

	entry := findMetric(t, entries(t, output.String()),
		"acuity_call_center_database_pool_acquire")
	if entry["outcome"] != "timeout" {
		t.Fatalf("pool acquisition metric = %#v", entry)
	}
}

func TestPoolTracerClassifiesCanceledAcquisitionSeparatelyFromTimeout(t *testing.T) {
	var output bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimePortalAPI,
		"portal-api-00009",
		slog.New(slog.NewJSONHandler(&output, nil)),
	)
	tracer := observability.NewPoolTracer(observer)
	ctx := tracer.TraceAcquireStart(
		context.Background(),
		nil,
		pgxpool.TraceAcquireStartData{},
	)
	tracer.TraceAcquireEnd(ctx, nil, pgxpool.TraceAcquireEndData{
		Err: context.Canceled,
	})

	entry := findMetric(t, entries(t, output.String()),
		"acuity_call_center_database_pool_acquire")
	if entry["outcome"] != "canceled" {
		t.Fatalf("pool acquisition metric = %#v", entry)
	}
}

func entries(t *testing.T, output string) []map[string]any {
	t.Helper()
	var result []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode metric log: %v", err)
		}
		result = append(result, entry)
	}
	return result
}

func findMetric(t *testing.T, logs []map[string]any, name string) map[string]any {
	t.Helper()
	for index := len(logs) - 1; index >= 0; index-- {
		if logs[index]["metric"] == name {
			return logs[index]
		}
	}
	t.Fatalf("metric %q not found in %#v", name, logs)
	return nil
}

func assertField(
	t *testing.T,
	logs []map[string]any,
	metric string,
	field string,
	want any,
) {
	t.Helper()
	if got := findMetric(t, logs, metric)[field]; got != want {
		t.Fatalf("%s %s = %#v, want %#v", metric, field, got, want)
	}
}
