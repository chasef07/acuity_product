package humancalling_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgconn"
)

type callVersionFixture struct {
	db            *testdb.DB
	portal        *humancalling.Module
	worker        *humancalling.Module
	provider      *recordingProvider
	authorization access.Authorization
	staff         []access.Identity
	prefix        string
	now           time.Time
	current       *time.Time
}

func newCallVersionFixture(
	t *testing.T,
	prefix string,
	staffCount int,
	provider *recordingProvider,
) callVersionFixture {
	t.Helper()
	db := testdb.Open(t)
	now := time.Date(2026, time.October, 21, 15, 0, 0, 0, time.UTC)
	current := now
	clock := func() time.Time { return current }
	accessModule := access.New(db.As(testdb.Portal), clock)
	authorization, staff := provisionConcurrentStaff(t, db.Owner, accessModule, now, prefix, staffCount)
	config := humancalling.Config{
		HandoffSIPDomain:       "synthetic.sip.telnyx.com",
		StaffSIPDomain:         "sip.telnyx.com",
		RingWindowDuration:     20 * time.Second,
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
		FromNumber:             "+14843336938",
		RingbackURL:            "https://media.synthetic.test/ringback.wav",
	}
	portal := humancalling.New(db.As(testdb.Portal), accessModule, provider, config, clock)
	worker := humancalling.New(db.As(testdb.Worker), accessModule, provider, config, clock)
	prepareCredentials(t, worker)
	readyConcurrentStaff(t, portal, staff, prefix+"-browser")
	if err := humancalling.New(db.Owner, accessModule, provider, config, clock).ProvisionLocationVoices(
		context.Background(), []humancalling.LocationVoiceProvision{{
			PracticeKey: prefix + "-practice", LocationKey: prefix + "-location",
			Number: "+14843336938", Enabled: true,
		}},
	); err != nil {
		t.Fatalf("provision outbound caller ID: %v", err)
	}
	return callVersionFixture{
		db: db, portal: portal, worker: worker, provider: provider,
		authorization: authorization, staff: staff, prefix: prefix, now: now,
		current: &current,
	}
}

func (fixture callVersionFixture) at(seconds int) time.Time {
	return fixture.now.Add(time.Duration(seconds) * time.Second)
}

func (fixture callVersionFixture) session(index int) string {
	return fmt.Sprintf("%s-browser-%d", fixture.prefix, index+1)
}

func (fixture callVersionFixture) apply(t *testing.T, fact humancalling.ProviderFact) {
	t.Helper()
	*fixture.current = fact.OccurredAt
	if err := fixture.worker.ApplyProviderFact(context.Background(), fact); err != nil {
		t.Fatalf("apply %s: %v", fact.EventID, err)
	}
}

func (fixture callVersionFixture) callID(t *testing.T) string {
	t.Helper()
	var callID string
	if err := fixture.db.Owner.QueryRow(context.Background(), `
		SELECT id::text FROM human_calling_calls ORDER BY created_at DESC, id DESC LIMIT 1
	`).Scan(&callID); err != nil {
		t.Fatalf("read Call: %v", err)
	}
	return callID
}

func (fixture callVersionFixture) callVersion(t *testing.T) int64 {
	t.Helper()
	var version int64
	if err := fixture.db.Owner.QueryRow(context.Background(), `
		SELECT COALESCE((
			SELECT version FROM human_calling_calls ORDER BY created_at DESC, id DESC LIMIT 1
		), 0)
	`).Scan(&version); err != nil {
		t.Fatalf("read Call version: %v", err)
	}
	return version
}

func (fixture callVersionFixture) startOutbound(t *testing.T) {
	t.Helper()
	if _, err := fixture.portal.StartOutboundCall(context.Background(), humancalling.StartOutboundCallCommand{
		Identity: fixture.staff[0], SessionID: fixture.session(0),
		IdempotencyKey: fixture.prefix + "-outbound",
		PracticeID:     fixture.authorization.Practice.ID,
		LocationID:     fixture.authorization.Locations[0].ID,
		Destination:    "+15555550123",
	}); err != nil {
		t.Fatalf("start outbound Call: %v", err)
	}
}

func (fixture callVersionFixture) requestHangup(t *testing.T) {
	t.Helper()
	if _, err := fixture.portal.RequestHangup(
		context.Background(), fixture.staff[0], fixture.session(0), fixture.callID(t),
	); err != nil {
		t.Fatalf("request Hangup: %v", err)
	}
}

func (fixture callVersionFixture) recordDisposition(t *testing.T) {
	t.Helper()
	if _, err := fixture.portal.RecordDisposition(
		context.Background(), fixture.staff[0], fixture.session(0), fixture.callID(t),
		humancalling.DispositionResolved,
	); err != nil {
		t.Fatalf("record disposition: %v", err)
	}
}

func (fixture callVersionFixture) requestTransfer(t *testing.T) (humancalling.StaffTransfer, error) {
	t.Helper()
	call, err := fixture.portal.ReadCall(context.Background(), fixture.staff[0], fixture.callID(t))
	if err != nil {
		t.Fatalf("read transfer source Call: %v", err)
	}
	return fixture.portal.RequestStaffTransfer(context.Background(), humancalling.RequestStaffTransferCommand{
		Identity: fixture.staff[0], CallID: call.ID, SessionID: fixture.session(0),
		RecipientSubject: fixture.staff[1].Subject, IdempotencyKey: fixture.prefix + "-transfer",
		HandoffNote: "Synthetic handoff note", ExpectedVersion: call.Version,
	})
}

type versionedInboundCall struct {
	callVersionFixture
	caller    humancalling.ProviderFact
	staffLegs []humancalling.ProviderFact
}

func newInboundCall(t *testing.T, prefix string) *versionedInboundCall {
	t.Helper()
	fixture := newCallVersionFixture(t, prefix, 2, &recordingProvider{
		dialResults: []humancalling.ProviderResult{
			{CallControlID: prefix + "-staff-control-1", CallLegID: prefix + "-staff-leg-1"},
			{CallControlID: prefix + "-staff-control-2", CallLegID: prefix + "-staff-leg-2"},
		},
	})
	handoff, err := fixture.portal.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject: "abita-" + prefix, PracticeID: fixture.authorization.Practice.ID,
		},
		LocationID: fixture.authorization.Locations[0].ID, SourceCallID: prefix + "-source",
		IdempotencyKey: prefix + "-handoff",
		Contact:        humancalling.ContactContext{Phone: "+15555550100"},
	})
	if err != nil {
		t.Fatalf("create handoff: %v", err)
	}
	return &versionedInboundCall{
		callVersionFixture: fixture,
		caller: humancalling.ProviderFact{
			Type: humancalling.FactCallInitiated, OccurredAt: fixture.now,
			ConnectionID: "staff-call-control-connection", CallControlID: prefix + "-caller-control",
			CallLegID: prefix + "-caller-leg", CallSessionID: prefix + "-caller-session",
			From: "+15555550100", To: handoff.SIPDestination,
		},
		staffLegs: make([]humancalling.ProviderFact, 2),
	}
}

func (call *versionedInboundCall) callerFact(kind humancalling.FactType, seconds int) humancalling.ProviderFact {
	fact := call.caller
	fact.EventID = call.prefix + "-caller-" + string(kind)
	fact.Type = kind
	fact.OccurredAt = call.at(seconds)
	return fact
}

func (call *versionedInboundCall) staffFact(index int, kind humancalling.FactType, seconds int) humancalling.ProviderFact {
	fact := call.staffLegs[index]
	fact.EventID = fmt.Sprintf("%s-staff-%d-%s", call.prefix, index+1, kind)
	fact.Type = kind
	fact.OccurredAt = call.at(seconds)
	if kind == humancalling.FactCallBridged {
		fact.ClientState = ""
	}
	if kind == humancalling.FactCallHangup {
		fact.HangupCause, fact.TerminationSource = "NORMAL_CLEARING", "CALL_CONTROL"
	}
	return fact
}

func (call *versionedInboundCall) dialStaff(t *testing.T) {
	t.Helper()
	processAllCommands(t, call.worker)
	for dialIndex, dial := range call.provider.all(humancalling.CommandDialStaff) {
		var subject string
		if err := call.db.Owner.QueryRow(context.Background(), `
			SELECT staff_subject FROM human_calling_call_legs WHERE id = $1
		`, dial.CallLegID).Scan(&subject); err != nil {
			t.Fatalf("read dialed Staff: %v", err)
		}
		clientState, _ := dial.Payload["client_state"].(string)
		for staffIndex, identity := range call.staff {
			if identity.Subject == subject {
				call.staffLegs[staffIndex] = humancalling.ProviderFact{
					ConnectionID:  "staff-call-control-connection",
					CallControlID: fmt.Sprintf("%s-staff-control-%d", call.prefix, dialIndex+1),
					CallLegID:     fmt.Sprintf("%s-staff-leg-%d", call.prefix, dialIndex+1),
					CallSessionID: fmt.Sprintf("%s-staff-session-%d", call.prefix, dialIndex+1),
					ClientState:   clientState,
				}
			}
		}
	}
}

func (call *versionedInboundCall) completeRingWindow(t *testing.T) {
	t.Helper()
	ring := call.provider.last(humancalling.CommandStartRingWindow)
	clientState, _ := ring.Payload["client_state"].(string)
	fact := call.callerFact(humancalling.FactPlaybackEnded, 4)
	fact.ClientState = clientState
	fact.PlaybackStatus = "completed"
	call.apply(t, fact)
}

func (call *versionedInboundCall) steps() []hintStep {
	return []hintStep{
		{"admit caller", true, func(t *testing.T) {
			call.apply(t, call.callerFact(humancalling.FactCallInitiated, 0))
		}},
		{"answer caller command", false, func(t *testing.T) { processAllCommands(t, call.worker) }},
		{"caller answered fan-out", true, func(t *testing.T) {
			call.apply(t, call.callerFact(humancalling.FactCallAnswered, 1))
		}},
		{"ring window and Staff dials", true, call.dialStaff},
		{"Staff 1 ringing", true, func(t *testing.T) {
			call.apply(t, call.staffFact(0, humancalling.FactCallInitiated, 2))
		}},
		{"Staff 2 ringing", true, func(t *testing.T) {
			call.apply(t, call.staffFact(1, humancalling.FactCallInitiated, 2))
		}},
		{"Staff 1 answered", true, func(t *testing.T) {
			call.apply(t, call.staffFact(0, humancalling.FactCallAnswered, 3))
		}},
		{"ring window completed", false, call.completeRingWindow},
		{"Bridge and loser cleanup commands", false, func(t *testing.T) { processAllCommands(t, call.worker) }},
		{"Staff 1 bridged", true, func(t *testing.T) {
			call.apply(t, call.staffFact(0, humancalling.FactCallBridged, 5))
		}},
		{"caller bridged", true, func(t *testing.T) {
			call.apply(t, call.callerFact(humancalling.FactCallBridged, 5))
		}},
		{"stop ring window command", false, func(t *testing.T) { processAllCommands(t, call.worker) }},
		{"Staff 2 cleanup hangup", true, func(t *testing.T) {
			call.apply(t, call.staffFact(1, humancalling.FactCallHangup, 6))
		}},
		{"Staff transfer requested", true, func(t *testing.T) {
			if _, err := call.requestTransfer(t); err != nil {
				t.Fatalf("request Staff transfer: %v", err)
			}
		}},
		{"Staff transfer canceled", true, func(t *testing.T) {
			var transferID string
			if err := call.db.Owner.QueryRow(context.Background(), `
				SELECT id::text FROM human_calling_staff_transfers
			`).Scan(&transferID); err != nil {
				t.Fatal(err)
			}
			if _, err := call.portal.CancelStaffTransfer(context.Background(), humancalling.RespondStaffTransferCommand{
				Identity: call.staff[0], TransferID: transferID, SessionID: call.session(0),
			}); err != nil {
				t.Fatalf("cancel Staff transfer: %v", err)
			}
		}},
		{"transfer cleanup commands", false, func(t *testing.T) { processAllCommands(t, call.worker) }},
		{"Hangup requested", true, call.requestHangup},
		{"Hangup commands", false, func(t *testing.T) { processAllCommands(t, call.worker) }},
		{"Staff 1 hung up", true, func(t *testing.T) {
			call.apply(t, call.staffFact(0, humancalling.FactCallHangup, 8))
		}},
		{"caller cleanup commands", false, func(t *testing.T) { processAllCommands(t, call.worker) }},
		{"disposition recorded", true, call.recordDisposition},
	}
}

type hintStep struct {
	name   string
	writer bool
	run    func(t *testing.T)
}

func runHintSteps(t *testing.T, fixture callVersionFixture, steps []hintStep) {
	t.Helper()
	listener := listenForHints(t, fixture.db.Owner)
	drainHints(t, listener)
	totalCalling, totalWorkspace := 0, 0
	for _, step := range steps {
		before := fixture.callVersion(t)
		step.run(t)
		hints := drainHints(t, listener)
		calling := callingHintsFor(t, hints, fixture.authorization.Practice.ID)
		workspace := workspaceHints(hints)
		after := fixture.callVersion(t)
		totalCalling += calling
		totalWorkspace += workspace
		t.Logf("%-34s version %2d -> %2d, calling hints %d, workspace hints %d",
			step.name, before, after, calling, workspace)
		if step.writer && (after <= before || calling == 0) {
			t.Errorf("%s: Call version %d -> %d with %d calling hints, want a new version and a calling hint",
				step.name, before, after, calling)
		}
	}
	t.Logf("flow total: %d calling hints, %d workspace hints", totalCalling, totalWorkspace)
}

func TestRequestHangupPublishesOneCallingHint(t *testing.T) {
	fixture := newCallVersionFixture(t, "hangup-hint", 1, &recordingProvider{
		dialResults: []humancalling.ProviderResult{{
			CallControlID: "hangup-hint-staff-control",
			CallLegID:     "hangup-hint-staff-leg",
		}},
	})
	fixture.startOutbound(t)
	processAllCommands(t, fixture.worker)
	listener := listenForHints(t, fixture.db.Owner)
	drainHints(t, listener)
	before := fixture.callVersion(t)

	fixture.requestHangup(t)

	hints := drainHints(t, listener)
	if calling := callingHintsFor(t, hints, fixture.authorization.Practice.ID); calling != 1 {
		t.Fatalf("Hangup Calling hints = %d (%#v), want exactly 1", calling, hints)
	}
	if after := fixture.callVersion(t); after <= before {
		t.Fatalf("Hangup Call version = %d, want above %d", after, before)
	}
}

func TestInboundCallWritersAdvanceTheVersionAndHint(t *testing.T) {
	call := newInboundCall(t, "inbound-hints")
	runHintSteps(t, call.callVersionFixture, call.steps())
}

func TestOutboundCallWritersAdvanceTheVersionAndHint(t *testing.T) {
	const prefix = "outbound-hints"
	fixture := newCallVersionFixture(t, prefix, 1, &recordingProvider{
		dialResults: []humancalling.ProviderResult{
			{CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-leg"},
			{CallControlID: prefix + "-destination-control", CallLegID: prefix + "-destination-leg"},
		},
	})
	staff := humancalling.ProviderFact{
		ConnectionID: "staff-call-control-connection", CallControlID: prefix + "-staff-control",
		CallLegID: prefix + "-staff-leg", CallSessionID: prefix + "-staff-session",
	}
	destination := humancalling.ProviderFact{
		ConnectionID: "staff-call-control-connection", CallControlID: prefix + "-destination-control",
		CallLegID: prefix + "-destination-leg", CallSessionID: prefix + "-destination-session",
	}
	fact := func(base humancalling.ProviderFact, name string, kind humancalling.FactType, seconds int) humancalling.ProviderFact {
		base.EventID = prefix + "-" + name + "-" + string(kind)
		base.Type = kind
		base.OccurredAt = fixture.at(seconds)
		if kind == humancalling.FactCallHangup {
			base.HangupCause, base.TerminationSource = "NORMAL_CLEARING", "CALL_CONTROL"
		}
		return base
	}
	runHintSteps(t, fixture, []hintStep{
		{"outbound started", true, fixture.startOutbound},
		{"Staff dial accepted", true, func(t *testing.T) {
			processAllCommands(t, fixture.worker)
			staff.ClientState, _ = fixture.provider.last(humancalling.CommandDialOutboundStaff).Payload["client_state"].(string)
		}},
		{"Staff ringing", true, func(t *testing.T) {
			fixture.apply(t, fact(staff, "staff", humancalling.FactCallInitiated, 1))
		}},
		{"Staff answered", true, func(t *testing.T) {
			fixture.apply(t, fact(staff, "staff", humancalling.FactCallAnswered, 2))
		}},
		{"media confirmed", true, func(t *testing.T) {
			state, err := fixture.portal.ReadCallingState(context.Background(), fixture.staff[0])
			if err != nil || len(state.Ringing) != 1 {
				t.Fatalf("outbound media offer = %#v, %v", state.Ringing, err)
			}
			if _, err := fixture.portal.ConfirmOutboundMedia(context.Background(), humancalling.ConfirmOutboundMediaCommand{
				Identity: fixture.staff[0], SessionID: fixture.session(0),
				CallID: state.Ringing[0].CallID, MediaToken: state.Ringing[0].MediaToken,
			}); err != nil {
				t.Fatalf("confirm outbound media: %v", err)
			}
		}},
		{"destination dialed", true, func(t *testing.T) {
			processAllCommands(t, fixture.worker)
			destination.ClientState, _ = fixture.provider.last(humancalling.CommandDialOutboundDestination).Payload["client_state"].(string)
		}},
		{"destination ringing", true, func(t *testing.T) {
			fixture.apply(t, fact(destination, "destination", humancalling.FactCallInitiated, 3))
		}},
		{"destination answered", true, func(t *testing.T) {
			fixture.apply(t, fact(destination, "destination", humancalling.FactCallAnswered, 4))
		}},
		{"Bridge command", false, func(t *testing.T) { processAllCommands(t, fixture.worker) }},
		{"destination bridged", true, func(t *testing.T) {
			bridged := fact(destination, "destination", humancalling.FactCallBridged, 5)
			bridged.ClientState = ""
			fixture.apply(t, bridged)
		}},
		{"Staff bridged", true, func(t *testing.T) {
			bridged := fact(staff, "staff", humancalling.FactCallBridged, 5)
			bridged.ClientState = ""
			fixture.apply(t, bridged)
		}},
		{"Hangup requested", true, fixture.requestHangup},
		{"Hangup commands", false, func(t *testing.T) { processAllCommands(t, fixture.worker) }},
		{"destination hung up", true, func(t *testing.T) {
			fixture.apply(t, fact(destination, "destination", humancalling.FactCallHangup, 6))
		}},
		{"cleanup commands", false, func(t *testing.T) { processAllCommands(t, fixture.worker) }},
		{"disposition recorded", true, fixture.recordDisposition},
	})
}

func pauseCallWriter(
	t *testing.T,
	fixture callVersionFixture,
	table string,
	condition string,
	key int64,
) *postgresAdvisoryBarrier {
	t.Helper()
	installPostgresTestTrigger(t, fixture.db.Owner, fmt.Sprintf(`
		CREATE FUNCTION test_pause_call_writer() RETURNS trigger LANGUAGE plpgsql AS $function$
		BEGIN
			PERFORM pg_advisory_xact_lock(%d);
			RETURN NULL;
		END
		$function$;
		CREATE TRIGGER test_pause_call_writer
		AFTER INSERT OR UPDATE ON %s
		FOR EACH ROW WHEN (%s)
		EXECUTE FUNCTION test_pause_call_writer()
	`, key, table, condition), fmt.Sprintf(`
		DROP TRIGGER IF EXISTS test_pause_call_writer ON %s;
		DROP FUNCTION IF EXISTS test_pause_call_writer()
	`, table))
	barrier := holdPostgresAdvisoryLock(t, fixture.db.Owner, key)
	t.Cleanup(barrier.close)
	return barrier
}

func runPausedCallWriters(
	t *testing.T,
	barrier *postgresAdvisoryBarrier,
	first func() error,
	second func() error,
) (error, error) {
	t.Helper()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- first() }()
	firstPID := waitForPostgresLockWaiter(t, barrier.connection, "advisory", barrier.pid)
	go func() { secondDone <- second() }()
	waitForPostgresLockWaiter(t, barrier.connection, "transactionid", firstPID)
	barrier.release()
	results := make([]error, 2)
	for index, done := range []chan error{firstDone, secondDone} {
		select {
		case results[index] = <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("Call writer %d did not finish", index+1)
		}
		var postgresErr *pgconn.PgError
		if errors.As(results[index], &postgresErr) && postgresErr.Code == "40P01" {
			t.Fatalf("Call writer %d deadlocked: %v", index+1, results[index])
		}
	}
	return results[0], results[1]
}

func TestConcurrentCallWritersQueueOnTheCallLock(t *testing.T) {
	bridgeReady := func(t *testing.T, prefix string) *versionedInboundCall {
		call := newInboundCall(t, prefix)
		for _, step := range call.steps()[:9] {
			step.run(t)
		}
		return call
	}
	connected := func(t *testing.T, prefix string) *versionedInboundCall {
		call := newInboundCall(t, prefix)
		for _, step := range call.steps()[:13] {
			step.run(t)
		}
		return call
	}
	bridgeWinner := func(t *testing.T, call *versionedInboundCall) func() error {
		fact := call.staffFact(0, humancalling.FactCallBridged, 5)
		return func() error { return call.worker.ApplyProviderFact(context.Background(), fact) }
	}
	hangup := func(t *testing.T, call *versionedInboundCall) func() error {
		callID := call.callID(t)
		return func() error {
			_, err := call.portal.RequestHangup(context.Background(), call.staff[0], call.session(0), callID)
			return err
		}
	}
	callerHangup := func(t *testing.T, call *versionedInboundCall) func() error {
		fact := call.callerFact(humancalling.FactCallHangup, 7)
		fact.HangupCause, fact.TerminationSource = "NORMAL_CLEARING", "CALLER"
		return func() error { return call.worker.ApplyProviderFact(context.Background(), fact) }
	}
	transfer := func(t *testing.T, call *versionedInboundCall) func() error {
		return func() error {
			_, err := call.requestTransfer(t)
			return err
		}
	}

	t.Run("bridge winner then hangup", func(t *testing.T) {
		call := bridgeReady(t, "lock-bridge-hangup")
		barrier := pauseCallWriter(t, call.callVersionFixture, "human_calling_call_legs",
			"NEW.role = 'STAFF' AND NEW.state = 'BRIDGED'", 610601)
		bridgeErr, hangupErr := runPausedCallWriters(t, barrier, bridgeWinner(t, call), hangup(t, call))
		if bridgeErr != nil || hangupErr != nil {
			t.Fatalf("bridge then hangup = %v, %v", bridgeErr, hangupErr)
		}
	})
	t.Run("hangup then bridge winner", func(t *testing.T) {
		call := bridgeReady(t, "lock-hangup-bridge")
		barrier := pauseCallWriter(t, call.callVersionFixture, "human_calling_call_legs",
			"NEW.role = 'STAFF' AND NEW.state = 'ENDING'", 610602)
		hangupErr, _ := runPausedCallWriters(t, barrier, hangup(t, call), bridgeWinner(t, call))
		if hangupErr != nil {
			t.Fatalf("hangup before bridge = %v", hangupErr)
		}
	})
	t.Run("staff transfer then hangup", func(t *testing.T) {
		call := connected(t, "lock-transfer-hangup")
		barrier := pauseCallWriter(t, call.callVersionFixture, "human_calling_staff_transfers",
			"NEW.state = 'REQUESTED'", 610603)
		transferErr, hangupErr := runPausedCallWriters(t, barrier, transfer(t, call), callerHangup(t, call))
		if transferErr != nil || hangupErr != nil {
			t.Fatalf("transfer then hangup = %v, %v", transferErr, hangupErr)
		}
	})
	t.Run("hangup then staff transfer", func(t *testing.T) {
		call := connected(t, "lock-hangup-transfer")
		barrier := pauseCallWriter(t, call.callVersionFixture, "human_calling_call_legs",
			"NEW.role = 'CALLER' AND NEW.state = 'ENDED'", 610604)
		hangupErr, transferErr := runPausedCallWriters(t, barrier, callerHangup(t, call), transfer(t, call))
		if hangupErr != nil || (transferErr != nil && !errors.Is(transferErr, humancalling.ErrConflict)) {
			t.Fatalf("hangup then transfer = %v, %v", hangupErr, transferErr)
		}
	})
}
