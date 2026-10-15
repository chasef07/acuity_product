package humancalling_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
)

func TestLateBridgeAndRingtoneFactsAfterOutboundHangupAreTerminal(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 13, 16, 0, 0, 0, time.UTC)
	provider := &recordingProvider{}
	fixture := newOutboundEndFixture(t, "late-fact", now, provider)
	calling := fixture.calling
	call := fixture.startCall(t, "late-fact-call")
	processAllCommands(t, calling)
	staffDial := provider.last(humancalling.CommandDialOutboundStaff)
	staff := humancalling.ProviderFact{
		EventID: "staff-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now, ConnectionID: "staff-call-control-connection",
		CallControlID: "staff-control", CallLegID: "staff-leg", CallSessionID: "staff-session",
		ClientState: staffDial.Payload["client_state"].(string),
	}
	if err := calling.ApplyProviderFact(ctx, staff); err != nil {
		t.Fatal(err)
	}
	staff.EventID, staff.Type = "staff-answered", humancalling.FactCallAnswered
	if err := calling.ApplyProviderFact(ctx, staff); err != nil {
		t.Fatal(err)
	}
	state, err := calling.ReadCallingState(ctx, fixture.identity)
	if err != nil || len(state.Ringing) != 1 {
		t.Fatalf("read media token: ringing=%d err=%v", len(state.Ringing), err)
	}
	if _, err := calling.ConfirmOutboundMedia(ctx, humancalling.ConfirmOutboundMediaCommand{
		Identity: fixture.identity, SessionID: fixture.sessionID, CallID: call.ID,
		MediaToken: state.Ringing[0].MediaToken,
	}); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	destinationDial := provider.last(humancalling.CommandDialOutboundDestination)
	destination := humancalling.ProviderFact{
		EventID: "destination-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(3 * time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: "destination-control", CallLegID: "destination-leg", CallSessionID: "destination-session",
		ClientState: destinationDial.Payload["client_state"].(string),
	}
	if err := calling.ApplyProviderFact(ctx, destination); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	bridgeClientState := provider.last(humancalling.CommandBridge).Payload["client_state"].(string)
	staffBridged := staff
	staffBridged.EventID, staffBridged.Type = "staff-bridged", humancalling.FactCallBridged
	staffBridged.OccurredAt, staffBridged.ClientState = now.Add(4*time.Second), bridgeClientState
	if err := calling.ApplyProviderFact(ctx, staffBridged); err != nil {
		t.Fatal(err)
	}

	receive := func(eventID, eventType string, fact humancalling.ProviderFact, status string) {
		t.Helper()
		payload := map[string]any{
			"connection_id": fact.ConnectionID, "call_control_id": fact.CallControlID,
			"call_leg_id": fact.CallLegID, "call_session_id": fact.CallSessionID,
			"client_state": fact.ClientState,
		}
		if status != "" {
			payload["status"] = status
		}
		raw, err := json.Marshal(map[string]any{"data": map[string]any{
			"record_type": "event", "event_type": eventType, "id": eventID,
			"occurred_at": fact.OccurredAt.Format(time.RFC3339Nano), "payload": payload,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.pool.Exec(ctx, `
			INSERT INTO human_calling_provider_receipts (
				event_id, event_type, occurred_at, received_at,
				signature_timestamp, raw_body, next_attempt_at, call_session_id
			) VALUES ($1, $2, $3, $4, 1, $5, $4, $6)
		`, eventID, eventType, fact.OccurredAt, now, raw, fact.CallSessionID); err != nil {
			t.Fatal(err)
		}
		if processed, err := calling.ProcessNextReceipt(ctx); !processed || err != nil {
			t.Fatalf("process %s: processed=%t err=%v", eventID, processed, err)
		}
	}
	receiptOutcome := func(eventID string) string {
		t.Helper()
		var outcome string
		if err := fixture.pool.QueryRow(ctx, `
			SELECT state || '/' || COALESCE(projection_error_code, '')
			FROM human_calling_provider_receipts WHERE event_id = $1
		`, eventID).Scan(&outcome); err != nil {
			t.Fatal(err)
		}
		return outcome
	}

	mismatched := destination
	mismatched.CallLegID, mismatched.CallSessionID = "unrelated-leg", "unrelated-session"
	mismatched.OccurredAt = now.Add(5 * time.Second)
	receive("live-mismatched-bridge", "call.bridged", mismatched, "")
	if got := receiptOutcome("live-mismatched-bridge"); got != "PENDING/PROJECTION_APPLY_FACT_CONFLICT" {
		t.Fatalf("live mismatched bridge receipt = %s, want retryable conflict", got)
	}

	if _, err := calling.RequestHangup(ctx, fixture.identity, fixture.sessionID, call.ID); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	hangupClientState := func(role string) string {
		t.Helper()
		var clientState string
		if err := fixture.pool.QueryRow(ctx, `
			SELECT command.payload->>'client_state'
			FROM human_calling_provider_commands command
			JOIN human_calling_call_legs leg ON leg.id = command.call_leg_id
			WHERE command.call_id = $1 AND leg.role = $2 AND command.action = 'HANGUP_LEG'
		`, call.ID, role).Scan(&clientState); err != nil {
			t.Fatal(err)
		}
		return clientState
	}
	destination.EventID, destination.Type = "destination-ended", humancalling.FactCallHangup
	destination.OccurredAt = now.Add(20 * time.Second)
	destination.ClientState = hangupClientState("DESTINATION")
	if err := calling.ApplyProviderFact(ctx, destination); err != nil {
		t.Fatal(err)
	}
	staff.EventID, staff.Type = "staff-ended", humancalling.FactCallHangup
	staff.OccurredAt, staff.ClientState = now.Add(20*time.Second), hangupClientState("STAFF")
	if err := calling.ApplyProviderFact(ctx, staff); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	var ended bool
	if err := fixture.pool.QueryRow(ctx, `
		SELECT terminal_outcome IS NOT NULL AND ended_at IS NOT NULL
			AND NOT EXISTS (
				SELECT 1 FROM human_calling_call_legs
				WHERE call_id = $1 AND state NOT IN ('ENDED', 'FAILED')
			)
		FROM human_calling_calls WHERE id = $1
	`, call.ID).Scan(&ended); err != nil || !ended {
		t.Fatalf("Call ended=%t err=%v", ended, err)
	}

	late := now.Add(22 * time.Second)
	lateBridge := destination
	lateBridge.OccurredAt = late
	receive("late-destination-bridged", "call.bridged", lateBridge, "")
	lateRingtone := staff
	lateRingtone.OccurredAt = late
	receive("late-ringtone-started", "call.playback.started", lateRingtone, "")
	receive("late-ringtone-ended", "call.playback.ended", lateRingtone, "completed")
	for _, eventID := range []string{
		"late-destination-bridged", "late-ringtone-started", "late-ringtone-ended",
	} {
		if got := receiptOutcome(eventID); got != "FAILED/TERMINAL_OR_OBSOLETE_PROVIDER_FACT" {
			t.Errorf("%s receipt = %s, want terminal or obsolete", eventID, got)
		}
	}
	lateMismatched := lateBridge
	lateMismatched.CallLegID, lateMismatched.CallSessionID = "unrelated-leg", "late-unrelated-session"
	receive("late-mismatched-bridge", "call.bridged", lateMismatched, "")
	if got := receiptOutcome("late-mismatched-bridge"); got != "PENDING/PROJECTION_APPLY_FACT_CONFLICT" {
		t.Fatalf("mismatched bridge with hangup client state = %s, want retryable conflict", got)
	}
}
