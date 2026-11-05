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

func TestRejectedOutboundBridgeEndsTheCallWithoutVoicemail(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	now := time.Date(2026, time.November, 5, 13, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	accessModule := access.New(db.As(testdb.Portal), clock)
	authorization, staff := provisionConcurrentStaff(t, db.Owner, accessModule, now, "rejected-bridge", 1)
	provider := &recordingProvider{
		dialResults: []humancalling.ProviderResult{
			{CallControlID: "staff-control", CallLegID: "staff-provider-leg"},
			{CallControlID: "destination-control", CallLegID: "destination-provider-leg"},
		},
		actionErrors: map[humancalling.CommandAction][]error{
			humancalling.CommandBridge: {
				fmt.Errorf("%w: synthetic rejected Bridge", humancalling.ErrDefinitiveProviderFailure),
			},
		},
	}
	config := humancalling.Config{
		StaffSIPDomain:         "sip.telnyx.com",
		RingWindowDuration:     20 * time.Second,
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
	}
	calling := humancalling.New(db.As(testdb.Portal), accessModule, provider, config, clock)
	worker := humancalling.New(db.As(testdb.Worker), accessModule, provider, config, clock)
	prepareCredentials(t, worker)
	readyConcurrentStaff(t, calling, staff, "rejected-bridge-browser")
	if err := humancalling.New(db.Owner, accessModule, provider, config, clock).ProvisionLocationVoices(ctx,
		[]humancalling.LocationVoiceProvision{{
			PracticeKey: "rejected-bridge-practice",
			LocationKey: "rejected-bridge-location",
			Number:      "+14843336938", Enabled: true,
		}}); err != nil {
		t.Fatalf("provision outbound caller ID: %v", err)
	}
	call, err := calling.StartOutboundCall(ctx, humancalling.StartOutboundCallCommand{
		Identity: staff[0], SessionID: "rejected-bridge-browser-1",
		IdempotencyKey: "rejected-bridge",
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		Destination:    "+15555550123",
	})
	if err != nil {
		t.Fatalf("start outbound Call: %v", err)
	}
	processAllCommands(t, worker)

	apply := func(fact humancalling.ProviderFact) {
		t.Helper()
		fact.OccurredAt, fact.ConnectionID = now.Add(time.Second), "staff-call-control-connection"
		if err := worker.ApplyProviderFact(ctx, fact); err != nil {
			t.Fatalf("apply %s: %v", fact.EventID, err)
		}
	}
	staffFact := humancalling.ProviderFact{
		CallControlID: "staff-control", CallLegID: "staff-provider-leg", CallSessionID: "staff-session",
		ClientState: provider.last(humancalling.CommandDialOutboundStaff).Payload["client_state"].(string),
	}
	staffFact.EventID, staffFact.Type = "staff-initiated", humancalling.FactCallInitiated
	apply(staffFact)
	staffFact.EventID, staffFact.Type = "staff-answered", humancalling.FactCallAnswered
	apply(staffFact)
	state, _, err := calling.ReadCallingStateConditionally(ctx, staff[0], "")
	if err != nil || len(state.Ringing) != 1 {
		t.Fatalf("read outbound media state: %#v, err = %v", state, err)
	}
	if _, err := calling.ConfirmOutboundMedia(ctx, humancalling.ConfirmOutboundMediaCommand{
		Identity: staff[0], SessionID: "rejected-bridge-browser-1", CallID: call.ID,
		MediaToken: state.Ringing[0].MediaToken,
	}); err != nil {
		t.Fatalf("confirm outbound media: %v", err)
	}
	processAllCommands(t, worker)
	destinationFact := humancalling.ProviderFact{
		CallControlID: "destination-control", CallLegID: "destination-provider-leg", CallSessionID: "destination-session",
		ClientState: provider.last(humancalling.CommandDialOutboundDestination).Payload["client_state"].(string),
	}
	destinationFact.EventID, destinationFact.Type = "destination-initiated", humancalling.FactCallInitiated
	apply(destinationFact)
	destinationFact.EventID, destinationFact.Type = "destination-answered", humancalling.FactCallAnswered
	apply(destinationFact)

	processAllCommands(t, worker)

	if attempts := provider.count(humancalling.CommandBridge); attempts != 1 {
		t.Fatalf("Bridge attempts = %d, want 1", attempts)
	}
	var bridgeState, terminal string
	if err := db.Owner.QueryRow(ctx, `
		SELECT state FROM human_calling_provider_commands WHERE call_id = $1 AND action = 'BRIDGE'
	`, call.ID).Scan(&bridgeState); err != nil {
		t.Fatalf("read Bridge command: %v", err)
	}
	if bridgeState != "FAILED" {
		t.Fatalf("Bridge command state = %s, want FAILED", bridgeState)
	}
	if err := db.Owner.QueryRow(ctx, `
		SELECT COALESCE(terminal_outcome, '') FROM human_calling_calls WHERE id = $1
	`, call.ID).Scan(&terminal); err != nil {
		t.Fatalf("read Call: %v", err)
	}
	if terminal != "UNANSWERED" {
		t.Fatalf("terminal outcome = %q, want UNANSWERED", terminal)
	}
	hungUp := map[string]bool{}
	for _, command := range provider.all(humancalling.CommandHangupLeg) {
		hungUp[command.TargetID] = true
	}
	if !hungUp["staff-control"] || !hungUp["destination-control"] {
		t.Fatalf("Hangup targets = %v, want the staff and destination legs", hungUp)
	}
	if count := provider.count(humancalling.CommandSpeakVoicemail); count != 0 {
		t.Fatalf("voicemail greetings = %d, want none on an outbound Call", count)
	}
}
