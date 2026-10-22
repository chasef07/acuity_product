package humancalling_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

type receiptReplay struct {
	callVersionFixture
	privateKey ed25519.PrivateKey
	ingress    *humancalling.Module
}

func newReceiptReplay(t *testing.T, prefix string, staffCount int, provider *recordingProvider) receiptReplay {
	t.Helper()
	fixture := newCallVersionFixture(t, prefix, staffCount, provider)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ingress := humancalling.New(fixture.db.As(testdb.Provider), nil, nil,
		humancalling.Config{WebhookPublicKeys: [][]byte{publicKey}},
		func() time.Time { return *fixture.current })
	return receiptReplay{callVersionFixture: fixture, privateKey: privateKey, ingress: ingress}
}

func (replay receiptReplay) fact(
	base humancalling.ProviderFact, name string, kind humancalling.FactType, millis int,
) humancalling.ProviderFact {
	base.EventID = replay.prefix + "-" + name + "-" + string(kind)
	base.Type = kind
	base.OccurredAt = replay.now.Add(time.Duration(millis) * time.Millisecond)
	if kind == humancalling.FactCallHangup {
		base.HangupCause, base.TerminationSource = "normal_clearing", "caller"
	}
	return base
}

func (replay receiptReplay) deliver(t *testing.T, facts ...humancalling.ProviderFact) {
	t.Helper()
	for _, fact := range facts {
		payload := map[string]any{
			"connection_id": fact.ConnectionID, "call_control_id": fact.CallControlID,
			"call_leg_id": fact.CallLegID, "call_session_id": fact.CallSessionID,
		}
		for key, value := range map[string]string{
			"client_state": fact.ClientState, "status": fact.PlaybackStatus,
			"hangup_cause": fact.HangupCause, "hangup_source": fact.TerminationSource,
			"from": fact.From, "to": fact.To, "recording_id": fact.RecordingID,
		} {
			if value != "" {
				payload[key] = value
			}
		}
		if !fact.RecordingStartedAt.IsZero() {
			payload["recording_started_at"] = fact.RecordingStartedAt.Format(time.RFC3339Nano)
			payload["recording_ended_at"] = fact.RecordingEndedAt.Format(time.RFC3339Nano)
		}
		raw, err := json.Marshal(map[string]any{"data": map[string]any{
			"record_type": "event", "event_type": string(fact.Type), "id": fact.EventID,
			"occurred_at": fact.OccurredAt.Format(time.RFC3339Nano), "payload": payload,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if fact.OccurredAt.After(*replay.current) {
			*replay.current = fact.OccurredAt
		}
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signature := base64.StdEncoding.EncodeToString(
			ed25519.Sign(replay.privateKey, append([]byte(timestamp+"|"), raw...)))
		if receipt, err := replay.ingress.ReceiveWebhook(context.Background(), raw, timestamp, signature); err != nil ||
			receipt.Duplicate {
			t.Fatalf("deliver %s: receipt=%+v err=%v", fact.EventID, receipt, err)
		}
	}
}

func (replay receiptReplay) settle(t *testing.T) {
	t.Helper()
	for pass := 0; pass < 20; pass++ {
		for {
			processed, err := replay.worker.ProcessNextReceipt(context.Background())
			if err != nil {
				t.Fatalf("process receipt: %v", err)
			}
			if !processed {
				break
			}
		}
		processAllCommands(t, replay.worker)
		var due int
		if err := replay.db.Owner.QueryRow(context.Background(), `
			SELECT count(*) FROM human_calling_provider_receipts
			WHERE state IN ('PENDING', 'PROCESSING')
		`).Scan(&due); err != nil {
			t.Fatal(err)
		}
		if due == 0 {
			return
		}
		*replay.current = replay.current.Add(time.Minute)
	}
}

func (replay receiptReplay) requireSettledOnFirstAttempt(t *testing.T, want map[string]string) {
	t.Helper()
	rows, err := replay.db.Owner.Query(context.Background(), `
		SELECT event_id, state || '/' || COALESCE(projection_error_code, ''), projection_attempts
		FROM human_calling_provider_receipts
		ORDER BY received_at, event_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var eventID, outcome string
		var attempts int
		if err := rows.Scan(&eventID, &outcome, &attempts); err != nil {
			t.Fatal(err)
		}
		expected, named := want[eventID]
		if !named {
			expected = "APPLIED/"
		}
		if outcome != expected || attempts != 1 {
			t.Errorf("receipt %s = %s after %d attempts, want %s after 1", eventID, outcome, attempts, expected)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func (replay receiptReplay) receiptOutcome(t *testing.T, eventID string) (string, int) {
	t.Helper()
	var outcome string
	var attempts int
	if err := replay.db.Owner.QueryRow(context.Background(), `
		SELECT state || '/' || COALESCE(projection_error_code, ''), projection_attempts
		FROM human_calling_provider_receipts WHERE event_id = $1
	`, eventID).Scan(&outcome, &attempts); err != nil {
		t.Fatal(err)
	}
	return outcome, attempts
}

func withClientStateKind(t *testing.T, clientState string, kind string) string {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(clientState)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(decoded, &state); err != nil {
		t.Fatal(err)
	}
	state["kind"] = kind
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(encoded)
}

func (replay receiptReplay) callState(t *testing.T) string {
	t.Helper()
	var state string
	if err := replay.db.Owner.QueryRow(context.Background(), `
		SELECT state FROM human_calling_call_states WHERE call_id = $1
	`, replay.callID(t)).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func (replay receiptReplay) clientState(action humancalling.CommandAction) string {
	state, _ := replay.provider.last(action).Payload["client_state"].(string)
	return state
}

func (replay receiptReplay) clientStates(action humancalling.CommandAction) map[string]string {
	states := map[string]string{}
	for _, command := range replay.provider.all(action) {
		states[command.TargetID], _ = command.Payload["client_state"].(string)
	}
	return states
}

type outboundReplay struct {
	receiptReplay
	staffLeg       humancalling.ProviderFact
	destinationLeg humancalling.ProviderFact
	ringtone       humancalling.ProviderFact
}

func dialOutboundReplay(t *testing.T, prefix string) outboundReplay {
	t.Helper()
	replay := newReceiptReplay(t, prefix, 1, &recordingProvider{
		dialResults: []humancalling.ProviderResult{
			{CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-leg"},
			{CallControlID: prefix + "-destination-control", CallLegID: prefix + "-destination-leg"},
		},
	})
	if _, err := replay.db.Owner.Exec(context.Background(), `
		UPDATE access_practices SET connected_call_recording_enabled = true,
			connected_call_recording_retention_days = 30
		WHERE id = $1
	`, replay.authorization.Practice.ID); err != nil {
		t.Fatal(err)
	}
	call := outboundReplay{receiptReplay: replay}
	session := prefix + "-session"
	call.staffLeg = humancalling.ProviderFact{
		ConnectionID: "staff-call-control-connection", CallControlID: prefix + "-staff-control",
		CallLegID: prefix + "-staff-leg", CallSessionID: session,
	}
	call.destinationLeg = humancalling.ProviderFact{
		ConnectionID: "staff-call-control-connection", CallControlID: prefix + "-destination-control",
		CallLegID: prefix + "-destination-leg", CallSessionID: session,
	}
	replay.startOutbound(t)
	processAllCommands(t, replay.worker)
	call.staffLeg.ClientState = replay.clientState(humancalling.CommandDialOutboundStaff)
	call.deliver(t,
		call.fact(call.staffLeg, "staff", humancalling.FactCallInitiated, 1000),
		call.fact(call.staffLeg, "staff", humancalling.FactCallAnswered, 2000),
	)
	call.settle(t)
	state, _, err := replay.portal.ReadCallingStateConditionally(context.Background(), replay.staff[0], "")
	if err != nil || len(state.Ringing) != 1 {
		t.Fatalf("outbound media offer = %#v, %v", state.Ringing, err)
	}
	if _, err := replay.portal.ConfirmOutboundMedia(context.Background(), humancalling.ConfirmOutboundMediaCommand{
		Identity: replay.staff[0], SessionID: replay.session(0),
		CallID: state.Ringing[0].CallID, MediaToken: state.Ringing[0].MediaToken,
	}); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, replay.worker)
	call.destinationLeg.ClientState = replay.clientState(humancalling.CommandDialOutboundDestination)
	call.deliver(t, call.fact(call.destinationLeg, "destination", humancalling.FactCallInitiated, 3000))
	call.settle(t)
	call.ringtone = call.staffLeg
	call.ringtone.ClientState = replay.clientStates(humancalling.CommandBridge)[call.staffLeg.CallControlID]
	call.deliver(t, call.fact(call.ringtone, "ringtone", humancalling.FactPlaybackStarted, 3500))
	call.settle(t)
	return call
}

func (call outboundReplay) hangUp(t *testing.T, millis int) (staff, destination humancalling.ProviderFact) {
	t.Helper()
	*call.current = call.now.Add(time.Duration(millis) * time.Millisecond)
	call.requestHangup(t)
	processAllCommands(t, call.worker)
	hangups := call.clientStates(humancalling.CommandHangupLeg)
	staff, destination = call.staffLeg, call.destinationLeg
	staff.ClientState = hangups[call.staffLeg.CallControlID]
	destination.ClientState = hangups[call.destinationLeg.CallControlID]
	if staff.ClientState == "" || destination.ClientState == "" {
		t.Fatalf("Hangup client states = %#v", hangups)
	}
	return staff, destination
}

func TestReceiptReplayLateBridgeAndRingtoneAfterOutboundHangupSettleObsolete(t *testing.T) {
	call := dialOutboundReplay(t, "replay-483")
	ringtoneEnded := call.fact(call.ringtone, "ringtone", humancalling.FactPlaybackEnded, 3900)
	ringtoneEnded.PlaybackStatus = "completed"
	call.deliver(t,
		call.fact(call.destinationLeg, "destination", humancalling.FactCallAnswered, 3800),
		ringtoneEnded,
	)
	call.settle(t)
	bridgedDestination, bridgedStaff := call.destinationLeg, call.staffLeg
	bridgedDestination.ClientState, bridgedStaff.ClientState = "", ""
	call.deliver(t,
		call.fact(bridgedDestination, "destination", humancalling.FactCallBridged, 4000),
		call.fact(bridgedStaff, "staff", humancalling.FactCallBridged, 4000),
	)
	call.settle(t)
	staff, destination := call.hangUp(t, 6000)
	call.deliver(t,
		call.fact(destination, "destination", humancalling.FactCallHangup, 20000),
		call.fact(staff, "staff", humancalling.FactCallHangup, 20000),
	)
	call.settle(t)
	ended := call.callState(t)

	lateRingtoneEnded := call.fact(staff, "late-ringtone", humancalling.FactPlaybackEnded, 22000)
	lateRingtoneEnded.PlaybackStatus = "completed"
	call.deliver(t,
		call.fact(destination, "late-destination", humancalling.FactCallBridged, 22000),
		call.fact(staff, "late-ringtone", humancalling.FactPlaybackStarted, 22000),
		lateRingtoneEnded,
	)
	call.settle(t)

	obsolete := "FAILED/TERMINAL_OR_OBSOLETE_PROVIDER_FACT"
	call.requireSettledOnFirstAttempt(t, map[string]string{
		"replay-483-late-destination-call.bridged":       obsolete,
		"replay-483-late-ringtone-call.playback.started": obsolete,
		"replay-483-late-ringtone-call.playback.ended":   obsolete,
	})
	if state := call.callState(t); ended != "NEEDS_DISPOSITION" || state != ended {
		t.Fatalf("Call state = %s after hangup, %s after late facts; want NEEDS_DISPOSITION", ended, state)
	}
}

func TestReceiptReplayOutboundRecordingSavedAfterStaffHangupApplies(t *testing.T) {
	call := dialOutboundReplay(t, "replay-152")
	call.deliver(t, call.fact(call.destinationLeg, "destination", humancalling.FactCallAnswered, 3800))
	bridgedDestination, bridgedStaff := call.destinationLeg, call.staffLeg
	bridgedDestination.ClientState, bridgedStaff.ClientState = "", ""
	call.deliver(t,
		call.fact(bridgedDestination, "destination", humancalling.FactCallBridged, 4000),
		call.fact(bridgedStaff, "staff", humancalling.FactCallBridged, 4000),
	)
	call.settle(t)
	staff, destination := call.hangUp(t, 6000)
	call.deliver(t,
		call.fact(destination, "destination", humancalling.FactCallHangup, 7000),
		call.fact(staff, "staff", humancalling.FactCallHangup, 7000),
	)
	call.settle(t)

	saved := call.fact(destination, "destination", humancalling.FactRecordingSaved, 17000)
	saved.RecordingID = "replay-152-recording"
	saved.RecordingStartedAt, saved.RecordingEndedAt = call.now.Add(5*time.Second), call.now.Add(7*time.Second)
	call.deliver(t, saved)
	call.settle(t)

	call.requireSettledOnFirstAttempt(t, nil)
	read, err := call.portal.ReadCall(context.Background(), call.staff[0], call.callID(t))
	if err != nil {
		t.Fatal(err)
	}
	if state := call.callState(t); state != "NEEDS_DISPOSITION" || read.Recording.AudioState != humancalling.RecordingReady {
		t.Fatalf("Call state = %s recording = %s; want NEEDS_DISPOSITION with READY recording",
			state, read.Recording.AudioState)
	}
}

func TestReceiptReplayDestinationBridgedAfterLocalHangupSettlesObsolete(t *testing.T) {
	call := dialOutboundReplay(t, "replay-quarantine")
	staff, destination := call.hangUp(t, 6000)
	answered := call.fact(destination, "destination", humancalling.FactCallAnswered, 6100)
	ringtoneEnded := call.fact(staff, "ringtone", humancalling.FactPlaybackEnded, 6280)
	ringtoneEnded.PlaybackStatus = "call_hangup"
	call.deliver(t, answered, ringtoneEnded)
	call.deliver(t,
		call.fact(destination, "destination", humancalling.FactCallHangup, 6340),
		call.fact(staff, "staff", humancalling.FactCallHangup, 6340),
	)
	call.settle(t)
	call.deliver(t,
		call.fact(destination, "destination", humancalling.FactCallBridged, 6300),
		call.fact(staff, "staff", humancalling.FactCallBridged, 6300),
	)
	call.settle(t)

	call.requireSettledOnFirstAttempt(t, map[string]string{
		"replay-quarantine-destination-call.bridged": "FAILED/TERMINAL_OR_OBSOLETE_PROVIDER_FACT",
	})
	if state := call.callState(t); state != "NEEDS_DISPOSITION" {
		t.Fatalf("Call state = %s, want NEEDS_DISPOSITION", state)
	}
}

type inboundReplay struct {
	receiptReplay
	callerLeg humancalling.ProviderFact
	staffLeg  humancalling.ProviderFact
}

func answerInboundReplay(t *testing.T, prefix string) inboundReplay {
	t.Helper()
	replay := newReceiptReplay(t, prefix, 1, &recordingProvider{
		dialResults: []humancalling.ProviderResult{
			{CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-leg"},
		},
	})
	handoff, err := replay.portal.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject: "abita-" + prefix, PracticeID: replay.authorization.Practice.ID,
		},
		LocationID: replay.authorization.Locations[0].ID, SourceCallID: prefix + "-source",
		IdempotencyKey: prefix + "-handoff",
		Contact:        humancalling.ContactContext{Phone: "+15555550100"},
	})
	if err != nil {
		t.Fatalf("create handoff: %v", err)
	}
	call := inboundReplay{receiptReplay: replay}
	call.callerLeg = humancalling.ProviderFact{
		ConnectionID: "staff-call-control-connection", CallControlID: prefix + "-caller-control",
		CallLegID: prefix + "-caller-leg", CallSessionID: prefix + "-caller-session",
		From: "+15555550100", To: handoff.SIPDestination,
	}
	call.deliver(t, call.fact(call.callerLeg, "caller", humancalling.FactCallInitiated, 0))
	call.settle(t)
	call.callerLeg.ClientState = replay.clientStates(humancalling.CommandAnswerCaller)[call.callerLeg.CallControlID]
	call.deliver(t, call.fact(call.callerLeg, "caller", humancalling.FactCallAnswered, 1000))
	call.settle(t)
	call.staffLeg = humancalling.ProviderFact{
		ConnectionID: "staff-call-control-connection", CallControlID: prefix + "-staff-control",
		CallLegID: prefix + "-staff-leg", CallSessionID: prefix + "-staff-session",
		ClientState: replay.clientStates(humancalling.CommandDialStaff)[call.callerLeg.CallControlID],
	}
	call.callerLeg.ClientState = replay.clientStates(humancalling.CommandStartRingWindow)[call.callerLeg.CallControlID]
	call.deliver(t,
		call.fact(call.callerLeg, "ring-window", humancalling.FactPlaybackStarted, 1500),
		call.fact(call.staffLeg, "staff", humancalling.FactCallInitiated, 2000),
	)
	call.settle(t)
	return call
}

func TestReceiptReplayDelayedStaffHangupBeforeLocalEndingApplies(t *testing.T) {
	call := answerInboundReplay(t, "replay-120")
	call.deliver(t, call.fact(call.staffLeg, "staff", humancalling.FactCallAnswered, 3000))
	call.settle(t)
	bridgedStaff, bridgedCaller := call.staffLeg, call.callerLeg
	bridgedStaff.ClientState = call.clientStates(humancalling.CommandBridge)[call.staffLeg.CallControlID]
	call.deliver(t,
		call.fact(bridgedStaff, "staff", humancalling.FactCallBridged, 4000),
		call.fact(bridgedCaller, "caller", humancalling.FactCallBridged, 4000),
	)
	call.settle(t)
	*call.current = call.now.Add(10 * time.Second)
	call.requestHangup(t)
	processAllCommands(t, call.worker)
	hangups := call.clientStates(humancalling.CommandHangupLeg)
	staff, caller := call.staffLeg, call.callerLeg
	staff.ClientState, caller.ClientState = hangups[call.staffLeg.CallControlID], hangups[call.callerLeg.CallControlID]
	call.deliver(t,
		call.fact(staff, "staff", humancalling.FactCallHangup, 9000),
		call.fact(caller, "caller", humancalling.FactCallHangup, 9000),
	)
	call.settle(t)

	call.requireSettledOnFirstAttempt(t, nil)
	if state := call.callState(t); state != "NEEDS_DISPOSITION" {
		t.Fatalf("Call state = %s, want NEEDS_DISPOSITION", state)
	}
}

func TestReceiptReplayInboundStaffHangupDuringBridgeSettles(t *testing.T) {
	for _, kind := range []string{"staff_hangup", "cleanup_retry", "late_dial_cleanup", "transfer_terminal_cleanup"} {
		t.Run(kind, func(t *testing.T) {
			prefix := "replay-unrouted-" + strings.ReplaceAll(kind, "_", "-")
			call := answerInboundReplay(t, prefix)
			call.deliver(t, call.fact(call.staffLeg, "staff", humancalling.FactCallAnswered, 3000))
			call.settle(t)
			*call.current = call.now.Add(4 * time.Second)
			call.requestHangup(t)
			processAllCommands(t, call.worker)
			hangups := call.clientStates(humancalling.CommandHangupLeg)
			staff, caller := call.staffLeg, call.callerLeg
			staff.ClientState = withClientStateKind(t, hangups[call.staffLeg.CallControlID], kind)
			caller.ClientState = hangups[call.callerLeg.CallControlID]
			ringWindowEnded := call.fact(caller, "ring-window", humancalling.FactPlaybackEnded, 4300)
			ringWindowEnded.PlaybackStatus = "call_hangup"
			call.deliver(t,
				call.fact(staff, "staff", humancalling.FactCallBridged, 4200),
				call.fact(caller, "caller", humancalling.FactCallBridged, 4200),
				ringWindowEnded,
				call.fact(staff, "staff", humancalling.FactCallHangup, 4400),
				call.fact(caller, "caller", humancalling.FactCallHangup, 4400),
			)
			call.settle(t)

			obsolete := "FAILED/TERMINAL_OR_OBSOLETE_PROVIDER_FACT"
			call.requireSettledOnFirstAttempt(t, map[string]string{
				prefix + "-staff-call.bridged":              obsolete,
				prefix + "-caller-call.bridged":             obsolete,
				prefix + "-ring-window-call.playback.ended": obsolete,
			})
			if state := call.callState(t); state != "MISSED" {
				t.Fatalf("Call state = %s, want MISSED", state)
			}
		})
	}
}

func TestReceiptReplayUnrecognizedKindWaitsForNewerWorker(t *testing.T) {
	call := answerInboundReplay(t, "replay-newer-kind")
	call.deliver(t, call.fact(call.staffLeg, "staff", humancalling.FactCallAnswered, 3000))
	call.settle(t)
	bridgedStaff := call.staffLeg
	bridgedStaff.ClientState = withClientStateKind(t,
		call.clientStates(humancalling.CommandBridge)[call.staffLeg.CallControlID], "bridge_from_a_newer_release")
	call.deliver(t,
		call.fact(call.callerLeg, "caller", humancalling.FactCallBridged, 4000),
		call.fact(bridgedStaff, "staff", humancalling.FactCallBridged, 4000),
	)
	call.settle(t)
	waiting, attempts := call.receiptOutcome(t, "replay-newer-kind-staff-call.bridged")
	if waiting != "PENDING/WAITING_FOR_RELATED_FACT_SLOW_RETRY" {
		t.Fatalf("unrecognized kind receipt = %s after %d attempts, want a slow related-fact wait", waiting, attempts)
	}
	before := call.callState(t)

	humancalling.RouteClientStateKindLike(t, "bridge_from_a_newer_release", "bridge")
	*call.current = call.current.Add(time.Hour)
	call.settle(t)

	if outcome, _ := call.receiptOutcome(t, "replay-newer-kind-staff-call.bridged"); outcome != "APPLIED/" {
		t.Fatalf("receipt once its kind is routed = %s, want APPLIED", outcome)
	}
	if after := call.callState(t); before == after || after != "CONNECTED" {
		t.Fatalf("Call state = %s while waiting, %s once routed; want CONNECTED only once routed", before, after)
	}
}

func TestReceiptReplayOutboundRecordingErrorApplies(t *testing.T) {
	call := dialOutboundReplay(t, "replay-recording-error")
	call.deliver(t, call.fact(call.destinationLeg, "destination", humancalling.FactCallAnswered, 3800))
	bridgedDestination, bridgedStaff := call.destinationLeg, call.staffLeg
	bridgedDestination.ClientState, bridgedStaff.ClientState = "", ""
	call.deliver(t,
		call.fact(bridgedDestination, "destination", humancalling.FactCallBridged, 4000),
		call.fact(bridgedStaff, "staff", humancalling.FactCallBridged, 4000),
	)
	call.settle(t)

	call.deliver(t, call.fact(call.ringtone, "staff", humancalling.FactRecordingError, 5000))
	call.settle(t)

	call.requireSettledOnFirstAttempt(t, nil)
	read, err := call.portal.ReadCall(context.Background(), call.staff[0], call.callID(t))
	if err != nil {
		t.Fatal(err)
	}
	if read.Recording.AudioState != humancalling.RecordingUnavailable {
		t.Fatalf("recording = %s after outbound recording error, want UNAVAILABLE", read.Recording.AudioState)
	}
}

func TestStaleLegReconcilerSkipsObsoleteObservation(t *testing.T) {
	call := dialOutboundReplay(t, "replay-stale-obsolete")
	staff, _ := call.hangUp(t, 6000)
	if _, err := call.db.Owner.Exec(context.Background(), `
		UPDATE human_calling_call_legs SET updated_at = $2 WHERE provider_call_control_id = $1
	`, call.staffLeg.CallControlID, call.current.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	lateRingtone := call.fact(staff, "late-ringtone", humancalling.FactPlaybackStarted, 6500)
	call.provider.mu.Lock()
	call.provider.observations = append(call.provider.observations, humancalling.ProviderCallObservation{
		Active: true, CallControlID: staff.CallControlID, CallLegID: staff.CallLegID,
		CallSessionID: staff.CallSessionID, Events: []humancalling.ProviderFact{lateRingtone},
	})
	call.provider.mu.Unlock()

	if reconciled, err := call.worker.MaintainOutgoingCallLegs(context.Background()); err != nil || !reconciled {
		t.Fatalf("reconcile stale Staff leg = %t, %v", reconciled, err)
	}

	var legState, legCode string
	var quarantined int
	if err := call.db.Owner.QueryRow(context.Background(), `
		SELECT leg.state, COALESCE(leg.error_code, ''),
			(SELECT count(*) FROM human_calling_timeline timeline
				WHERE timeline.call_id = leg.call_id
					AND timeline.kind = 'call_leg.reconciliation_quarantined')
		FROM human_calling_call_legs leg
		WHERE leg.provider_call_control_id = $1
	`, call.staffLeg.CallControlID).Scan(&legState, &legCode, &quarantined); err != nil {
		t.Fatal(err)
	}
	if legState != "ENDING" || legCode != "" || quarantined != 0 {
		t.Fatalf("stale Staff leg after an obsolete observation = %s/%s quarantined:%d; want ENDING without quarantine",
			legState, legCode, quarantined)
	}
}

func TestReceiptReplayVoicemailRecordingAfterRoutingFailureApplies(t *testing.T) {
	call := answerInboundReplay(t, "replay-174")
	ringCompleted := call.fact(call.callerLeg, "ring-window", humancalling.FactPlaybackEnded, 20000)
	ringCompleted.PlaybackStatus = "completed"
	call.deliver(t, ringCompleted)
	call.settle(t)
	greeting := call.callerLeg
	greeting.ClientState = call.clientStates(humancalling.CommandSpeakVoicemail)[call.callerLeg.CallControlID]
	greetingCompleted := call.fact(greeting, "greeting", humancalling.FactSpeakEnded, 30000)
	greetingCompleted.PlaybackStatus = "completed"
	call.provider.mu.Lock()
	call.provider.actionErrors = map[humancalling.CommandAction][]error{
		humancalling.CommandStartVoicemailRecording: {fmt.Errorf(
			"%w: synthetic recording start rejection", humancalling.ErrDefinitiveProviderFailure,
		)},
	}
	call.provider.mu.Unlock()
	call.deliver(t, greetingCompleted)
	call.settle(t)
	if _, err := call.db.Owner.Exec(context.Background(), `
		UPDATE human_calling_provider_commands SET created_at = $1
		WHERE action = 'START_VOICEMAIL_RECORDING'
	`, greetingCompleted.OccurredAt); err != nil {
		t.Fatal(err)
	}
	routed := call.callState(t)
	routingFailure := call.callerLeg
	routingFailure.ClientState = call.clientStates(humancalling.CommandHangupLeg)[call.callerLeg.CallControlID]
	if routingFailure.ClientState == "" {
		t.Fatal("routing failure did not hang up the caller")
	}

	saved := call.fact(routingFailure, "caller", humancalling.FactRecordingSaved, 45000)
	saved.RecordingID = "replay-174-recording"
	saved.RecordingStartedAt, saved.RecordingEndedAt = call.now.Add(31*time.Second), call.now.Add(44*time.Second)
	call.deliver(t, saved, call.fact(routingFailure, "caller", humancalling.FactCallHangup, 46000))
	call.settle(t)

	call.requireSettledOnFirstAttempt(t, nil)
	if state := call.callState(t); routed != "UNANSWERED" || state != "VOICEMAIL" {
		t.Fatalf("Call state = %s after routing failure, %s after late recording; want UNANSWERED then VOICEMAIL",
			routed, state)
	}
}
