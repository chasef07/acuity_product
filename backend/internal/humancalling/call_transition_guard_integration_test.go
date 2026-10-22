package humancalling_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
)

func TestIllegalOutcomeMoveQuarantinesReceiptOnFirstAttempt(t *testing.T) {
	call := admitInboundCall(t, "illegal-outcome")
	call.answerAndRing(t)
	call.completeRingWindow(t)
	call.completeGreeting(t)
	processAllCommands(t, call.worker)
	ctx := context.Background()
	if _, err := call.db.Owner.Exec(ctx, `
		UPDATE human_calling_calls SET terminal_outcome = 'ABANDONED', ended_at = $1
	`, call.now.Add(35*time.Second)); err != nil {
		t.Fatal(err)
	}
	saved := humancalling.ProviderFact{
		EventID: "illegal-outcome-recording-saved", Type: humancalling.FactRecordingSaved,
		OccurredAt: call.now.Add(40 * time.Second), ConnectionID: call.caller.ConnectionID,
		CallControlID: call.caller.CallControlID, CallLegID: call.caller.CallLegID,
		CallSessionID: call.caller.CallSessionID, RecordingID: "illegal-outcome-recording",
		ClientState:        call.clientState(humancalling.CommandStartVoicemailRecording),
		RecordingStartedAt: call.now, RecordingEndedAt: call.now.Add(10 * time.Second),
	}

	err := call.worker.ApplyProviderFact(ctx, saved)
	if !errors.Is(err, humancalling.ErrIllegalCallTransition) || !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("VOICEMAIL over ABANDONED = %v, want ErrIllegalCallTransition", err)
	}
	if outcome := call.terminalOutcome(t); outcome != "ABANDONED" {
		t.Fatalf("rejected outcome move left %s", outcome)
	}

	body, err := json.Marshal(map[string]any{"data": map[string]any{
		"record_type": "event", "event_type": "call.recording.saved", "id": saved.EventID,
		"occurred_at": saved.OccurredAt.Format(time.RFC3339Nano),
		"payload": map[string]any{
			"connection_id": saved.ConnectionID, "call_control_id": saved.CallControlID,
			"call_leg_id": saved.CallLegID, "call_session_id": saved.CallSessionID,
			"client_state": saved.ClientState, "recording_id": saved.RecordingID,
			"recording_started_at": saved.RecordingStartedAt.Format(time.RFC3339Nano),
			"recording_ended_at":   saved.RecordingEndedAt.Format(time.RFC3339Nano),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := call.db.Owner.Exec(ctx, `
		INSERT INTO human_calling_provider_receipts (
			event_id, event_type, occurred_at, received_at, signature_timestamp,
			raw_body, next_attempt_at
		) VALUES ($1, 'call.recording.saved', $2, $3, 1, $4, $3)
	`, saved.EventID, saved.OccurredAt, call.now, body); err != nil {
		t.Fatal(err)
	}
	if processed, err := call.worker.ProcessNextReceipt(ctx); err != nil || !processed {
		t.Fatalf("process illegal outcome receipt = %t, %v", processed, err)
	}
	var state, code string
	var attempts int
	if err := call.db.Owner.QueryRow(ctx, `
		SELECT state, projection_attempts, COALESCE(projection_error_code, '')
		FROM human_calling_provider_receipts WHERE event_id = $1
	`, saved.EventID).Scan(&state, &attempts, &code); err != nil {
		t.Fatal(err)
	}
	if state != string(humancalling.ReceiptQuarantined) || attempts != 1 || code != "ILLEGAL_CALL_TRANSITION" {
		t.Fatalf("illegal outcome receipt = %s after %d attempts with %q", state, attempts, code)
	}
	if outcome := call.terminalOutcome(t); outcome != "ABANDONED" {
		t.Fatalf("quarantined outcome move left %s", outcome)
	}
	audit, err := call.worker.AuditProviderReceipts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Quarantine) != 1 || audit.Quarantine[0].ErrorCode != "ILLEGAL_CALL_TRANSITION" {
		t.Fatalf("receipt audit quarantine = %+v", audit.Quarantine)
	}
}

func TestIllegalLegMoveFailsProviderCommandWithoutResending(t *testing.T) {
	call := admitInboundCall(t, "illegal-leg")
	ctx := context.Background()
	call.provider.mu.Lock()
	call.provider.actionErrors = map[humancalling.CommandAction][]error{
		humancalling.CommandAnswerCaller: {&humancalling.ProviderError{
			SafeCode: "TELNYX_ANSWER_REJECTED", Definitive: true,
		}},
	}
	call.provider.mu.Unlock()
	if _, err := call.db.Owner.Exec(ctx, `
		CREATE FUNCTION rewind_failed_call_leg() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			NEW.state := 'PENDING';
			RETURN NEW;
		END
		$$;
		CREATE TRIGGER a_rewind_failed_call_leg
		BEFORE UPDATE OF state ON human_calling_call_legs
		FOR EACH ROW WHEN (NEW.state = 'FAILED')
		EXECUTE FUNCTION rewind_failed_call_leg()
	`); err != nil {
		t.Fatalf("install regressed CallLeg writer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = call.db.Owner.Exec(context.Background(), `
			DROP TRIGGER IF EXISTS a_rewind_failed_call_leg ON human_calling_call_legs;
			DROP FUNCTION IF EXISTS rewind_failed_call_leg()
		`)
	})

	processed, err := call.worker.ProcessNextCommand(ctx)
	if !processed || !errors.Is(err, humancalling.ErrIllegalCallTransition) {
		t.Fatalf("Answer result with illegal CallLeg move = %t, %v", processed, err)
	}
	var legState, outcome, commandState, commandCode string
	if err := call.db.Owner.QueryRow(ctx, `
		SELECT leg.state, COALESCE(call.terminal_outcome, ''),
			command.state, COALESCE(command.last_error_code, '')
		FROM human_calling_call_legs leg
		JOIN human_calling_calls call ON call.id = leg.call_id
		JOIN human_calling_provider_commands command
			ON command.call_leg_id = leg.id AND command.action = 'ANSWER_CALLER'
		WHERE leg.role = 'CALLER'
	`).Scan(&legState, &outcome, &commandState, &commandCode); err != nil {
		t.Fatal(err)
	}
	if legState != "RINGING" || outcome != "" || commandState != "FAILED" ||
		commandCode != "ILLEGAL_CALL_TRANSITION" {
		t.Fatalf("illegal CallLeg move = leg:%s outcome:%q command:%s/%s",
			legState, outcome, commandState, commandCode)
	}
	if _, err := call.db.Owner.Exec(ctx, `
		UPDATE human_calling_provider_commands SET updated_at = $1 WHERE action = 'ANSWER_CALLER'
	`, call.now.Add(-31*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := call.worker.MaintainOutgoingCallLegs(ctx); err != nil {
		t.Fatalf("recover interrupted commands: %v", err)
	}
	if processed, err := call.worker.ProcessNextCommand(ctx); err != nil || processed {
		t.Fatalf("failed Answer command processed again = %t, %v", processed, err)
	}
	if answers := len(call.provider.all(humancalling.CommandAnswerCaller)); answers != 1 {
		t.Fatalf("Answer sent %d times, want 1", answers)
	}
}
