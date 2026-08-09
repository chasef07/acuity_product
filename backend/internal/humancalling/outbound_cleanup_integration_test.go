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

func TestConcurrentOutboundOutcomesConvergeAndCleanUp(t *testing.T) {
	pool := testdb.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Date(2026, time.August, 9, 6, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, staff := provisionConcurrentStaff(
		t, accessModule, now, "outbound-resilience", 5,
	)
	dialResults := make([]humancalling.ProviderResult, 0, 10)
	for index := range staff {
		dialResults = append(dialResults, humancalling.ProviderResult{
			CallControlID: fmt.Sprintf("outbound-burst-staff-control-%d", index+1),
			CallLegID:     fmt.Sprintf("outbound-burst-staff-leg-%d", index+1),
		})
	}
	for index := range staff {
		dialResults = append(dialResults, humancalling.ProviderResult{
			CallControlID: fmt.Sprintf("outbound-burst-destination-control-%d", index+1),
			CallLegID:     fmt.Sprintf("outbound-burst-destination-leg-%d", index+1),
		})
	}
	provider := &recordingProvider{dialResults: dialResults}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		StaffSIPDomain:         "sip.telnyx.com",
		RingWindowDuration:     20 * time.Second,
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "fixture-call-control",
		CredentialConnectionID: "fixture-credential-connection",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	readyConcurrentStaff(t, calling, staff, "outbound-resilience-browser")
	if err := calling.ProvisionLocationVoices(ctx, []humancalling.LocationVoiceProvision{{
		PracticeKey: "outbound-resilience-practice",
		LocationKey: "outbound-resilience-location",
		Number:      "+14843336938",
		Enabled:     true,
	}}); err != nil {
		t.Fatalf("provision synthetic outbound voice: %v", err)
	}

	outcomes := []string{"answer", "busy", "no_answer", "call_rejected", "timeout"}
	type startResult struct {
		index int
		call  humancalling.Call
		err   error
	}
	start := make(chan struct{})
	started := make(chan startResult, len(staff))
	for index, identity := range staff {
		go func() {
			<-start
			call, err := calling.StartOutboundCall(ctx, humancalling.StartOutboundCallCommand{
				Identity:       identity,
				SessionID:      fmt.Sprintf("outbound-resilience-browser-%d", index+1),
				IdempotencyKey: "outbound-resilience-" + outcomes[index],
				PracticeID:     authorization.Practice.ID,
				LocationID:     authorization.Locations[0].ID,
				Destination:    fmt.Sprintf("+15555552%03d", index+1),
			})
			started <- startResult{index: index, call: call, err: err}
		}()
	}
	close(start)
	callOutcome := make(map[string]string, len(staff))
	identityBySubject := make(map[string]access.Identity, len(staff))
	for _, identity := range staff {
		identityBySubject[identity.Subject] = identity
	}
	for range staff {
		result := <-started
		if result.err != nil {
			t.Fatalf("start concurrent outbound Call: %v", result.err)
		}
		callOutcome[result.call.ID] = outcomes[result.index]
	}
	processAllCommands(t, calling)

	type legFact struct {
		callID, subject, controlID, legID, clientState, sessionID string
	}
	staffLegs := make([]legFact, 0, len(staff))
	rows, err := pool.Query(ctx, `
		SELECT call.id::text, leg.staff_subject, leg.provider_call_control_id,
			leg.provider_call_leg_id, command.payload->>'client_state'
		FROM human_calling_calls call
		JOIN human_calling_call_legs leg ON leg.call_id = call.id AND leg.role = 'STAFF'
		JOIN human_calling_provider_commands command
			ON command.call_leg_id = leg.id AND command.action = 'DIAL_OUTBOUND_STAFF'
		WHERE call.id = ANY($1::uuid[])
		ORDER BY call.id
	`, outboundCallIDs(callOutcome))
	if err != nil {
		t.Fatalf("read concurrent outbound Staff legs: %v", err)
	}
	for rows.Next() {
		var leg legFact
		if err := rows.Scan(&leg.callID, &leg.subject, &leg.controlID, &leg.legID, &leg.clientState); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		leg.sessionID = "outbound-burst-staff-session-" + leg.subject
		staffLegs = append(staffLegs, leg)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(staffLegs) != len(staff) {
		t.Fatalf("concurrent outbound Staff legs = %d, want %d", len(staffLegs), len(staff))
	}
	staffFacts := make(chan error, len(staffLegs))
	for _, leg := range staffLegs {
		go func() {
			fact := humancalling.ProviderFact{
				EventID: "outbound-burst-staff-initiated-" + leg.callID,
				Type:    humancalling.FactCallInitiated, OccurredAt: now.Add(time.Second),
				ConnectionID: "fixture-call-control", CallControlID: leg.controlID,
				CallLegID: leg.legID, CallSessionID: leg.sessionID,
				ClientState: leg.clientState,
			}
			if err := calling.ApplyProviderFact(ctx, fact); err != nil {
				staffFacts <- err
				return
			}
			fact.EventID = "outbound-burst-staff-answered-" + leg.callID
			fact.Type = humancalling.FactCallAnswered
			fact.OccurredAt = now.Add(2 * time.Second)
			staffFacts <- calling.ApplyProviderFact(ctx, fact)
		}()
	}
	for range staffLegs {
		if err := <-staffFacts; err != nil {
			t.Fatalf("project concurrent outbound Staff answer: %v", err)
		}
	}
	for _, leg := range staffLegs {
		identity := identityBySubject[leg.subject]
		state, err := calling.ReadCallingState(ctx, identity)
		if err != nil || len(state.Ringing) != 1 || state.Ringing[0].CallID != leg.callID {
			t.Fatalf("read concurrent outbound media state: %#v err=%v", state, err)
		}
		if _, err := calling.ConfirmOutboundMedia(ctx, humancalling.ConfirmOutboundMediaCommand{
			Identity:  identity,
			SessionID: "outbound-resilience-browser-" + leg.subject[len(leg.subject)-1:],
			CallID:    leg.callID, MediaToken: state.Ringing[0].MediaToken,
		}); err != nil {
			t.Fatalf("confirm concurrent outbound media: %v", err)
		}
	}
	processAllCommands(t, calling)

	destinationLegs := make([]legFact, 0, len(staff))
	rows, err = pool.Query(ctx, `
		SELECT call.id::text, staff.staff_subject, destination.provider_call_control_id,
			destination.provider_call_leg_id, command.payload->>'client_state'
		FROM human_calling_calls call
		JOIN human_calling_call_legs staff ON staff.call_id = call.id AND staff.role = 'STAFF'
		JOIN human_calling_call_legs destination ON destination.call_id = call.id AND destination.role = 'DESTINATION'
		JOIN human_calling_provider_commands command
			ON command.call_leg_id = destination.id AND command.action = 'DIAL_OUTBOUND_DESTINATION'
		WHERE call.id = ANY($1::uuid[])
		ORDER BY call.id
	`, outboundCallIDs(callOutcome))
	if err != nil {
		t.Fatalf("read concurrent outbound destination legs: %v", err)
	}
	for rows.Next() {
		var leg legFact
		if err := rows.Scan(&leg.callID, &leg.subject, &leg.controlID, &leg.legID, &leg.clientState); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		leg.sessionID = "outbound-burst-destination-session-" + leg.callID
		destinationLegs = append(destinationLegs, leg)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(destinationLegs) != len(staff) {
		t.Fatalf("concurrent outbound destination legs = %d, want %d", len(destinationLegs), len(staff))
	}
	destinationFacts := make(chan error, len(destinationLegs))
	for _, leg := range destinationLegs {
		go func() {
			destinationFacts <- calling.ApplyProviderFact(ctx, humancalling.ProviderFact{
				EventID: "outbound-burst-destination-initiated-" + leg.callID,
				Type:    humancalling.FactCallInitiated, OccurredAt: now.Add(3 * time.Second),
				ConnectionID: "fixture-call-control", CallControlID: leg.controlID,
				CallLegID: leg.legID, CallSessionID: leg.sessionID,
				ClientState: leg.clientState,
			})
		}()
	}
	for range destinationLegs {
		if err := <-destinationFacts; err != nil {
			t.Fatalf("project concurrent outbound destination initiation: %v", err)
		}
	}
	outcomeFacts := make(chan error, len(destinationLegs)-1)
	for _, leg := range destinationLegs {
		if callOutcome[leg.callID] == "answer" {
			if err := calling.ApplyProviderFact(ctx, humancalling.ProviderFact{
				EventID: "outbound-burst-destination-outcome-" + leg.callID,
				Type:    humancalling.FactCallAnswered, OccurredAt: now.Add(4 * time.Second),
				ConnectionID: "fixture-call-control", CallControlID: leg.controlID,
				CallLegID:     leg.legID,
				CallSessionID: leg.sessionID, ClientState: leg.clientState,
			}); err != nil {
				t.Fatalf("project answered concurrent outbound outcome: %v", err)
			}
			continue
		}
		go func() {
			outcome := callOutcome[leg.callID]
			fact := humancalling.ProviderFact{
				EventID:    "outbound-burst-destination-outcome-" + leg.callID,
				OccurredAt: now.Add(4 * time.Second), ConnectionID: "fixture-call-control",
				CallControlID: leg.controlID,
				CallLegID:     leg.legID, CallSessionID: leg.sessionID,
				ClientState: leg.clientState,
			}
			fact.Type = humancalling.FactCallHangup
			fact.HangupCause = outcome
			fact.TerminationSource = "DESTINATION"
			if err := calling.ApplyProviderFact(ctx, fact); err != nil {
				outcomeFacts <- fmt.Errorf("%s: %w", outcome, err)
				return
			}
			outcomeFacts <- nil
		}()
	}
	for range len(destinationLegs) - 1 {
		if err := <-outcomeFacts; err != nil {
			t.Fatalf("project concurrent outbound outcome: %v", err)
		}
	}
	processAllCommands(t, calling)

	var answeredDestination legFact
	for _, leg := range destinationLegs {
		if callOutcome[leg.callID] == "answer" {
			answeredDestination = leg
			break
		}
	}
	var answeredStaff legFact
	for _, leg := range staffLegs {
		if leg.callID == answeredDestination.callID {
			answeredStaff = leg
			break
		}
	}
	for _, fact := range []humancalling.ProviderFact{
		{EventID: "outbound-burst-destination-bridged", Type: humancalling.FactCallBridged,
			OccurredAt: now.Add(5 * time.Second), CallControlID: answeredDestination.controlID,
			CallLegID: answeredDestination.legID, CallSessionID: answeredDestination.sessionID},
		{EventID: "outbound-burst-staff-bridged", Type: humancalling.FactCallBridged,
			OccurredAt: now.Add(5 * time.Second), CallControlID: answeredStaff.controlID,
			CallLegID: answeredStaff.legID, CallSessionID: answeredStaff.sessionID},
		{EventID: "outbound-burst-destination-ended", Type: humancalling.FactCallHangup,
			OccurredAt: now.Add(6 * time.Second), CallControlID: answeredDestination.controlID,
			CallLegID: answeredDestination.legID, CallSessionID: answeredDestination.sessionID,
			HangupCause: "NORMAL_CLEARING", TerminationSource: "DESTINATION"},
	} {
		if err := calling.ApplyProviderFact(ctx, fact); err != nil {
			t.Fatalf("complete answered outbound Call: %v", err)
		}
	}
	processAllCommands(t, calling)
	staffHangups := make(chan error, len(staffLegs))
	for _, leg := range staffLegs {
		go func() {
			staffHangups <- calling.ApplyProviderFact(ctx, humancalling.ProviderFact{
				EventID: "outbound-burst-staff-ended-" + leg.callID,
				Type:    humancalling.FactCallHangup, OccurredAt: now.Add(7 * time.Second),
				CallControlID: leg.controlID, CallLegID: leg.legID,
				CallSessionID: leg.sessionID, HangupCause: "NORMAL_CLEARING",
				TerminationSource: "STAFF",
			})
		}()
	}
	for range staffLegs {
		if err := <-staffHangups; err != nil {
			t.Fatalf("complete outbound Staff cleanup: %v", err)
		}
	}
	processAllCommands(t, calling)

	wantTerminal := map[string]string{
		"answer": "ENDED", "busy": "UNANSWERED", "no_answer": "UNANSWERED",
		"call_rejected": "UNANSWERED", "timeout": "UNANSWERED",
	}
	wantProviderTermination := map[string]string{
		"answer": "COMPLETED", "busy": "BUSY", "no_answer": "NO_ANSWER",
		"call_rejected": "DECLINED", "timeout": "NO_ANSWER",
	}
	for callID, outcome := range callOutcome {
		var terminal, providerTermination string
		if err := pool.QueryRow(ctx, `
			SELECT terminal_outcome, provider_termination
			FROM human_calling_calls WHERE id = $1
		`, callID).Scan(&terminal, &providerTermination); err != nil {
			t.Fatalf("read concurrent outbound terminal outcome: %v", err)
		}
		if terminal != wantTerminal[outcome] {
			t.Errorf("concurrent outbound %s terminal = %q, want %q", outcome, terminal, wantTerminal[outcome])
		}
		if providerTermination != wantProviderTermination[outcome] {
			t.Errorf("concurrent outbound %s provider termination = %q, want %q", outcome, providerTermination, wantProviderTermination[outcome])
		}
	}
	var activeLegs, activeCommands int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM human_calling_call_legs
		WHERE call_id = ANY($1::uuid[])
			AND state IN ('PENDING','DIALING','RINGING','BRIDGE_PENDING','BRIDGED')
	`, outboundCallIDs(callOutcome)).Scan(&activeLegs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM human_calling_provider_commands
		WHERE call_id = ANY($1::uuid[]) AND state IN ('PENDING','SENDING')
	`, outboundCallIDs(callOutcome)).Scan(&activeCommands); err != nil {
		t.Fatal(err)
	}
	if activeLegs != 0 || activeCommands != 0 {
		t.Errorf("concurrent outbound cleanup = %d active legs, %d active commands, want 0 and 0", activeLegs, activeCommands)
	}
	t.Logf("local deterministic outbound proof only: concurrent_calls=5 answer=1 busy=1 no_answer=1 rejected=1 timeout=1 active_legs=%d active_commands=%d", activeLegs, activeCommands)
}

func outboundCallIDs(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
