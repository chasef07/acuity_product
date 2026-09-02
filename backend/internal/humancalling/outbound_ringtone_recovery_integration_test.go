package humancalling_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
)

func TestOutboundRingtoneEndedUsesExactHangupCommand(t *testing.T) {
	for _, staffEnded := range []bool{false, true} {
		name := "destination_unanswered"
		if staffEnded {
			name = "staff_hangup"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
			provider := &recordingProvider{}
			fixture := newOutboundEndFixture(t, "ringtone-end", now, provider)
			calling := fixture.calling
			call := fixture.startCall(t, "ringtone-end-call")
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
			staff.OccurredAt = now
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
			dial := provider.last(humancalling.CommandDialOutboundDestination)
			destination := humancalling.ProviderFact{
				EventID: "destination-initiated", Type: humancalling.FactCallInitiated,
				OccurredAt: now.Add(3 * time.Second), ConnectionID: "staff-call-control-connection",
				CallControlID: "destination-control", CallLegID: "destination-leg", CallSessionID: "destination-session",
				ClientState: dial.Payload["client_state"].(string),
			}
			if err := calling.ApplyProviderFact(ctx, destination); err != nil {
				t.Fatal(err)
			}
			processAllCommands(t, calling)
			bridge := provider.last(humancalling.CommandBridge)
			playback := staff
			playback.EventID, playback.Type = "ringtone-started", humancalling.FactPlaybackStarted
			playback.ClientState = bridge.Payload["client_state"].(string)
			if err := calling.ApplyProviderFact(ctx, playback); err != nil {
				t.Fatal(err)
			}
			if staffEnded {
				if _, err := calling.RequestHangup(ctx, fixture.identity, fixture.sessionID, call.ID); err != nil {
					t.Fatal(err)
				}
			}
			destination.EventID, destination.Type = "destination-ended", humancalling.FactCallHangup
			destination.OccurredAt = now.Add(20 * time.Second)
			if err := calling.ApplyProviderFact(ctx, destination); err != nil {
				t.Fatal(err)
			}
			processAllCommands(t, calling)
			var hangupClientState string
			if err := fixture.pool.QueryRow(ctx, `
				SELECT payload->>'client_state' FROM human_calling_provider_commands
				WHERE call_id=$1 AND call_leg_id=$2 AND action='HANGUP_LEG'
				ORDER BY created_at DESC LIMIT 1
			`, call.ID, staffDial.CallLegID).Scan(&hangupClientState); err != nil {
				t.Fatal(err)
			}
			staff.EventID, staff.Type = "staff-ended", humancalling.FactCallHangup
			staff.OccurredAt, staff.ClientState = now.Add(21*time.Second), hangupClientState
			if err := calling.ApplyProviderFact(ctx, staff); err != nil {
				t.Fatal(err)
			}
			playback.EventID, playback.Type = "ringtone-ended", humancalling.FactPlaybackEnded
			playback.OccurredAt, playback.ClientState = staff.OccurredAt, hangupClientState
			playback.PlaybackStatus = "call_hangup"
			for attempt := 0; attempt < 2; attempt++ {
				if err := calling.ApplyProviderFact(ctx, playback); err != nil {
					t.Fatalf("terminal ringtone with hangup client state: %v", err)
				}
			}
			wrongLeg := playback
			wrongLeg.EventID, wrongLeg.CallLegID = "wrong-leg", "unrelated-leg"
			if err := calling.ApplyProviderFact(ctx, wrongLeg); !errors.Is(err, humancalling.ErrConflict) {
				t.Fatalf("mismatched ringtone identity error=%v, want conflict", err)
			}
			var terminal string
			var activeLegs, activeCommands, recordings, projected int
			if err := fixture.pool.QueryRow(ctx, `
				SELECT terminal_outcome,
				(SELECT count(*) FROM human_calling_call_legs WHERE call_id=$1 AND state NOT IN ('ENDED','FAILED')),
				(SELECT count(*) FROM human_calling_provider_commands WHERE call_id=$1 AND state IN ('PENDING','SENDING','AMBIGUOUS')),
				(SELECT count(*) FROM human_calling_call_recordings WHERE call_id=$1),
				(SELECT count(*) FROM human_calling_projected_facts WHERE event_id='ringtone-ended')
				FROM human_calling_calls WHERE id=$1
			`, call.ID).Scan(&terminal, &activeLegs, &activeCommands, &recordings, &projected); err != nil {
				t.Fatal(err)
			}
			if terminal != "UNANSWERED" || activeLegs != 0 || activeCommands != 0 || recordings != 0 || projected != 1 {
				t.Fatalf("terminal=%s legs=%d commands=%d recordings=%d facts=%d", terminal, activeLegs, activeCommands, recordings, projected)
			}
			raw, err := json.Marshal(map[string]any{"data": map[string]any{
				"record_type": "event", "event_type": "call.playback.ended", "id": "quarantined-ringtone",
				"occurred_at": playback.OccurredAt.Format(time.RFC3339Nano), "payload": map[string]any{
					"call_control_id": playback.CallControlID, "call_leg_id": playback.CallLegID,
					"call_session_id": playback.CallSessionID, "client_state": playback.ClientState, "status": "call_hangup",
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.pool.Exec(ctx, `INSERT INTO human_calling_provider_receipts(event_id,call_id,event_type,received_at,signature_timestamp,raw_body,state,projection_attempts,projection_error_code,quarantined_at,last_attempt_at) VALUES('quarantined-ringtone',$1,'call.playback.ended',$2,1,$3,'QUARANTINED',10,'PROJECTION_APPLY_FACT_CONFLICT',$2,$2)`, call.ID, now, raw); err != nil {
				t.Fatal(err)
			}
			operator := access.Identity{Subject: "ringtone-operator", Email: "operator@example.test", EmailVerified: true}
			if _, err := fixture.pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES($1,$2)`, operator.Email, operator.Subject); err != nil {
				t.Fatal(err)
			}
			timeline, err := calling.ReadOperatorTimeline(ctx, operator, call.ID)
			if err != nil {
				t.Fatal(err)
			}
			var reference string
			for _, entry := range timeline.Entries {
				if entry.RecoveryReference != "" {
					reference = entry.RecoveryReference
				}
			}
			command := humancalling.RequeueQuarantinedReceiptCommand{Identity: fixture.identity, PracticeID: fixture.authorization.Practice.ID, ReceiptReference: reference}
			if err := calling.RecoverQuarantinedRingtone(ctx, command); !errors.Is(err, humancalling.ErrDenied) {
				t.Fatalf("ordinary Staff recovery error=%v", err)
			}
			command.Identity = operator
			if err := calling.RecoverQuarantinedRingtone(ctx, command); err != nil {
				t.Fatalf("audited recovery: %v", err)
			}
			var receiptState string
			var attempts, audits int
			var rawUnchanged bool
			if err := fixture.pool.QueryRow(ctx, `SELECT state,projection_attempts,raw_body=$1,(SELECT count(*) FROM access_audit_events WHERE action='provider_receipt.recovered') FROM human_calling_provider_receipts WHERE event_id='quarantined-ringtone'`, raw).Scan(&receiptState, &attempts, &rawUnchanged, &audits); err != nil {
				t.Fatal(err)
			}
			if receiptState != "APPLIED" || attempts != 10 || !rawUnchanged || audits != 1 {
				t.Fatalf("recovery state=%s attempts=%d raw=%v audits=%d", receiptState, attempts, rawUnchanged, audits)
			}
		})
	}
}
