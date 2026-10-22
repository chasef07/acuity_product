package humancalling_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestCommandFailureAfterCallerHangupDuringGreetingKeepsMissedCall(t *testing.T) {
	call := admitInboundCall(t, "greeting-hangup")
	call.answerAndRing(t)
	call.completeRingWindow(t)
	call.failCommandAfterCallerHangup(t, humancalling.CommandSpeakVoicemail)

	read := call.read(t)
	if call.terminalOutcome(t) != "MISSED" || read.State != humancalling.CallMissed ||
		read.Voicemail.TaskID == "" || call.callerHangups() != 0 {
		t.Fatalf("Speak failure after caller hangup = outcome:%s state:%s task:%q caller hangups:%d",
			call.terminalOutcome(t), read.State, read.Voicemail.TaskID, call.callerHangups())
	}
}

func TestCommandFailureAfterCallerHangupBeforeAnswerKeepsAbandonedCall(t *testing.T) {
	call := admitInboundCall(t, "answer-hangup")
	call.failCommandAfterCallerHangup(t, humancalling.CommandAnswerCaller)

	read := call.read(t)
	if call.terminalOutcome(t) != "ABANDONED" || read.State != humancalling.CallMissed ||
		read.Voicemail.TaskID != "" || call.callerHangups() != 0 {
		t.Fatalf("Answer failure after caller hangup = outcome:%s state:%s task:%q caller hangups:%d",
			call.terminalOutcome(t), read.State, read.Voicemail.TaskID, call.callerHangups())
	}
}

func TestRecordingErrorAfterRoutingFailureKeepsRoutingFailure(t *testing.T) {
	call := admitInboundCall(t, "recording-error")
	call.answerAndRing(t)
	call.completeRingWindow(t)
	call.completeGreeting(t)
	call.provider.mu.Lock()
	call.provider.actionErrors = map[humancalling.CommandAction][]error{
		humancalling.CommandStartVoicemailRecording: {fmt.Errorf(
			"%w: synthetic recording start rejection", humancalling.ErrDefinitiveProviderFailure,
		)},
	}
	call.provider.mu.Unlock()
	processAllCommands(t, call.worker)
	routed := call.read(t)

	call.apply(t, humancalling.ProviderFact{
		EventID: "recording-error-after-routing-failure", Type: humancalling.FactRecordingError,
		OccurredAt: call.now.Add(40 * time.Second), ClientState: call.clientState(humancalling.CommandStartVoicemailRecording),
	})

	read := call.read(t)
	if call.terminalOutcome(t) != "ROUTING_FAILED" || routed.State != humancalling.CallUnanswered ||
		read.State != routed.State || read.Voicemail.TaskID == "" {
		t.Fatalf("recording error after routing failure = outcome:%s state:%s->%s task:%q",
			call.terminalOutcome(t), routed.State, read.State, read.Voicemail.TaskID)
	}
}

func TestGreetingFailureAfterVoicemailKeepsVoicemail(t *testing.T) {
	call := admitInboundCall(t, "late-greeting-failure")
	call.answerAndRing(t)
	call.completeRingWindow(t)
	call.completeGreeting(t)
	processAllCommands(t, call.worker)
	call.apply(t, humancalling.ProviderFact{
		EventID: "late-greeting-failure-saved", Type: humancalling.FactRecordingSaved,
		OccurredAt: call.now.Add(40 * time.Second), RecordingID: "late-greeting-failure-recording",
		ClientState:        call.clientState(humancalling.CommandStartVoicemailRecording),
		RecordingStartedAt: call.now, RecordingEndedAt: call.now.Add(10 * time.Second),
	})

	call.apply(t, humancalling.ProviderFact{
		EventID: "late-greeting-failure-ended", Type: humancalling.FactSpeakEnded,
		OccurredAt: call.now.Add(41 * time.Second), PlaybackStatus: "failed",
		ClientState: call.clientState(humancalling.CommandSpeakVoicemail),
	})

	read := call.read(t)
	if call.terminalOutcome(t) != "VOICEMAIL" || read.State != humancalling.CallVoicemail ||
		read.Voicemail.AudioState != humancalling.VoicemailReady {
		t.Fatalf("greeting failure after voicemail = outcome:%s state:%s audio:%s",
			call.terminalOutcome(t), read.State, read.Voicemail.AudioState)
	}
}

func TestStaleQuarantineKeepsEndedStaffLegEnded(t *testing.T) {
	call := admitInboundCall(t, "ended-leg-quarantine")
	call.answerAndRing(t)
	dial := call.provider.last(humancalling.CommandDialStaff)
	staffState, _ := dial.Payload["client_state"].(string)
	call.apply(t, humancalling.ProviderFact{
		EventID: "ended-leg-quarantine-staff-hangup", Type: humancalling.FactCallHangup,
		OccurredAt: call.now.Add(5 * time.Second), CallControlID: "ended-leg-quarantine-staff-control",
		CallLegID: "ended-leg-quarantine-staff-leg", CallSessionID: call.caller.CallSessionID,
		ClientState: staffState, HangupCause: "call_rejected",
	})
	if _, err := call.db.Owner.Exec(context.Background(), `
		UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1
	`, dial.CallLegID, call.now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	call.provider.mu.Lock()
	call.provider.observations = append(call.provider.observations, humancalling.ProviderCallObservation{
		CallControlID: "ended-leg-quarantine-staff-control", CallLegID: "ended-leg-quarantine-staff-leg",
		Events: []humancalling.ProviderFact{{
			EventID: "ended-leg-quarantine-contradiction", Type: humancalling.FactCallInitiated,
			OccurredAt: call.now, CallControlID: "ended-leg-quarantine-staff-control",
			CallLegID: "contradictory-provider-leg", CallSessionID: call.caller.CallSessionID,
			ClientState: staffState,
		}},
	})
	call.provider.mu.Unlock()

	if reconciled, err := call.worker.MaintainOutgoingCallLegs(context.Background()); err != nil || !reconciled {
		t.Fatalf("quarantine ended Staff leg = %t, %v", reconciled, err)
	}

	var legState, commandState, commandCode string
	if err := call.db.Owner.QueryRow(context.Background(), `
		SELECT leg.state, command.state, COALESCE(command.last_error_code, '')
		FROM human_calling_call_legs leg
		JOIN human_calling_provider_commands command ON command.id = $2
		WHERE leg.id = $1
	`, dial.CallLegID, dial.ID).Scan(&legState, &commandState, &commandCode); err != nil {
		t.Fatal(err)
	}
	read := call.read(t)
	if legState != "ENDED" || commandState != "FAILED" ||
		commandCode != "PROVIDER_OBSERVATION_CONFLICT" ||
		read.State != humancalling.CallUnanswered {
		t.Fatalf("ended Staff leg quarantine = leg:%s command:%s/%s Call:%s",
			legState, commandState, commandCode, read.State)
	}
	if reconciled, err := call.worker.MaintainOutgoingCallLegs(context.Background()); err != nil || reconciled {
		t.Fatalf("repeat ended Staff leg quarantine = %t, %v", reconciled, err)
	}
}

type inboundCall struct {
	db       *testdb.DB
	portal   *humancalling.Module
	worker   *humancalling.Module
	provider *recordingProvider
	caller   humancalling.ProviderFact
	staff    access.Identity
	now      time.Time
}

func admitInboundCall(t *testing.T, prefix string) inboundCall {
	t.Helper()
	now := time.Date(2026, time.October, 21, 15, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	db := testdb.Open(t)
	portalAccess := access.New(db.As(testdb.Portal), clock)
	authorization, staff := provisionConcurrentStaff(t, db.Owner, portalAccess, now, prefix, 1)
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-leg",
	}}}
	config := humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com", StaffSIPDomain: "sip.telnyx.com",
		RingWindowDuration: 20 * time.Second, HandoffTokenKey: []byte("0123456789abcdef0123456789abcdef"),
		CallControlID: "staff-call-control-connection", CredentialConnectionID: "staff-credential-connection",
		FromNumber: "+14843336938", RingbackURL: "https://media.synthetic.test/ringback.wav",
	}
	portal := humancalling.New(db.As(testdb.Portal), portalAccess, provider, config, clock)
	worker := humancalling.New(db.As(testdb.Worker), access.New(db.As(testdb.Worker), clock), provider, config, clock)
	prepareCredentials(t, worker)
	readyConcurrentStaff(t, portal, staff, prefix+"-browser")
	if _, err := portal.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service:    humancalling.ServiceIdentity{Subject: "abita-" + prefix, PracticeID: authorization.Practice.ID},
		LocationID: authorization.Locations[0].ID, SourceCallID: prefix + "-source",
		IdempotencyKey: prefix + "-handoff", Contact: humancalling.ContactContext{Phone: "+15555550100"},
	}); err != nil {
		t.Fatal(err)
	}
	call := inboundCall{
		db: db, portal: portal, worker: worker, provider: provider, staff: staff[0], now: now,
		caller: humancalling.ProviderFact{
			EventID: prefix + "-caller-initiated", Type: humancalling.FactCallInitiated,
			OccurredAt: now, ConnectionID: "staff-call-control-connection",
			CallControlID: prefix + "-caller-control", CallLegID: prefix + "-caller-leg",
			CallSessionID: prefix + "-caller-session", From: "+15555550100", To: "+14843989071",
		},
	}
	call.apply(t, call.caller)
	return call
}

func (call inboundCall) apply(t *testing.T, fact humancalling.ProviderFact) {
	t.Helper()
	if fact.CallControlID == "" {
		fact.CallControlID = call.caller.CallControlID
		fact.CallLegID = call.caller.CallLegID
		fact.CallSessionID = call.caller.CallSessionID
	}
	if err := call.worker.ApplyProviderFact(context.Background(), fact); err != nil {
		t.Fatalf("apply %s: %v", fact.EventID, err)
	}
}

func (call inboundCall) answerAndRing(t *testing.T) {
	t.Helper()
	processAllCommands(t, call.worker)
	answered := call.caller
	answered.EventID = call.caller.EventID + "-answered"
	answered.Type = humancalling.FactCallAnswered
	answered.OccurredAt = call.now.Add(time.Second)
	call.apply(t, answered)
	processAllCommands(t, call.worker)
}

func (call inboundCall) completeRingWindow(t *testing.T) {
	t.Helper()
	call.apply(t, humancalling.ProviderFact{
		EventID: call.caller.EventID + "-ring-completed", Type: humancalling.FactPlaybackEnded,
		OccurredAt: call.now.Add(20 * time.Second), PlaybackStatus: "completed",
		ClientState: call.clientState(humancalling.CommandStartRingWindow),
	})
}

func (call inboundCall) completeGreeting(t *testing.T) {
	t.Helper()
	processAllCommands(t, call.worker)
	call.apply(t, humancalling.ProviderFact{
		EventID: call.caller.EventID + "-greeting-completed", Type: humancalling.FactSpeakEnded,
		OccurredAt: call.now.Add(30 * time.Second), PlaybackStatus: "completed",
		ClientState: call.clientState(humancalling.CommandSpeakVoicemail),
	})
}

func (call inboundCall) failCommandAfterCallerHangup(t *testing.T, action humancalling.CommandAction) {
	t.Helper()
	call.provider.mu.Lock()
	call.provider.blockAction = action
	call.provider.blockStarted = make(chan struct{})
	call.provider.blockRelease = make(chan struct{})
	call.provider.blockError = humancalling.ErrProviderTargetAbsent
	started, release := call.provider.blockStarted, call.provider.blockRelease
	call.provider.mu.Unlock()
	processed := make(chan error, 1)
	go func() {
		for {
			ok, err := call.worker.ProcessNextCommand(context.Background())
			if err != nil || !ok {
				processed <- err
				return
			}
		}
	}()
	select {
	case <-started:
	case err := <-processed:
		t.Fatalf("%s was never sent: %v", action, err)
	}
	hangupErr := call.worker.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: call.caller.EventID + "-hangup", Type: humancalling.FactCallHangup,
		OccurredAt: call.now.Add(25 * time.Second), CallControlID: call.caller.CallControlID,
		CallLegID: call.caller.CallLegID, CallSessionID: call.caller.CallSessionID,
		HangupCause: "normal_clearing",
	})
	close(release)
	if err := <-processed; err != nil {
		t.Fatalf("finish %s: %v", action, err)
	}
	if hangupErr != nil {
		t.Fatalf("apply caller hangup: %v", hangupErr)
	}
}

func (call inboundCall) clientState(action humancalling.CommandAction) string {
	state, _ := call.provider.last(action).Payload["client_state"].(string)
	return state
}

func (call inboundCall) callerHangups() int {
	hangups := 0
	for _, command := range call.provider.all(humancalling.CommandHangupLeg) {
		if command.TargetID == call.caller.CallControlID {
			hangups++
		}
	}
	return hangups
}

func (call inboundCall) terminalOutcome(t *testing.T) string {
	t.Helper()
	var outcome string
	if err := call.db.Owner.QueryRow(context.Background(), `
		SELECT COALESCE(terminal_outcome, '') FROM human_calling_calls
	`).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	return outcome
}

func (call inboundCall) read(t *testing.T) humancalling.Call {
	t.Helper()
	var callID string
	if err := call.db.Owner.QueryRow(context.Background(), `
		SELECT id::text FROM human_calling_calls
	`).Scan(&callID); err != nil {
		t.Fatal(err)
	}
	read, err := call.portal.ReadCall(context.Background(), call.staff, callID)
	if err != nil {
		t.Fatalf("read Call: %v", err)
	}
	return read
}
