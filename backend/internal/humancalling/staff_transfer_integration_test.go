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
	"github.com/jackc/pgx/v5/pgxpool"
)

type connectedStaffTransferFixture struct {
	pool      *pgxpool.Pool
	calling   *humancalling.Module
	provider  *recordingProvider
	staff     []access.Identity
	callID    string
	sourceLeg string
	caller    humancalling.ProviderFact
	now       time.Time
}

func prepareConnectedOutboundStaffTransfer(t *testing.T, prefix string) connectedStaffTransferFixture {
	t.Helper()
	now := time.Date(2026, time.August, 26, 15, 0, 0, 0, time.UTC)
	pool := testdb.Open(t)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, staff := provisionConcurrentStaff(t, accessModule, now, prefix, 2)
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{
		{CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-provider-leg"},
		{CallControlID: prefix + "-destination-control", CallLegID: prefix + "-destination-provider-leg"},
	}}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		StaffSIPDomain: "sip.telnyx.com", RingWindowDuration: 20 * time.Second,
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	readyConcurrentStaff(t, calling, staff, prefix+"-browser")
	if err := calling.ProvisionLocationVoices(context.Background(), []humancalling.LocationVoiceProvision{{
		PracticeKey: prefix + "-practice", LocationKey: prefix + "-location",
		Number: "+14843336938", Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	call, err := calling.StartOutboundCall(context.Background(), humancalling.StartOutboundCallCommand{
		Identity: staff[0], SessionID: prefix + "-browser-1", IdempotencyKey: prefix,
		PracticeID: authorization.Practice.ID, LocationID: authorization.Locations[0].ID,
		Destination: "+15555550123",
	})
	if err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	staffDial := provider.last(humancalling.CommandDialOutboundStaff)
	staffClientState, _ := staffDial.Payload["client_state"].(string)
	staffFact := humancalling.ProviderFact{
		EventID: prefix + "-staff-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-provider-leg",
		CallSessionID: prefix + "-staff-session", ClientState: staffClientState,
	}
	if err := calling.ApplyProviderFact(context.Background(), staffFact); err != nil {
		t.Fatal(err)
	}
	staffFact.EventID = prefix + "-staff-answered"
	staffFact.Type = humancalling.FactCallAnswered
	staffFact.OccurredAt = now.Add(2 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), staffFact); err != nil {
		t.Fatal(err)
	}
	callingState, err := calling.ReadCallingState(context.Background(), staff[0])
	if err != nil || len(callingState.Ringing) != 1 {
		t.Fatalf("outbound media offer = %#v, %v", callingState, err)
	}
	if _, err := calling.ConfirmOutboundMedia(context.Background(), humancalling.ConfirmOutboundMediaCommand{
		Identity: staff[0], SessionID: prefix + "-browser-1", CallID: call.ID,
		MediaToken: callingState.Ringing[0].MediaToken,
	}); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	destinationDial := provider.last(humancalling.CommandDialOutboundDestination)
	destinationClientState, _ := destinationDial.Payload["client_state"].(string)
	destination := humancalling.ProviderFact{
		EventID: prefix + "-destination-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(3 * time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: prefix + "-destination-control", CallLegID: prefix + "-destination-provider-leg",
		CallSessionID: prefix + "-destination-session", ClientState: destinationClientState,
	}
	if err := calling.ApplyProviderFact(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	destination.EventID = prefix + "-destination-answered"
	destination.Type = humancalling.FactCallAnswered
	destination.OccurredAt = now.Add(4 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	for _, fact := range []humancalling.ProviderFact{
		{EventID: prefix + "-destination-bridged", Type: humancalling.FactCallBridged,
			OccurredAt: now.Add(5 * time.Second), CallControlID: destination.CallControlID,
			CallLegID: destination.CallLegID, CallSessionID: destination.CallSessionID},
		{EventID: prefix + "-staff-bridged", Type: humancalling.FactCallBridged,
			OccurredAt: now.Add(5 * time.Second), CallControlID: staffFact.CallControlID,
			CallLegID: staffFact.CallLegID, CallSessionID: staffFact.CallSessionID},
	} {
		if err := calling.ApplyProviderFact(context.Background(), fact); err != nil {
			t.Fatal(err)
		}
	}
	var sourceLeg string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text FROM human_calling_call_legs
		WHERE call_id = $1 AND role = 'STAFF' AND staff_subject = $2
	`, call.ID, staff[0].Subject).Scan(&sourceLeg); err != nil {
		t.Fatal(err)
	}
	return connectedStaffTransferFixture{
		pool: pool, calling: calling, provider: provider, staff: staff,
		callID: call.ID, sourceLeg: sourceLeg, caller: destination, now: now,
	}
}

func prepareConnectedStaffTransfer(t *testing.T, prefix string) connectedStaffTransferFixture {
	return prepareConnectedStaffTransferWithCount(t, prefix, 2)
}

func prepareConnectedStaffTransferWithCount(
	t *testing.T,
	prefix string,
	staffCount int,
) connectedStaffTransferFixture {
	t.Helper()
	now := time.Date(2026, time.August, 26, 14, 0, 0, 0, time.UTC)
	dialResults := make([]humancalling.ProviderResult, 0, staffCount)
	for index := range staffCount {
		dialResults = append(dialResults, humancalling.ProviderResult{
			CallControlID: fmt.Sprintf("%s-staff-control-%d", prefix, index+1),
			CallLegID:     fmt.Sprintf("%s-staff-provider-leg-%d", prefix, index+1),
		})
	}
	provider := &recordingProvider{dialResults: dialResults}
	pool, calling, caller, staff := prepareInboundFanout(t, now, prefix, provider, staffCount)
	if _, err := pool.Exec(context.Background(), `
		UPDATE access_practices
		SET connected_call_recording_enabled = true,
			connected_call_recording_retention_days = 30
	`); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)

	var sourceLeg, sourceState, sourceControl, sourceProviderLeg string
	if err := pool.QueryRow(context.Background(), `
		SELECT leg.id::text, command.payload->>'client_state',
			leg.provider_call_control_id, leg.provider_call_leg_id
		FROM human_calling_call_legs leg
		JOIN human_calling_provider_commands command ON command.call_leg_id = leg.id
		WHERE leg.role = 'STAFF' AND leg.staff_subject = $1
			AND command.action = 'DIAL_STAFF'
	`, staff[0].Subject).Scan(
		&sourceLeg, &sourceState, &sourceControl, &sourceProviderLeg,
	); err != nil {
		t.Fatal(err)
	}
	var callID string
	if err := pool.QueryRow(context.Background(), `
		SELECT call_id::text FROM human_calling_call_legs WHERE id = $1
	`, sourceLeg).Scan(&callID); err != nil {
		t.Fatal(err)
	}
	source := humancalling.ProviderFact{
		EventID: prefix + "-source-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(2 * time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: sourceControl, CallLegID: sourceProviderLeg,
		CallSessionID: prefix + "-source-session", ClientState: sourceState,
	}
	if err := calling.ApplyProviderFact(context.Background(), source); err != nil {
		t.Fatalf("project transfer fixture source initiated: %v", err)
	}
	source.EventID = prefix + "-source-answered"
	source.Type = humancalling.FactCallAnswered
	source.OccurredAt = now.Add(3 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), source); err != nil {
		t.Fatalf("project transfer fixture source answered: %v", err)
	}
	processAllCommands(t, calling)
	bridge := provider.last(humancalling.CommandBridge)
	bridgeState, _ := bridge.Payload["client_state"].(string)
	source.EventID = prefix + "-source-bridged"
	source.Type = humancalling.FactCallBridged
	source.OccurredAt = now.Add(4 * time.Second)
	source.ClientState = bridgeState
	if err := calling.ApplyProviderFact(context.Background(), source); err != nil {
		t.Fatalf("project transfer fixture source bridged: %v", err)
	}
	caller.EventID = prefix + "-caller-bridged"
	caller.Type = humancalling.FactCallBridged
	caller.OccurredAt = now.Add(4 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatalf("project transfer fixture caller bridged: %v", err)
	}
	processAllCommands(t, calling)

	rows, err := pool.Query(context.Background(), `
		SELECT id::text, provider_call_control_id, provider_call_leg_id
		FROM human_calling_call_legs
		WHERE call_id = $1 AND role = 'STAFF' AND id <> $2
			AND state NOT IN ('ENDED', 'FAILED')
		ORDER BY id
	`, callID, sourceLeg)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	loserIndex := 0
	for rows.Next() {
		loserIndex++
		var loserID, loserControl, loserProviderLeg string
		if err := rows.Scan(&loserID, &loserControl, &loserProviderLeg); err != nil {
			t.Fatal(err)
		}
		loserSession := fmt.Sprintf("%s-loser-session-%d", prefix, loserIndex)
		if _, err := pool.Exec(context.Background(), `
			UPDATE human_calling_call_legs
			SET provider_call_session_id = COALESCE(provider_call_session_id, $2)
			WHERE id = $1
		`, loserID, loserSession); err != nil {
			t.Fatal(err)
		}
		if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
			EventID: fmt.Sprintf("%s-loser-ended-%d", prefix, loserIndex), Type: humancalling.FactCallHangup,
			OccurredAt: now.Add(5 * time.Second), CallControlID: loserControl,
			CallLegID: loserProviderLeg, CallSessionID: loserSession,
			HangupCause: "NORMAL_CLEARING", TerminationSource: "CALL_CONTROL",
		}); err != nil {
			t.Fatalf("project transfer fixture losing Staff Hangup: %v", err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return connectedStaffTransferFixture{
		pool: pool, calling: calling, provider: provider, staff: staff,
		callID: callID, sourceLeg: sourceLeg, caller: caller, now: now,
	}
}

func requestFixtureTransfer(
	t *testing.T,
	fixture connectedStaffTransferFixture,
	key string,
) (humancalling.StaffTransfer, humancalling.ProviderCommand) {
	t.Helper()
	call, err := fixture.calling.ReadCall(context.Background(), fixture.staff[0], fixture.callID)
	if err != nil {
		t.Fatal(err)
	}
	transfer, err := fixture.calling.RequestStaffTransfer(
		context.Background(), humancalling.RequestStaffTransferCommand{
			Identity: fixture.staff[0], CallID: fixture.callID,
			SessionID:        "staff-transfer-" + key + "-browser-1",
			RecipientSubject: fixture.staff[1].Subject,
			IdempotencyKey:   key, HandoffNote: "Synthetic handoff note",
			ExpectedVersion: call.Version,
		},
	)
	if err != nil {
		t.Fatalf("request Staff transfer: %v", err)
	}
	processAllCommands(t, fixture.calling)
	return transfer, fixture.provider.last(humancalling.CommandTransferStaff)
}

func transferTargetFact(
	transfer humancalling.StaffTransfer,
	command humancalling.ProviderCommand,
	prefix string,
	factType humancalling.FactType,
	occurredAt time.Time,
) humancalling.ProviderFact {
	clientState, _ := command.Payload["target_leg_client_state"].(string)
	return humancalling.ProviderFact{
		EventID: prefix + "-" + string(factType), Type: factType,
		OccurredAt: occurredAt, ConnectionID: "staff-call-control-connection",
		CallControlID: prefix + "-target-control",
		CallLegID:     prefix + "-target-provider-leg",
		CallSessionID: prefix + "-target-session", ClientState: clientState,
	}
}

func completeFixtureTransfer(
	t *testing.T,
	fixture connectedStaffTransferFixture,
	transfer humancalling.StaffTransfer,
	command humancalling.ProviderCommand,
	prefix string,
) humancalling.ProviderFact {
	t.Helper()
	target := transferTargetFact(
		transfer, command, prefix, humancalling.FactCallInitiated,
		fixture.now.Add(6*time.Second),
	)
	if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
		t.Fatalf("initiate transfer target: %v", err)
	}
	target.EventID = prefix + "-answered"
	target.Type = humancalling.FactCallAnswered
	target.OccurredAt = fixture.now.Add(7 * time.Second)
	if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
		t.Fatalf("answer transfer target: %v", err)
	}
	target.EventID = prefix + "-bridged"
	target.Type = humancalling.FactCallBridged
	target.OccurredAt = fixture.now.Add(8 * time.Second)
	if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
		t.Fatalf("bridge transfer target: %v", err)
	}
	return target
}

func TestStaffTransferRequiresTargetAnswerAndBridgeEvidence(t *testing.T) {
	for _, order := range []string{"answer-before-bridge", "bridge-before-answer"} {
		t.Run(order, func(t *testing.T) {
			fixture := prepareConnectedStaffTransfer(t, "staff-transfer-"+order)
			call, err := fixture.calling.ReadCall(context.Background(), fixture.staff[0], fixture.callID)
			if err != nil {
				t.Fatal(err)
			}
			candidates, err := fixture.calling.ListTransferCandidates(
				context.Background(), fixture.staff[0], fixture.callID,
				"staff-transfer-"+order+"-browser-1",
			)
			if err != nil || len(candidates) != 1 || candidates[0].Subject != fixture.staff[1].Subject {
				t.Fatalf("transfer candidates = %#v, %v", candidates, err)
			}
			transfer, err := fixture.calling.RequestStaffTransfer(
				context.Background(), humancalling.RequestStaffTransferCommand{
					Identity: fixture.staff[0], CallID: fixture.callID,
					SessionID:        "staff-transfer-" + order + "-browser-1",
					RecipientSubject: fixture.staff[1].Subject,
					IdempotencyKey:   "transfer-" + order,
					HandoffNote:      "Synthetic handoff note", ExpectedVersion: call.Version,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			processAllCommands(t, fixture.calling)
			command := fixture.provider.last(humancalling.CommandTransferStaff)
			if command.TargetID == "" || command.CallLegID != transfer.TargetCallLegID ||
				command.PeerCallLegID != transfer.CustomerCallLegID ||
				command.Payload["client_state"] == command.Payload["target_leg_client_state"] ||
				command.Payload["record"] != nil || command.Payload["prevent_double_bridge"] != nil {
				t.Fatalf("transfer provider command = %#v", command)
			}
			targetState, _ := command.Payload["target_leg_client_state"].(string)
			target := humancalling.ProviderFact{
				ConnectionID:  "staff-call-control-connection",
				CallControlID: "target-control-" + order,
				CallLegID:     "target-provider-leg-" + order,
				CallSessionID: "target-session-" + order,
				ClientState:   targetState,
			}
			apply := func(eventID string, factType humancalling.FactType, offset int) {
				t.Helper()
				target.EventID = eventID
				target.Type = factType
				target.OccurredAt = fixture.now.Add(time.Duration(offset) * time.Second)
				if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
					t.Fatalf("apply %s: %v", factType, err)
				}
			}
			apply("target-initiated-"+order, humancalling.FactCallInitiated, 6)
			if order == "answer-before-bridge" {
				apply("target-answered-"+order, humancalling.FactCallAnswered, 7)
				assertTransferLegStates(t, fixture.pool, transfer.ID, "ACCEPTED", "BRIDGED", "ANSWERED")
				apply("target-bridged-"+order, humancalling.FactCallBridged, 8)
			} else {
				apply("target-bridged-"+order, humancalling.FactCallBridged, 7)
				assertTransferLegStates(t, fixture.pool, transfer.ID, "REQUESTED", "BRIDGED", "RINGING")
				apply("target-answered-"+order, humancalling.FactCallAnswered, 8)
			}
			assertTransferLegStates(t, fixture.pool, transfer.ID, "COMPLETED", "ENDED", "BRIDGED")

			var recordingCount int
			if err := fixture.pool.QueryRow(context.Background(), `
				SELECT count(*) FROM human_calling_call_recordings WHERE call_id = $1
			`, fixture.callID).Scan(&recordingCount); err != nil || recordingCount != 1 {
				t.Fatalf("connected recording evidence count = %d, %v", recordingCount, err)
			}
			sourceState, err := fixture.calling.ReadCallingState(context.Background(), fixture.staff[0])
			if err != nil || sourceState.Bridged != nil {
				t.Fatalf("source Calling state after transfer = %#v, %v", sourceState, err)
			}
			targetCalling, err := fixture.calling.ReadCallingState(context.Background(), fixture.staff[1])
			if err != nil || targetCalling.Bridged == nil ||
				targetCalling.Bridged.CallLegID != transfer.TargetCallLegID {
				t.Fatalf("target Calling state after transfer = %#v, %v", targetCalling, err)
			}
		})
	}
}

func assertTransferLegStates(
	t *testing.T,
	pool *pgxpool.Pool,
	transferID string,
	wantTransfer string,
	wantSource string,
	wantTarget string,
) {
	t.Helper()
	var transferState, sourceState, targetState string
	if err := pool.QueryRow(context.Background(), `
		SELECT transfer.state, source.state, target.state
		FROM human_calling_staff_transfers transfer
		JOIN human_calling_call_legs source ON source.id = transfer.source_staff_leg_id
		JOIN human_calling_call_legs target ON target.id = transfer.target_staff_leg_id
		WHERE transfer.id = $1
	`, transferID).Scan(&transferState, &sourceState, &targetState); err != nil {
		t.Fatal(err)
	}
	if transferState != wantTransfer || sourceState != wantSource || targetState != wantTarget {
		t.Fatalf("transfer states = %s/%s/%s, want %s/%s/%s",
			transferState, sourceState, targetState, wantTransfer, wantSource, wantTarget)
	}
}

func TestStaffTransferDeclineLeavesSourceOwner(t *testing.T) {
	fixture := prepareConnectedStaffTransfer(t, "staff-transfer-decline")
	call, _ := fixture.calling.ReadCall(context.Background(), fixture.staff[0], fixture.callID)
	transfer, err := fixture.calling.RequestStaffTransfer(
		context.Background(), humancalling.RequestStaffTransferCommand{
			Identity: fixture.staff[0], CallID: fixture.callID,
			SessionID:        "staff-transfer-decline-browser-1",
			RecipientSubject: fixture.staff[1].Subject,
			IdempotencyKey:   "decline", ExpectedVersion: call.Version,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.calling.DeclineStaffTransfer(
		context.Background(), humancalling.RespondStaffTransferCommand{
			Identity: fixture.staff[1], TransferID: transfer.ID,
			SessionID: "staff-transfer-decline-browser-2",
		},
	); err != nil {
		t.Fatal(err)
	}
	assertTransferLegStates(t, fixture.pool, transfer.ID, "DECLINED", "BRIDGED", "FAILED")
	state, err := fixture.calling.ReadCallingState(context.Background(), fixture.staff[0])
	if err != nil || state.Bridged == nil || state.Bridged.CallLegID != fixture.sourceLeg {
		t.Fatalf("source after decline = %#v, %v", state, err)
	}
}

func TestStaffTransferSourceCanCancelWhenActiveCallRefusesSoftphoneTakeover(t *testing.T) {
	fixture := prepareConnectedStaffTransfer(t, "staff-transfer-takeover-cancel")
	transfer, _ := requestFixtureTransfer(t, fixture, "takeover-cancel")
	const takeoverSession = "staff-transfer-takeover-cancel-browser-replacement"

	lease, err := fixture.calling.AcquireSoftphone(
		context.Background(), fixture.staff[0], takeoverSession, true,
	)
	if err != nil || lease.Owner || lease.ActiveCallID != fixture.callID {
		t.Fatalf("refuse active Call takeover = %#v, %v", lease, err)
	}

	canceled, err := fixture.calling.CancelStaffTransfer(
		context.Background(), humancalling.RespondStaffTransferCommand{
			Identity: fixture.staff[0], TransferID: transfer.ID,
			SessionID: "staff-transfer-takeover-cancel-browser-1",
		},
	)
	if err != nil || canceled.State != humancalling.StaffTransferCanceled {
		t.Fatalf("cancel transfer from current source browser = %#v, %v", canceled, err)
	}
	assertTransferLegStates(t, fixture.pool, transfer.ID, "CANCELED", "BRIDGED", "ENDING")
}

func TestStaffTransferSourceCanCancelAfterTargetAnswerBeforeBridge(t *testing.T) {
	fixture := prepareConnectedStaffTransfer(t, "staff-transfer-accepted-cancel")
	transfer, command := requestFixtureTransfer(t, fixture, "accepted-cancel")
	target := transferTargetFact(
		transfer,
		command,
		"staff-transfer-accepted-cancel",
		humancalling.FactCallInitiated,
		fixture.now.Add(6*time.Second),
	)
	if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
		t.Fatalf("initiate transfer target: %v", err)
	}
	target.EventID = "staff-transfer-accepted-cancel-answered"
	target.Type = humancalling.FactCallAnswered
	target.OccurredAt = fixture.now.Add(7 * time.Second)
	if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
		t.Fatalf("answer transfer target: %v", err)
	}

	canceled, err := fixture.calling.CancelStaffTransfer(
		context.Background(),
		humancalling.RespondStaffTransferCommand{
			Identity:   fixture.staff[0],
			TransferID: transfer.ID,
			SessionID:  "staff-transfer-accepted-cancel-browser-1",
		},
	)
	if err != nil || canceled.State != humancalling.StaffTransferCanceled {
		t.Fatalf("cancel accepted transfer = %#v, %v", canceled, err)
	}
	assertTransferLegStates(t, fixture.pool, transfer.ID, "CANCELED", "BRIDGED", "ENDING")
	var available bool
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT desired_available
		FROM human_calling_softphone_leases
		WHERE user_subject = $1
	`, fixture.staff[1].Subject).Scan(&available); err != nil || !available {
		t.Fatalf("recipient availability after accepted cancel = %t, %v", available, err)
	}
}

func TestCallingStateHidesActiveStaffTransferAfterLocationAccessLoss(t *testing.T) {
	fixture := prepareConnectedStaffTransfer(t, "staff-transfer-access-scope")
	transfer, _ := requestFixtureTransfer(t, fixture, "access-scope")

	var otherLocationID string
	if err := fixture.pool.QueryRow(context.Background(), `
		INSERT INTO access_locations (practice_id, provisioning_key, name)
		VALUES ($1, 'staff-transfer-access-scope-other', 'Other Location')
		RETURNING id::text
	`, transfer.PracticeID).Scan(&otherLocationID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(context.Background(), `
		WITH narrowed AS (
			UPDATE access_memberships
			SET location_scope = 'SELECTED'
			WHERE practice_id = $1 AND user_subject = $2
			RETURNING id, practice_id
		)
		INSERT INTO access_membership_locations (membership_id, practice_id, location_id)
		SELECT id, practice_id, $3 FROM narrowed
	`, transfer.PracticeID, fixture.staff[0].Subject, otherLocationID); err != nil {
		t.Fatal(err)
	}

	state, err := fixture.calling.ReadCallingState(context.Background(), fixture.staff[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Transfers) != 0 {
		t.Fatalf("active transfers after Location access loss = %#v, want none", state.Transfers)
	}
}

func TestStaffTransferFailurePathsPreserveSourceOwnership(t *testing.T) {
	tests := []struct {
		name      string
		wantState string
		fail      func(*testing.T, connectedStaffTransferFixture, humancalling.StaffTransfer, humancalling.ProviderCommand)
	}{
		{
			name: "source cancellation", wantState: "CANCELED",
			fail: func(t *testing.T, fixture connectedStaffTransferFixture, transfer humancalling.StaffTransfer, _ humancalling.ProviderCommand) {
				_, err := fixture.calling.CancelStaffTransfer(context.Background(), humancalling.RespondStaffTransferCommand{
					Identity: fixture.staff[0], TransferID: transfer.ID,
					SessionID: "staff-transfer-cancel-browser-1",
				})
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "authoritative timeout", wantState: "EXPIRED",
			fail: func(t *testing.T, fixture connectedStaffTransferFixture, transfer humancalling.StaffTransfer, _ humancalling.ProviderCommand) {
				if _, err := fixture.pool.Exec(context.Background(), `
					UPDATE human_calling_staff_transfers SET expires_at = $2 WHERE id = $1
				`, transfer.ID, fixture.now); err != nil {
					t.Fatal(err)
				}
				processed, err := fixture.calling.ExpireStaffTransfers(context.Background())
				if err != nil || processed != 1 {
					t.Fatalf("expire transfer = %d, %v", processed, err)
				}
			},
		},
		{
			name: "target readiness loss", wantState: "FAILED",
			fail: func(t *testing.T, fixture connectedStaffTransferFixture, _ humancalling.StaffTransfer, _ humancalling.ProviderCommand) {
				_, err := fixture.calling.SetReadiness(context.Background(), humancalling.ReadinessCommand{
					Identity: fixture.staff[1], SessionID: "staff-transfer-readiness-browser-2",
					Registered: true, MicrophoneReady: true, AudioReady: true,
					SessionHealthy: false, Available: false,
				})
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "target authorization loss", wantState: "FAILED",
			fail: func(t *testing.T, fixture connectedStaffTransferFixture, transfer humancalling.StaffTransfer, command humancalling.ProviderCommand) {
				target := transferTargetFact(transfer, command, "staff-transfer-authorization", humancalling.FactCallInitiated, fixture.now.Add(6*time.Second))
				if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.pool.Exec(context.Background(), `
					UPDATE access_memberships SET revoked_at = $2
					WHERE practice_id = $1 AND user_subject = $3
				`, transfer.PracticeID, fixture.now, fixture.staff[1].Subject); err != nil {
					t.Fatal(err)
				}
				target.EventID = "staff-transfer-authorization-answered"
				target.Type = humancalling.FactCallAnswered
				target.OccurredAt = fixture.now.Add(7 * time.Second)
				if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "target disconnect", wantState: "FAILED",
			fail: func(t *testing.T, fixture connectedStaffTransferFixture, transfer humancalling.StaffTransfer, command humancalling.ProviderCommand) {
				target := transferTargetFact(transfer, command, "staff-transfer-target-hangup", humancalling.FactCallInitiated, fixture.now.Add(6*time.Second))
				if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
					t.Fatal(err)
				}
				target.EventID = "staff-transfer-target-hangup-ended"
				target.Type = humancalling.FactCallHangup
				target.OccurredAt = fixture.now.Add(7 * time.Second)
				target.HangupCause = "NORMAL_CLEARING"
				if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := map[string]string{
				"source cancellation": "cancel", "authoritative timeout": "timeout",
				"target readiness loss": "readiness", "target disconnect": "target-hangup",
				"target authorization loss": "authorization",
			}[test.name]
			fixture := prepareConnectedStaffTransfer(t, "staff-transfer-"+key)
			transfer, command := requestFixtureTransfer(t, fixture, key)
			test.fail(t, fixture, transfer, command)
			var transferState, sourceState, targetState string
			if err := fixture.pool.QueryRow(context.Background(), `
				SELECT transfer.state, source.state, target.state
				FROM human_calling_staff_transfers transfer
				JOIN human_calling_call_legs source ON source.id = transfer.source_staff_leg_id
				JOIN human_calling_call_legs target ON target.id = transfer.target_staff_leg_id
				WHERE transfer.id = $1
			`, transfer.ID).Scan(&transferState, &sourceState, &targetState); err != nil {
				t.Fatal(err)
			}
			if transferState != test.wantState || sourceState != "BRIDGED" ||
				targetState == "ANSWERED" || targetState == "BRIDGE_PENDING" || targetState == "BRIDGED" {
				t.Fatalf("failed transfer states = %s/%s/%s", transferState, sourceState, targetState)
			}
			if targetState == "ENDING" {
				var commandState, targetControl string
				if err := fixture.pool.QueryRow(context.Background(), `
					SELECT command.state, COALESCE(leg.provider_call_control_id, '')
					FROM human_calling_provider_commands command
					JOIN human_calling_call_legs leg ON leg.id = command.call_leg_id
					WHERE command.id = $1
				`, transfer.ProviderCommandID).Scan(&commandState, &targetControl); err != nil ||
					(commandState != "AMBIGUOUS" && !(commandState == "RECONCILED" && targetControl != "")) {
					t.Fatalf("uncertain target command = %q control:%q, %v", commandState, targetControl, err)
				}
			}
			state, err := fixture.calling.ReadCallingState(context.Background(), fixture.staff[0])
			if err != nil || state.Bridged == nil || state.Bridged.CallLegID != fixture.sourceLeg {
				t.Fatalf("source owner after %s = %#v, %v", test.name, state, err)
			}
		})
	}
}

func TestStaffTransferDefinitiveProviderFailurePreservesSource(t *testing.T) {
	fixture := prepareConnectedStaffTransfer(t, "staff-transfer-provider-failure")
	fixture.provider.actionErrors = map[humancalling.CommandAction][]error{
		humancalling.CommandTransferStaff: {
			fmt.Errorf("%w: synthetic rejected transfer", humancalling.ErrDefinitiveProviderFailure),
		},
	}
	call, err := fixture.calling.ReadCall(context.Background(), fixture.staff[0], fixture.callID)
	if err != nil {
		t.Fatal(err)
	}
	transfer, err := fixture.calling.RequestStaffTransfer(context.Background(), humancalling.RequestStaffTransferCommand{
		Identity: fixture.staff[0], CallID: fixture.callID,
		SessionID:        "staff-transfer-provider-failure-browser-1",
		RecipientSubject: fixture.staff[1].Subject, IdempotencyKey: "provider-failure",
		ExpectedVersion: call.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, fixture.calling)
	assertTransferLegStates(t, fixture.pool, transfer.ID, "FAILED", "BRIDGED", "FAILED")
}

func TestStaffTransferIsIdempotentAcrossReplaysAndCompletion(t *testing.T) {
	fixture := prepareConnectedStaffTransfer(t, "staff-transfer-idempotent")
	call, err := fixture.calling.ReadCall(context.Background(), fixture.staff[0], fixture.callID)
	if err != nil {
		t.Fatal(err)
	}
	request := humancalling.RequestStaffTransferCommand{
		Identity: fixture.staff[0], CallID: fixture.callID,
		SessionID:        "staff-transfer-idempotent-browser-1",
		RecipientSubject: fixture.staff[1].Subject, IdempotencyKey: "idempotent",
		HandoffNote: "same durable request", ExpectedVersion: call.Version,
	}
	first, err := fixture.calling.RequestStaffTransfer(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.calling.RequestStaffTransfer(context.Background(), request)
	if err != nil || second.ID != first.ID {
		t.Fatalf("active replay = %#v, %v", second, err)
	}
	processAllCommands(t, fixture.calling)
	if got := fixture.provider.count(humancalling.CommandTransferStaff); got != 1 {
		t.Fatalf("transfer executions = %d, want 1", got)
	}
	command := fixture.provider.last(humancalling.CommandTransferStaff)
	target := completeFixtureTransfer(t, fixture, first, command, "staff-transfer-idempotent")
	if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
		t.Fatalf("duplicate provider fact: %v", err)
	}
	completed, err := fixture.calling.RequestStaffTransfer(context.Background(), request)
	if err != nil || completed.ID != first.ID || completed.State != humancalling.StaffTransferCompleted {
		t.Fatalf("completed replay = %#v, %v", completed, err)
	}
	if got := fixture.provider.count(humancalling.CommandTransferStaff); got != 1 {
		t.Fatalf("replayed transfer executions = %d, want 1", got)
	}
}

func TestStaffTransferSourceAndTargetHangupsUseCurrentOwner(t *testing.T) {
	fixture := prepareConnectedStaffTransfer(t, "staff-transfer-hangup-authority")
	transfer, command := requestFixtureTransfer(t, fixture, "hangup-authority")

	var sourceControl, sourceProviderLeg, sourceSession string
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT provider_call_control_id, provider_call_leg_id, provider_call_session_id
		FROM human_calling_call_legs WHERE id = $1
	`, fixture.sourceLeg).Scan(&sourceControl, &sourceProviderLeg, &sourceSession); err != nil {
		t.Fatal(err)
	}
	sourceHangup := humancalling.ProviderFact{
		EventID: "staff-transfer-source-ended-in-flight", Type: humancalling.FactCallHangup,
		OccurredAt: fixture.now.Add(6 * time.Second), CallControlID: sourceControl,
		CallLegID: sourceProviderLeg, CallSessionID: sourceSession,
		HangupCause: "NORMAL_CLEARING", TerminationSource: "CALL_CONTROL",
	}
	if err := fixture.calling.ApplyProviderFact(context.Background(), sourceHangup); err != nil {
		t.Fatal(err)
	}
	target := completeFixtureTransfer(t, fixture, transfer, command, "staff-transfer-hangup-authority")
	assertTransferLegStates(t, fixture.pool, transfer.ID, "COMPLETED", "ENDED", "BRIDGED")

	sourceHangup.EventID = "staff-transfer-old-source-delayed-cleanup"
	sourceHangup.OccurredAt = fixture.now.Add(9 * time.Second)
	if err := fixture.calling.ApplyProviderFact(context.Background(), sourceHangup); err != nil {
		t.Fatal(err)
	}
	var terminal string
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT COALESCE(terminal_outcome, '') FROM human_calling_calls WHERE id = $1
	`, fixture.callID).Scan(&terminal); err != nil || terminal != "" {
		t.Fatalf("old source ended transferred Call = %q, %v", terminal, err)
	}

	target.EventID = "staff-transfer-current-target-ended"
	target.Type = humancalling.FactCallHangup
	target.OccurredAt = fixture.now.Add(10 * time.Second)
	target.HangupCause = "NORMAL_CLEARING"
	if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT terminal_outcome FROM human_calling_calls WHERE id = $1
	`, fixture.callID).Scan(&terminal); err != nil || terminal != "ENDED" {
		t.Fatalf("current target terminal outcome = %q, %v", terminal, err)
	}
	state, err := fixture.calling.AcquireSoftphone(context.Background(), fixture.staff[1], "staff-transfer-hangup-authority-browser-2", false)
	if err != nil || state.ActiveCallID != "" {
		t.Fatalf("target softphone release = %#v, %v", state, err)
	}
	var dispositionWindows int
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_calls
		WHERE id = $1 AND terminal_outcome = 'ENDED' AND disposition_deadline IS NOT NULL
	`, fixture.callID).Scan(&dispositionWindows); err != nil || dispositionWindows != 1 {
		t.Fatalf("disposition outcomes = %d, %v", dispositionWindows, err)
	}
}

func TestStaffTransferCannotStartAfterSourceAlreadyEnded(t *testing.T) {
	fixture := prepareConnectedStaffTransfer(t, "staff-transfer-source-ended-before")
	var sourceControl, sourceProviderLeg, sourceSession string
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT provider_call_control_id, provider_call_leg_id, provider_call_session_id
		FROM human_calling_call_legs WHERE id = $1
	`, fixture.sourceLeg).Scan(&sourceControl, &sourceProviderLeg, &sourceSession); err != nil {
		t.Fatal(err)
	}
	if err := fixture.calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "staff-transfer-source-ended-before-request", Type: humancalling.FactCallHangup,
		OccurredAt: fixture.now.Add(6 * time.Second), CallControlID: sourceControl,
		CallLegID: sourceProviderLeg, CallSessionID: sourceSession,
		HangupCause: "NORMAL_CLEARING",
	}); err != nil {
		t.Fatal(err)
	}
	call, err := fixture.calling.ReadCall(context.Background(), fixture.staff[0], fixture.callID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.calling.RequestStaffTransfer(context.Background(), humancalling.RequestStaffTransferCommand{
		Identity: fixture.staff[0], CallID: fixture.callID,
		SessionID:        "staff-transfer-source-ended-before-browser-1",
		RecipientSubject: fixture.staff[1].Subject, IdempotencyKey: "too-late",
		ExpectedVersion: call.Version,
	})
	if !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("transfer after source ended = %v", err)
	}
	var transfers int
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_staff_transfers WHERE call_id = $1
	`, fixture.callID).Scan(&transfers); err != nil || transfers != 0 {
		t.Fatalf("late transfers = %d, %v", transfers, err)
	}
}

func TestStaffTransferCallerHangupCancelsTransferOnce(t *testing.T) {
	fixture := prepareConnectedStaffTransfer(t, "staff-transfer-caller-hangup")
	transfer, _ := requestFixtureTransfer(t, fixture, "caller-hangup")
	caller := fixture.caller
	caller.EventID = "staff-transfer-caller-ended"
	caller.Type = humancalling.FactCallHangup
	caller.OccurredAt = fixture.now.Add(6 * time.Second)
	caller.HangupCause = "NORMAL_CLEARING"
	if err := fixture.calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatal(err)
	}
	if err := fixture.calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatalf("duplicate caller Hangup: %v", err)
	}
	var transferState, terminal string
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT transfer.state, call.terminal_outcome
		FROM human_calling_staff_transfers transfer
		JOIN human_calling_calls call ON call.id = transfer.call_id
		WHERE transfer.id = $1
	`, transfer.ID).Scan(&transferState, &terminal); err != nil ||
		transferState != "CANCELED" || terminal != "ENDED" {
		t.Fatalf("caller termination = %s/%s, %v", transferState, terminal, err)
	}
}

func TestOutboundStaffTransferTargetsDestinationLeg(t *testing.T) {
	fixture := prepareConnectedOutboundStaffTransfer(t, "staff-transfer-outbound")
	transfer, command := requestFixtureTransfer(t, fixture, "outbound")
	var customerRole, customerControl string
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT role, provider_call_control_id
		FROM human_calling_call_legs WHERE id = $1
	`, transfer.CustomerCallLegID).Scan(&customerRole, &customerControl); err != nil {
		t.Fatal(err)
	}
	if customerRole != "DESTINATION" || command.PeerCallLegID != transfer.CustomerCallLegID ||
		command.TargetID != customerControl {
		t.Fatalf("outbound transfer customer target = role:%s command:%#v", customerRole, command)
	}
	completeFixtureTransfer(t, fixture, transfer, command, "staff-transfer-outbound")
	assertTransferLegStates(t, fixture.pool, transfer.ID, "COMPLETED", "ENDED", "BRIDGED")
}

func TestInterruptedStaffTransferCommandReconcilesWithoutNewIdentity(t *testing.T) {
	fixture := prepareConnectedStaffTransfer(t, "staff-transfer-interrupted")
	call, err := fixture.calling.ReadCall(context.Background(), fixture.staff[0], fixture.callID)
	if err != nil {
		t.Fatal(err)
	}
	transfer, err := fixture.calling.RequestStaffTransfer(context.Background(), humancalling.RequestStaffTransferCommand{
		Identity: fixture.staff[0], CallID: fixture.callID,
		SessionID:        "staff-transfer-interrupted-browser-1",
		RecipientSubject: fixture.staff[1].Subject, IdempotencyKey: "interrupted",
		ExpectedVersion: call.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	old := fixture.now.Add(-2 * time.Minute)
	if _, err := fixture.pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET state = 'SENDING', attempts = 1, created_at = $2, updated_at = $2
		WHERE id = $1
	`, transfer.ProviderCommandID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs SET updated_at = $1 WHERE id = $2
	`, old, transfer.TargetCallLegID); err != nil {
		t.Fatal(err)
	}
	fixture.provider.observations = []humancalling.ProviderCallObservation{{
		Active: true, CallControlID: "interrupted-target-control",
		CallLegID: "interrupted-target-leg", CallSessionID: "interrupted-target-session",
	}}
	processed, err := fixture.calling.MaintainOutgoingCallLegs(context.Background())
	if err != nil || !processed {
		t.Fatalf("maintain interrupted transfer = %t, %v", processed, err)
	}
	var commandState, stableClientState string
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT state, payload->>'target_leg_client_state'
		FROM human_calling_provider_commands WHERE id = $1
	`, transfer.ProviderCommandID).Scan(&commandState, &stableClientState); err != nil {
		t.Fatal(err)
	}
	if commandState != "AMBIGUOUS" {
		t.Fatalf("interrupted transfer command state = %s", commandState)
	}
	var projectedState, projectedClientState string
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT leg.state, command.payload->>'target_leg_client_state'
		FROM human_calling_call_legs leg
		JOIN human_calling_provider_commands command ON command.id = $2
		WHERE leg.id = $1
	`, transfer.TargetCallLegID, transfer.ProviderCommandID).Scan(
		&projectedState, &projectedClientState,
	); err != nil {
		t.Fatal(err)
	}
	if projectedState != "RINGING" || projectedClientState != stableClientState ||
		fixture.provider.count(humancalling.CommandTransferStaff) != 0 {
		t.Fatalf("reconciled stable transfer = state:%s client:%q executions:%d",
			projectedState, projectedClientState,
			fixture.provider.count(humancalling.CommandTransferStaff))
	}
}

func TestChainedStaffTransfersKeepOneCurrentOwnerAndHistoryRow(t *testing.T) {
	fixture := prepareConnectedStaffTransferWithCount(t, "staff-transfer-chain", 3)
	firstCall, err := fixture.calling.ReadCall(context.Background(), fixture.staff[0], fixture.callID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := fixture.calling.RequestStaffTransfer(context.Background(), humancalling.RequestStaffTransferCommand{
		Identity: fixture.staff[0], CallID: fixture.callID,
		SessionID:        "staff-transfer-chain-browser-1",
		RecipientSubject: fixture.staff[1].Subject, IdempotencyKey: "chain-first",
		ExpectedVersion: firstCall.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, fixture.calling)
	completeFixtureTransfer(t, fixture, first, fixture.provider.last(humancalling.CommandTransferStaff), "staff-transfer-chain-first")

	secondCall, err := fixture.calling.ReadCall(context.Background(), fixture.staff[1], fixture.callID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.calling.RequestStaffTransfer(context.Background(), humancalling.RequestStaffTransferCommand{
		Identity: fixture.staff[1], CallID: fixture.callID,
		SessionID:        "staff-transfer-chain-browser-2",
		RecipientSubject: fixture.staff[2].Subject, IdempotencyKey: "chain-second",
		ExpectedVersion: secondCall.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, fixture.calling)
	completeFixtureTransfer(t, fixture, second, fixture.provider.last(humancalling.CommandTransferStaff), "staff-transfer-chain-second")

	state, err := fixture.calling.ReadCallingState(context.Background(), fixture.staff[2])
	if err != nil || state.Bridged == nil || state.Bridged.CallLegID != second.TargetCallLegID {
		t.Fatalf("chained current owner = %#v, %v", state, err)
	}
	var practiceID, currentOwnerEmail string
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT call.practice_id::text, membership.email
		FROM human_calling_calls call
		JOIN human_calling_call_legs leg ON leg.call_id = call.id AND leg.state = 'BRIDGED'
		JOIN access_memberships membership
			ON membership.practice_id = call.practice_id AND membership.user_subject = leg.staff_subject
		WHERE call.id = $1
	`, fixture.callID).Scan(&practiceID, &currentOwnerEmail); err != nil {
		t.Fatal(err)
	}
	history, err := fixture.calling.QueryCallHistory(context.Background(), humancalling.CallHistoryQuery{
		Identity: fixture.staff[2], PracticeID: practiceID, Phone: "+15555550100", Limit: 25,
	})
	if err != nil || len(history.Items) != 1 || history.Items[0].ID != fixture.callID ||
		history.Items[0].AnsweredByEmail != currentOwnerEmail {
		t.Fatalf("chained Call history = %#v, %v", history, err)
	}
	var bridgedOwners, historicalOwners int
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT
			count(*) FILTER (WHERE state = 'BRIDGED'),
			count(*) FILTER (WHERE bridged_at IS NOT NULL)
		FROM human_calling_call_legs WHERE call_id = $1 AND role = 'STAFF'
	`, fixture.callID).Scan(&bridgedOwners, &historicalOwners); err != nil ||
		bridgedOwners != 1 || historicalOwners != 3 {
		t.Fatalf("chained ownership = current:%d historical:%d, %v", bridgedOwners, historicalOwners, err)
	}
}

func TestUnrecognizedTransferPeerDoesNotPoisonExactTargetCallbacks(t *testing.T) {
	fixture := prepareConnectedStaffTransfer(t, "staff-transfer-peer-admission")
	transfer, command := requestFixtureTransfer(t, fixture, "peer-admission")
	targetSession := "staff-transfer-peer-shared-session"
	if err := fixture.calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "staff-transfer-unrecognized-peer-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt:    fixture.now.Add(6 * time.Second),
		CallControlID: "unrecognized-peer-control", CallLegID: "unrecognized-peer-leg",
		CallSessionID: targetSession, From: "sip:unknown@example.test", To: "sip:unknown@example.test",
	}); err == nil {
		t.Fatal("unrecognized transfer peer initiation was admitted")
	}
	target := transferTargetFact(
		transfer, command, "staff-transfer-peer-admission",
		humancalling.FactCallInitiated, fixture.now.Add(7*time.Second),
	)
	target.CallSessionID = targetSession
	if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
		t.Fatalf("exact target callback after rejected peer: %v", err)
	}
	if err := fixture.calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "staff-transfer-untagged-peer-bridged", Type: humancalling.FactCallBridged,
		OccurredAt: fixture.now.Add(8 * time.Second), CallControlID: "bridge-peer-control",
		CallLegID: "bridge-peer-leg", CallSessionID: targetSession,
	}); err != nil {
		t.Fatalf("correlate untagged transfer bridge peer: %v", err)
	}
	assertTransferLegStates(t, fixture.pool, transfer.ID, "REQUESTED", "BRIDGED", "RINGING")
	target.EventID = "staff-transfer-exact-target-answered"
	target.Type = humancalling.FactCallAnswered
	target.OccurredAt = fixture.now.Add(9 * time.Second)
	if err := fixture.calling.ApplyProviderFact(context.Background(), target); err != nil {
		t.Fatalf("exact target answer after peer bridge: %v", err)
	}
	assertTransferLegStates(t, fixture.pool, transfer.ID, "COMPLETED", "ENDED", "BRIDGED")
}
