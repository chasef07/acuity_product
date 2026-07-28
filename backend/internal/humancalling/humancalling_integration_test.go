package humancalling_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAuthenticatedHandoffCreatesOneCurrentOffer(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	provider := &recordingProvider{}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		StaffSIPDomain:   "sip.telnyx.com",
		OfferDuration:    20 * time.Second,
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		RecordingBucket:  "synthetic-recordings",
	}, func() time.Time { return now })

	command := humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-synthetic",
			PracticeID: authorization.Practice.ID,
		},
		LocationID:     authorization.Locations[0].ID,
		SourceCallID:   "livekit-call-1",
		IdempotencyKey: "handoff-attempt-1",
		Contact: humancalling.ContactContext{
			Phone:          "+15555550100",
			PhoneSource:    "Abita",
			DisplayName:    "Synthetic Caller",
			NameSource:     "Abita",
			TransferReason: "Scheduling help",
			ReasonSource:   "Abita AI",
		},
	}
	first, err := calling.CreateHandoff(context.Background(), command)
	if err != nil {
		t.Fatalf("create handoff: %v", err)
	}
	replayed, err := calling.CreateHandoff(context.Background(), command)
	if err != nil {
		t.Fatalf("replay handoff: %v", err)
	}
	if replayed.ID != first.ID || replayed.SIPDestination != first.SIPDestination {
		t.Fatalf("idempotent handoff changed: first=%#v replayed=%#v", first, replayed)
	}

	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "event-inbound-1",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		CallControlID: "caller-control-1",
		CallLegID:     "caller-leg-1",
		CallSessionID: "caller-session-1",
		To:            first.SIPDestination,
	}); err != nil {
		t.Fatalf("admit synthetic caller: %v", err)
	}
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"browser-session-1",
		false,
	); err != nil {
		t.Fatalf("acquire softphone: %v", err)
	}
	if _, err := calling.SetReadiness(context.Background(), humancalling.ReadinessCommand{
		Identity:        identity,
		SessionID:       "browser-session-1",
		Registered:      true,
		MicrophoneReady: true,
		AudioReady:      true,
		SessionHealthy:  true,
		Available:       true,
	}); err != nil {
		t.Fatalf("establish readiness: %v", err)
	}

	offers, err := calling.ListOffers(context.Background(), identity)
	if err != nil {
		t.Fatalf("list offers: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("offers = %#v, want one", offers)
	}
	if !offers[0].Deadline.Equal(now.Add(20*time.Second)) ||
		offers[0].DisplayName != "Synthetic Caller" ||
		offers[0].Phone != "" {
		t.Fatalf("minimum pre-claim offer = %#v", offers[0])
	}
}

func TestConcurrentHandoffRetriesReplayTheCommittedIdentity(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionStaff(t, accessModule, now)
	calling := humancalling.New(
		pool,
		accessModule,
		&recordingProvider{},
		humancalling.Config{
			HandoffSIPDomain: "synthetic.sip.telnyx.com",
			HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		},
		func() time.Time { return now },
	)
	command := humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-concurrent-handoff",
			PracticeID: authorization.Practice.ID,
		},
		LocationID:     authorization.Locations[0].ID,
		SourceCallID:   "concurrent-handoff-source",
		IdempotencyKey: "concurrent-handoff-idempotency",
		Contact: humancalling.ContactContext{
			DisplayName: "Concurrent Handoff",
			NameSource:  "Abita",
		},
	}
	start := make(chan struct{})
	type outcome struct {
		handoff humancalling.Handoff
		err     error
	}
	outcomes := make(chan outcome, 8)
	for range 8 {
		go func() {
			<-start
			handoff, err := calling.CreateHandoff(context.Background(), command)
			outcomes <- outcome{handoff: handoff, err: err}
		}()
	}
	close(start)
	var expected humancalling.Handoff
	for index := 0; index < 8; index++ {
		result := <-outcomes
		if result.err != nil {
			t.Fatalf("concurrent handoff retry %d: %v", index, result.err)
		}
		if index == 0 {
			expected = result.handoff
			continue
		}
		if result.handoff.ID != expected.ID ||
			result.handoff.SIPDestination != expected.SIPDestination {
			t.Fatalf("handoff retry changed identity: first=%#v got=%#v", expected, result.handoff)
		}
	}
}

func TestSoftphoneLeaseRequiresExplicitTakeover(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, identity := provisionStaff(t, accessModule, now)
	calling := humancalling.New(pool, accessModule, &recordingProvider{}, humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
	}, func() time.Time { return now })

	first, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"browser-session-1",
		false,
	)
	if err != nil || !first.Owner {
		t.Fatalf("first lease = %#v, err = %v", first, err)
	}
	if _, err := calling.SetReadiness(context.Background(), ready(
		identity,
		"browser-session-1",
	)); err != nil {
		t.Fatalf("ready first session: %v", err)
	}

	blocked, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"browser-session-2",
		false,
	)
	if err != nil || blocked.Owner {
		t.Fatalf("second lease without takeover = %#v, err = %v", blocked, err)
	}
	taken, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"browser-session-2",
		true,
	)
	if err != nil || !taken.Owner || taken.Available {
		t.Fatalf("second lease with takeover = %#v, err = %v", taken, err)
	}
	if _, err := calling.SetReadiness(context.Background(), ready(
		identity,
		"browser-session-1",
	)); !errors.Is(err, humancalling.ErrDenied) {
		t.Fatalf("old session readiness error = %v, want denied", err)
	}
	secondReady, err := calling.SetReadiness(context.Background(), ready(
		identity,
		"browser-session-2",
	))
	if err != nil || !secondReady.Available {
		t.Fatalf("new session readiness = %#v, err = %v", secondReady, err)
	}
}

func TestLeaseRenewalDoesNotRefreshStaleReadinessProof(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	calling, identity, offer := readyOfferAt(
		t,
		&recordingProvider{},
		"stale-readiness",
		func() time.Time { return now },
	)

	now = now.Add(16 * time.Second)
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"stale-readiness-browser",
		false,
	); err != nil {
		t.Fatalf("renew stale lease: %v", err)
	}
	result, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"stale-readiness-browser",
		offer.ID,
	)
	if err != nil || result.Status != humancalling.AcceptIneligible {
		t.Fatalf("accept with stale readiness = %#v, err = %v", result, err)
	}

	if _, err := calling.SetReadiness(context.Background(), ready(
		identity,
		"stale-readiness-browser",
	)); err != nil {
		t.Fatalf("refresh readiness: %v", err)
	}
	result, err = calling.AcceptOffer(
		context.Background(),
		identity,
		"stale-readiness-browser",
		offer.ID,
	)
	if err != nil || result.Status != humancalling.Accepted {
		t.Fatalf("accept with current readiness = %#v, err = %v", result, err)
	}
}

func TestActiveCallTakeoverTransfersControlToTheNewLeaseOwner(t *testing.T) {
	provider := &recordingProvider{}
	calling, identity, offer := readyOffer(t, provider, "active-takeover")
	if _, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"active-takeover-browser",
		offer.ID,
	); err != nil {
		t.Fatalf("accept active-takeover offer: %v", err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process active-takeover setup: %v", err)
		}
		if !processed {
			break
		}
	}
	clientState := base64.StdEncoding.EncodeToString([]byte(
		fmt.Sprintf(`{"v":1,"call":"%s","leg":"staff"}`, offer.ID),
	))
	for _, fact := range []humancalling.ProviderFact{
		{
			EventID:       "active-takeover-staff",
			Type:          humancalling.FactCallInitiated,
			OccurredAt:    offer.Deadline.Add(-10 * time.Second),
			CallControlID: "staff-control-1",
			CallLegID:     "staff-leg-1",
			CallSessionID: "active-takeover-provider-session",
			ClientState:   clientState,
		},
		{
			EventID:       "active-takeover-bridge",
			Type:          humancalling.FactCallBridged,
			OccurredAt:    offer.Deadline.Add(-9 * time.Second),
			CallControlID: "staff-control-1",
			CallLegID:     "staff-leg-1",
			CallSessionID: "active-takeover-provider-session",
			ClientState:   clientState,
		},
	} {
		if err := calling.ApplyProviderFact(context.Background(), fact); err != nil {
			t.Fatalf("apply active-takeover fact %s: %v", fact.EventID, err)
		}
	}
	taken, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"active-takeover-new-browser",
		true,
	)
	if err != nil || !taken.Owner || taken.ActiveCallID != offer.ID {
		t.Fatalf("take over active Call: state=%#v err=%v", taken, err)
	}
	if _, err := calling.RequestHangup(
		context.Background(),
		identity,
		"active-takeover-browser",
		offer.ID,
	); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("stale session hangup error = %v, want conflict", err)
	}
	if _, err := calling.RequestHangup(
		context.Background(),
		identity,
		"active-takeover-new-browser",
		offer.ID,
	); err != nil {
		t.Fatalf("new owner hangup: %v", err)
	}
}

func TestCallControlsWaitForAcquireSoftphoneIdentityFenceBeforeLockingCall(t *testing.T) {
	provider := &recordingProvider{}
	calling, identity, offer := readyOffer(t, provider, "control-lock-order")
	sessionID := "control-lock-order-browser"
	if _, err := calling.AcceptOffer(
		context.Background(),
		identity,
		sessionID,
		offer.ID,
	); err != nil {
		t.Fatalf("accept lock-order offer: %v", err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process lock-order setup: %v", err)
		}
		if !processed {
			break
		}
	}
	var clientState string
	provider.mu.Lock()
	for _, command := range provider.commands {
		if command.Action == humancalling.CommandDialStaff {
			clientState, _ = command.Payload["client_state"].(string)
			break
		}
	}
	provider.mu.Unlock()
	for _, fact := range []humancalling.ProviderFact{
		{
			EventID:       "control-lock-order-staff",
			Type:          humancalling.FactCallInitiated,
			OccurredAt:    offer.Deadline.Add(-10 * time.Second),
			CallControlID: "staff-control-1",
			CallLegID:     "staff-leg-1",
			CallSessionID: "control-lock-order-provider-session",
			ClientState:   clientState,
		},
		{
			EventID:       "control-lock-order-bridge",
			Type:          humancalling.FactCallBridged,
			OccurredAt:    offer.Deadline.Add(-9 * time.Second),
			CallControlID: "staff-control-1",
			CallLegID:     "staff-leg-1",
			CallSessionID: "control-lock-order-provider-session",
			ClientState:   clientState,
		},
	} {
		if err := calling.ApplyProviderFact(context.Background(), fact); err != nil {
			t.Fatalf("connect lock-order Call: %v", err)
		}
	}
	pool, err := pgxpool.New(context.Background(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("open lock-order observer pool: %v", err)
	}
	t.Cleanup(pool.Close)
	assertCallControlWaitsForIdentityFence(
		t,
		pool,
		identity,
		offer.ID,
		func() error {
			_, err := calling.RequestHangup(
				context.Background(),
				identity,
				sessionID,
				offer.ID,
			)
			return err
		},
	)
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_calls
		SET state = 'NEEDS_DISPOSITION', ended_at = $2, updated_at = $2
		WHERE id = $1
	`, offer.ID, offer.Deadline.Add(-8*time.Second)); err != nil {
		t.Fatalf("prepare disposition lock-order Call: %v", err)
	}
	assertCallControlWaitsForIdentityFence(
		t,
		pool,
		identity,
		offer.ID,
		func() error {
			_, err := calling.RecordDisposition(
				context.Background(),
				identity,
				sessionID,
				offer.ID,
				humancalling.DispositionResolved,
			)
			return err
		},
	)
}

func TestAmbiguousHangupRetriesOneIdentityAndConvergesFromProviderState(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	notAlive := false
	provider := &recordingProvider{
		hangupErrors: []error{humancalling.ErrAmbiguousEffect, nil},
		callAlive:    &notAlive,
	}
	calling, identity, offer := readyOfferAt(
		t,
		provider,
		"ambiguous-hangup",
		func() time.Time { return now },
	)
	if _, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"ambiguous-hangup-browser",
		offer.ID,
	); err != nil {
		t.Fatalf("accept ambiguous-hangup offer: %v", err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process ambiguous-hangup setup: %v", err)
		}
		if !processed {
			break
		}
	}
	clientState := base64.StdEncoding.EncodeToString([]byte(
		fmt.Sprintf(`{"v":1,"call":"%s","leg":"staff"}`, offer.ID),
	))
	for _, fact := range []humancalling.ProviderFact{
		{
			EventID:       "ambiguous-hangup-staff",
			Type:          humancalling.FactCallInitiated,
			OccurredAt:    now.Add(time.Second),
			CallControlID: "staff-control-1",
			CallLegID:     "staff-leg-1",
			CallSessionID: "ambiguous-hangup-provider-session",
			ClientState:   clientState,
		},
		{
			EventID:       "ambiguous-hangup-bridge",
			Type:          humancalling.FactCallBridged,
			OccurredAt:    now.Add(2 * time.Second),
			CallControlID: "staff-control-1",
			CallLegID:     "staff-leg-1",
			CallSessionID: "ambiguous-hangup-provider-session",
			ClientState:   clientState,
		},
	} {
		if err := calling.ApplyProviderFact(context.Background(), fact); err != nil {
			t.Fatalf("connect ambiguous-hangup Call: %v", err)
		}
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process post-bridge command: %v", err)
		}
		if !processed {
			break
		}
	}
	now = now.Add(3 * time.Second)
	if _, err := calling.RequestHangup(
		context.Background(),
		identity,
		"ambiguous-hangup-browser",
		offer.ID,
	); err != nil {
		t.Fatalf("commit ambiguous Hangup: %v", err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("execute ambiguous Hangup: processed=%t err=%v", processed, err)
	}
	now = now.Add(6 * time.Second)
	if err := calling.RecoverInterruptedCommands(context.Background()); err != nil {
		t.Fatalf("schedule exact-ID Hangup reconciliation: %v", err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("retry exact-ID Hangup: processed=%t err=%v", processed, err)
	}
	now = now.Add(6 * time.Second)
	if reconciled, err := calling.ReconcileConfirmedHangups(context.Background()); err != nil ||
		reconciled != 1 {
		t.Fatalf("reconcile confirmed Hangup: count=%d err=%v", reconciled, err)
	}
	ended, err := calling.ReadCall(context.Background(), identity, offer.ID)
	if err != nil || ended.State != humancalling.CallNeedsDisposition {
		t.Fatalf("Hangup without webhook = %#v, err = %v", ended, err)
	}
	provider.mu.Lock()
	hangupIDs := []string{}
	for _, command := range provider.commands {
		if command.Action == humancalling.CommandHangup {
			hangupIDs = append(hangupIDs, command.ID)
		}
	}
	provider.mu.Unlock()
	if len(hangupIDs) != 2 || hangupIDs[0] != hangupIDs[1] {
		t.Fatalf("Hangup reconciliation identities = %#v", hangupIDs)
	}
}

func TestConcurrentAcceptsCommitOneClaimantAndOneDial(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	provisioned, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "slice-2-claim-test",
		Practices: []access.PracticeProvision{{
			Key:       "claim-practice",
			Name:      "Claim Practice",
			Locations: []access.LocationProvision{{Key: "claim-location", Name: "Claim Location"}},
			Invitations: []access.InvitationProvision{
				{
					Key:           "claim-staff-one",
					Email:         "one@claim.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(time.Hour),
				},
				{
					Key:           "claim-staff-two",
					Email:         "two@claim.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(time.Hour),
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("provision claim fixture: %v", err)
	}
	identities := []access.Identity{
		{Subject: "claim-one", Email: "one@claim.test", EmailVerified: true},
		{Subject: "claim-two", Email: "two@claim.test", EmailVerified: true},
	}
	authorizations := make([]access.Authorization, len(identities))
	for index := range identities {
		authorizations[index], err = accessModule.AcceptInvitation(
			context.Background(),
			identities[index],
			provisioned.Invitations[index].Token,
		)
		if err != nil {
			t.Fatalf("accept claim invitation %d: %v", index, err)
		}
	}

	provider := &recordingProvider{}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		StaffSIPDomain:   "sip.telnyx.com",
		OfferDuration:    20 * time.Second,
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		RecordingBucket:  "synthetic-recordings",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	handoff, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-synthetic",
			PracticeID: authorizations[0].Practice.ID,
		},
		LocationID:     authorizations[0].Locations[0].ID,
		SourceCallID:   "claim-source-call",
		IdempotencyKey: "claim-idempotency",
		Contact: humancalling.ContactContext{
			Phone:          "+15555550123",
			PhoneSource:    "Abita",
			DisplayName:    "Concurrent Caller",
			NameSource:     "Abita",
			TransferReason: "Concurrent claim proof",
			ReasonSource:   "Abita AI",
		},
	})
	if err != nil {
		t.Fatalf("create claim handoff: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "claim-inbound-event",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		CallControlID: "claim-caller-control",
		CallLegID:     "claim-caller-leg",
		CallSessionID: "claim-session",
		To:            handoff.SIPDestination,
	}); err != nil {
		t.Fatalf("admit claim caller: %v", err)
	}
	for index, identity := range identities {
		sessionID := "claim-browser-" + string(rune('1'+index))
		if _, err := calling.AcquireSoftphone(
			context.Background(),
			identity,
			sessionID,
			false,
		); err != nil {
			t.Fatalf("acquire claim softphone %d: %v", index, err)
		}
		if _, err := calling.SetReadiness(context.Background(), humancalling.ReadinessCommand{
			Identity:        identity,
			SessionID:       sessionID,
			Registered:      true,
			MicrophoneReady: true,
			AudioReady:      true,
			SessionHealthy:  true,
			Available:       true,
		}); err != nil {
			t.Fatalf("establish claim readiness %d: %v", index, err)
		}
	}
	offers, err := calling.ListOffers(context.Background(), identities[0])
	if err != nil || len(offers) != 1 {
		t.Fatalf("claim fixture offers = %#v, err = %v", offers, err)
	}

	type acceptOutcome struct {
		result humancalling.AcceptResult
		err    error
		index  int
	}
	start := make(chan struct{})
	outcomes := make(chan acceptOutcome, 2)
	for index, identity := range identities {
		index := index
		identity := identity
		go func() {
			<-start
			result, err := calling.AcceptOffer(
				context.Background(),
				identity,
				"claim-browser-"+string(rune('1'+index)),
				offers[0].ID,
			)
			outcomes <- acceptOutcome{result: result, err: err, index: index}
		}()
	}
	close(start)

	statuses := map[humancalling.AcceptStatus]int{}
	var winner access.Identity
	var winnerSession string
	for range identities {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Fatalf("accept offer: %v", outcome.err)
		}
		statuses[outcome.result.Status]++
		if outcome.result.Status == humancalling.Accepted {
			winner = identities[outcome.index]
			winnerSession = "claim-browser-" + string(rune('1'+outcome.index))
		}
	}
	if statuses[humancalling.Accepted] != 1 || statuses[humancalling.AlreadyClaimed] != 1 {
		t.Fatalf("concurrent accept statuses = %#v", statuses)
	}

	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process durable command: %v", err)
		}
		if !processed {
			break
		}
	}
	if provider.count(humancalling.CommandDialStaff) != 1 {
		t.Fatalf("provider commands = %#v, want one Dial", provider.commands)
	}
	var dialMediaToken string
	var dialClientState string
	provider.mu.Lock()
	for _, command := range provider.commands {
		if command.Action == humancalling.CommandDialStaff {
			destination, _ := command.Payload["to"].(string)
			if !strings.HasPrefix(destination, "sip:sip-acuity-") ||
				!strings.HasSuffix(destination, "@sip.telnyx.com") {
				provider.mu.Unlock()
				t.Fatalf("Dial destination = %q", destination)
			}
			headers, _ := command.Payload["custom_headers"].([]any)
			if len(headers) == 1 {
				header, _ := headers[0].(map[string]any)
				if header["name"] == "X-Acuity-Media-Token" {
					dialMediaToken, _ = header["value"].(string)
				}
			}
			dialClientState, _ = command.Payload["client_state"].(string)
		}
	}
	provider.mu.Unlock()
	if dialMediaToken == "" || dialClientState == "" {
		t.Fatalf("Dial omitted opaque correlation: %#v", provider.commands)
	}

	connecting, err := calling.ReadCall(context.Background(), winner, offers[0].ID)
	if err != nil {
		t.Fatalf("read connecting Call: %v", err)
	}
	if connecting.State != humancalling.CallConnecting || connecting.Phone != "+15555550123" {
		t.Fatalf("connecting Call = %#v", connecting)
	}
	if connecting.ExpectedMediaToken != dialMediaToken {
		t.Fatalf(
			"expected media token = %q, want durable Dial token %q",
			connecting.ExpectedMediaToken,
			dialMediaToken,
		)
	}
	clientState := dialClientState
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "claim-staff-initiated-event",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now.Add(time.Second),
		CallControlID: "staff-control-1",
		CallLegID:     "staff-leg-1",
		CallSessionID: "claim-session",
		ClientState:   clientState,
	}); err != nil {
		t.Fatalf("apply initiated staff leg: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "claim-staff-answered-event",
		Type:          humancalling.FactCallAnswered,
		OccurredAt:    now.Add(1500 * time.Millisecond),
		CallControlID: "staff-control-1",
		CallLegID:     "staff-leg-1",
		CallSessionID: "claim-session",
		ClientState:   clientState,
	}); err != nil {
		t.Fatalf("apply answered staff leg: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "claim-bridge-event",
		Type:          humancalling.FactCallBridged,
		OccurredAt:    now.Add(2 * time.Second),
		CallControlID: "staff-control-1",
		CallLegID:     "staff-leg-1",
		CallSessionID: "claim-session",
	}); err != nil {
		t.Fatalf("apply bridge evidence: %v", err)
	}
	callerClientState := base64.StdEncoding.EncodeToString([]byte(
		fmt.Sprintf(`{"v":1,"call":"%s","leg":"caller"}`, offers[0].ID),
	))
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "claim-caller-bridge-event",
		Type:          humancalling.FactCallBridged,
		OccurredAt:    now.Add(2 * time.Second),
		CallControlID: "claim-caller-control",
		CallLegID:     "claim-caller-leg",
		CallSessionID: "claim-session",
		ClientState:   callerClientState,
	}); err != nil {
		t.Fatalf("apply caller-leg bridge evidence: %v", err)
	}
	connected, err := calling.ReadCall(context.Background(), winner, offers[0].ID)
	if err != nil {
		t.Fatalf("read connected Call: %v", err)
	}
	if connected.State != humancalling.CallConnected ||
		connected.WinnerSubject != winner.Subject ||
		connected.Recording.State != humancalling.RecordingIntended {
		t.Fatalf("provider-confirmed Call = %#v", connected)
	}
	processed, err := calling.ProcessNextCommand(context.Background())
	if err != nil || !processed {
		t.Fatalf("process recording command: processed=%t err=%v", processed, err)
	}
	if provider.count(humancalling.CommandStartRecording) != 1 {
		t.Fatalf("recording commands = %#v", provider.commands)
	}
	provider.mu.Lock()
	for _, command := range provider.commands {
		if command.Action != humancalling.CommandStartRecording {
			continue
		}
		customName, _ := command.Payload["custom_file_name"].(string)
		if customName != "call-"+strings.ReplaceAll(offers[0].ID, "-", "") {
			provider.mu.Unlock()
			t.Fatalf("recording custom filename = %q", customName)
		}
	}
	provider.mu.Unlock()
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:            "claim-recording-event",
		Type:               humancalling.FactRecordingSaved,
		OccurredAt:         now.Add(4 * time.Second),
		CallControlID:      "staff-control-1",
		CallLegID:          "staff-leg-1",
		CallSessionID:      "claim-session",
		RecordingID:        "recording-1",
		RecordingBucket:    "synthetic-recordings",
		RecordingObjectKey: "synthetic/call.wav",
	}); err != nil {
		t.Fatalf("apply recording evidence: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "claim-hangup-event",
		Type:          humancalling.FactCallHangup,
		OccurredAt:    now.Add(10 * time.Second),
		CallControlID: "staff-control-1",
		CallLegID:     "staff-leg-1",
		CallSessionID: "claim-session",
		HangupCause:   "normal_clearing",
	}); err != nil {
		t.Fatalf("apply hangup evidence: %v", err)
	}
	ended, err := calling.ReadCall(context.Background(), winner, offers[0].ID)
	if err != nil {
		t.Fatalf("read ended Call: %v", err)
	}
	if ended.State != humancalling.CallNeedsDisposition ||
		ended.Recording.State != humancalling.RecordingReady ||
		ended.ExpectedMediaToken != "" {
		t.Fatalf("ended Call = %#v", ended)
	}
	resolved, err := calling.RecordDisposition(
		context.Background(),
		winner,
		winnerSession,
		offers[0].ID,
		humancalling.DispositionResolved,
	)
	if err != nil {
		t.Fatalf("record disposition: %v", err)
	}
	if resolved.State != humancalling.CallResolved {
		t.Fatalf("resolved Call = %#v", resolved)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "claim-delayed-recording-error",
		Type:          humancalling.FactRecordingError,
		OccurredAt:    now.Add(3 * time.Second),
		CallControlID: "staff-control-1",
		CallLegID:     "staff-leg-1",
		CallSessionID: "claim-session",
		RecordingID:   "recording-1",
	}); err != nil {
		t.Fatalf("apply older recording error after READY: %v", err)
	}
	stillReady, err := calling.ReadCall(context.Background(), winner, offers[0].ID)
	if err != nil || stillReady.Recording.State != humancalling.RecordingReady {
		t.Fatalf("arrival-order-independent recording = %#v, err = %v", stillReady, err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "claim-delayed-staff-answered-after-disposition",
		Type:          humancalling.FactCallAnswered,
		OccurredAt:    now.Add(1500 * time.Millisecond),
		CallControlID: "staff-control-1",
		CallLegID:     "staff-leg-1",
		CallSessionID: "claim-session",
		ClientState:   clientState,
	}); err != nil {
		t.Fatalf("fold delayed staff fact after disposition: %v", err)
	}
	stale, err := calling.AcceptOffer(
		context.Background(),
		winner,
		winnerSession,
		offers[0].ID,
	)
	if err != nil ||
		stale.Status != humancalling.AlreadyClaimed ||
		stale.State != humancalling.CallResolved {
		t.Fatalf("stale terminal acceptance = %#v, err = %v", stale, err)
	}
}

func ready(identity access.Identity, sessionID string) humancalling.ReadinessCommand {
	return humancalling.ReadinessCommand{
		Identity:        identity,
		SessionID:       sessionID,
		Registered:      true,
		MicrophoneReady: true,
		AudioReady:      true,
		SessionHealthy:  true,
		Available:       true,
	}
}

func TestAmbiguousCallerAnswerWaitsForCallerAnswerEvidence(t *testing.T) {
	provider := &recordingProvider{answerError: humancalling.ErrAmbiguousEffect}
	calling, identity, offer := readyOffer(t, provider, "answer-reconcile")

	processed, err := calling.ProcessNextCommand(context.Background())
	if err != nil || !processed {
		t.Fatalf("process ambiguous caller Answer: processed=%t err=%v", processed, err)
	}
	reconciling, err := calling.ListOffers(context.Background(), identity)
	if err != nil || len(reconciling) != 0 {
		t.Fatalf("ambiguous caller Answer offers = %#v, err = %v", reconciling, err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || processed {
		t.Fatalf("dependent caller command escaped reconciliation: processed=%t err=%v", processed, err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "answer-reconcile-caller-answered",
		Type:          humancalling.FactCallAnswered,
		OccurredAt:    offer.Deadline.Add(-10 * time.Second),
		CallControlID: "answer-reconcile-caller-control",
		CallLegID:     "answer-reconcile-caller-leg",
		CallSessionID: "answer-reconcile-provider-session",
	}); err != nil {
		t.Fatalf("reconcile caller Answer: %v", err)
	}
	reopened, err := calling.ListOffers(context.Background(), identity)
	if err != nil || len(reopened) != 1 || reopened[0].ID != offer.ID {
		t.Fatalf("reconciled caller offer = %#v, err = %v", reopened, err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("process ringback after Answer evidence: processed=%t err=%v", processed, err)
	}
}

func TestWorkerNormalizesQueuedLegacyAnswerBeforeProviderRequest(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	provider := &recordingProvider{}
	calling := humancalling.New(
		pool,
		nil,
		provider,
		humancalling.Config{},
		func() time.Time { return now },
	)
	const commandID = "00000000-0000-0000-0000-000000000101"
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_provider_commands (
			id, action, target_id, payload, next_attempt_at
		)
		VALUES (
			$1, 'ANSWER_CALLER', 'legacy-caller-control',
			'{"client_state":"legacy-client-state"}'::jsonb, $2
		)
	`, commandID, now); err != nil {
		t.Fatalf("insert legacy Answer command: %v", err)
	}

	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("process legacy Answer command: processed=%t err=%v", processed, err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.commands) != 1 ||
		provider.commands[0].Payload["transcription"] != false {
		t.Fatalf("normalized provider command = %#v", provider.commands)
	}
	var persisted map[string]any
	if err := pool.QueryRow(context.Background(), `
		SELECT payload
		FROM human_calling_provider_commands
		WHERE id = $1
	`, commandID).Scan(&persisted); err != nil {
		t.Fatalf("read normalized Answer command: %v", err)
	}
	if persisted["transcription"] != false {
		t.Fatalf("persisted Answer payload = %#v", persisted)
	}
}

func TestAcceptedCallCannotDialAfterCallerAnswerDefinitivelyFails(t *testing.T) {
	provider := &recordingProvider{
		answerError: fmt.Errorf(
			"%w: caller leg no longer exists",
			humancalling.ErrDefinitiveProviderFailure,
		),
	}
	calling, identity, offer := readyOffer(t, provider, "accepted-answer-failure")
	if _, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"accepted-answer-failure-browser",
		offer.ID,
	); err != nil {
		t.Fatalf("accept before Answer result: %v", err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process failed accepted Answer: %v", err)
		}
		if !processed {
			break
		}
	}
	call, err := calling.ReadCall(context.Background(), identity, offer.ID)
	if err != nil || call.State != humancalling.CallUnanswered {
		t.Fatalf("definitive accepted Answer failure = %#v, err = %v", call, err)
	}
	if provider.count(humancalling.CommandDialStaff) != 0 {
		t.Fatalf("Dial executed after failed Answer: %#v", provider.commands)
	}
}

func TestCallerHangupFencesPendingSetupCommands(t *testing.T) {
	provider := &recordingProvider{}
	calling, identity, offer := readyOffer(t, provider, "stale-setup")
	if _, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"stale-setup-browser",
		offer.ID,
	); err != nil {
		t.Fatalf("accept stale-setup offer: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "stale-setup-caller-hangup",
		Type:          humancalling.FactCallHangup,
		OccurredAt:    offer.Deadline.Add(-10 * time.Second),
		CallControlID: "stale-setup-caller-control",
		CallLegID:     "stale-setup-caller-leg",
		CallSessionID: "stale-setup-provider-session",
		HangupCause:   "caller_hangup",
	}); err != nil {
		t.Fatalf("terminate before setup commands: %v", err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("fence terminal setup command: %v", err)
		}
		if !processed {
			break
		}
	}
	if provider.count(humancalling.CommandAnswerCaller) != 0 ||
		provider.count(humancalling.CommandStartRingback) != 0 ||
		provider.count(humancalling.CommandDialStaff) != 0 {
		t.Fatalf("terminal Call executed setup commands: %#v", provider.commands)
	}
}

func TestAmbiguousDialReconcilesFromExpectedProviderFactWithoutRedial(t *testing.T) {
	provider := &recordingProvider{dialError: humancalling.ErrAmbiguousEffect}
	calling, identity, offer := readyOffer(t, provider, "ambiguous")
	result, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"ambiguous-browser",
		offer.ID,
	)
	if err != nil || result.Status != humancalling.Accepted {
		t.Fatalf("accept ambiguous offer: result=%#v err=%v", result, err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process ambiguous command: %v", err)
		}
		if !processed {
			break
		}
	}
	reconciling, err := calling.ReadCall(context.Background(), identity, offer.ID)
	if err != nil {
		t.Fatalf("read reconciling Call: %v", err)
	}
	if reconciling.State != humancalling.CallReconciling {
		t.Fatalf("ambiguous Dial Call = %#v", reconciling)
	}
	if provider.count(humancalling.CommandDialStaff) != 1 {
		t.Fatalf("ambiguous Dial count = %d", provider.count(humancalling.CommandDialStaff))
	}
	if !provider.ordered(
		humancalling.CommandAnswerCaller,
		humancalling.CommandStartRingback,
	) {
		t.Fatalf("caller commands were not Answer then ringback: %#v", provider.commands)
	}

	clientState := base64.StdEncoding.EncodeToString([]byte(
		fmt.Sprintf(`{"v":1,"call":"%s","leg":"staff"}`, offer.ID),
	))
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "ambiguous-staff-initiated-event",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    offer.Deadline.Add(-11 * time.Second),
		CallControlID: "ambiguous-staff-control",
		CallLegID:     "ambiguous-staff-leg",
		CallSessionID: "ambiguous-provider-session",
		ClientState:   clientState,
	}); err != nil {
		t.Fatalf("reconcile ambiguous Dial from initiated staff leg: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "ambiguous-bridge-event",
		Type:          humancalling.FactCallBridged,
		OccurredAt:    offer.Deadline.Add(-10 * time.Second),
		CallControlID: "ambiguous-staff-control",
		CallLegID:     "ambiguous-staff-leg",
		CallSessionID: "ambiguous-provider-session",
		ClientState:   clientState,
	}); err != nil {
		t.Fatalf("reconcile ambiguous bridge: %v", err)
	}
	connected, err := calling.ReadCall(context.Background(), identity, offer.ID)
	if err != nil {
		t.Fatalf("read reconciled Call: %v", err)
	}
	if connected.State != humancalling.CallConnected ||
		connected.ExpectedStaffLegID != "ambiguous-staff-leg" ||
		provider.count(humancalling.CommandDialStaff) != 1 {
		t.Fatalf("reconciled Call = %#v, commands = %#v", connected, provider.commands)
	}
}

func TestDefinitivePreBridgeFailureReopensOnlyTheOriginalOffer(t *testing.T) {
	provider := &recordingProvider{
		dialError: fmt.Errorf("%w: destination rejected", humancalling.ErrDefinitiveProviderFailure),
	}
	calling, identity, offer := readyOffer(t, provider, "definitive")
	result, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"definitive-browser",
		offer.ID,
	)
	if err != nil || result.Status != humancalling.Accepted {
		t.Fatalf("accept definitive-failure offer: result=%#v err=%v", result, err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process definitive command: %v", err)
		}
		if !processed {
			break
		}
	}
	reopened, err := calling.ListOffers(context.Background(), identity)
	if err != nil {
		t.Fatalf("list reopened offer: %v", err)
	}
	if len(reopened) != 1 ||
		reopened[0].ID != offer.ID ||
		!reopened[0].Deadline.Equal(offer.Deadline) ||
		provider.count(humancalling.CommandDialStaff) != 1 {
		t.Fatalf("reopened offers = %#v, commands = %#v", reopened, provider.commands)
	}
}

func TestWorkerRestartRepairsInterruptedDialWithTheSameProviderIdentity(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	provider := &interruptibleDialProvider{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	calling, identity, offer := readyOfferAt(
		t,
		provider,
		"interrupted",
		func() time.Time { return now },
	)
	if _, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"interrupted-browser",
		offer.ID,
	); err != nil {
		t.Fatalf("accept interrupted offer: %v", err)
	}
	for range 2 {
		if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
			t.Fatalf("process pre-Dial command: processed=%t err=%v", processed, err)
		}
	}
	result := make(chan error, 1)
	go func() {
		_, err := calling.ProcessNextCommand(context.Background())
		result <- err
	}()
	<-provider.entered
	now = now.Add(31 * time.Second)
	if err := calling.RecoverInterruptedCommands(context.Background()); err != nil {
		t.Fatalf("recover interrupted Dial: %v", err)
	}
	close(provider.release)
	if err := <-result; err != nil {
		t.Fatalf("finish interrupted worker: %v", err)
	}
	call, err := calling.ReadCall(context.Background(), identity, offer.ID)
	if err != nil {
		t.Fatalf("read recovered Call: %v", err)
	}
	if call.State != humancalling.CallConnecting {
		t.Fatalf("recovered Call = %#v", call)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("safe same-ID Dial recovery: processed=%t err=%v", processed, err)
	}
	call, err = calling.ReadCall(context.Background(), identity, offer.ID)
	if err != nil || call.State != humancalling.CallConnecting ||
		call.ExpectedStaffLegID != "late-staff-leg" {
		t.Fatalf("repaired Call = %#v, err = %v", call, err)
	}
	if provider.dialCount != 2 ||
		len(provider.commandIDs) != 2 ||
		provider.commandIDs[0] != provider.commandIDs[1] {
		t.Fatalf(
			"Dial attempts = %d ids = %#v, want two attempts with one identity",
			provider.dialCount,
			provider.commandIDs,
		)
	}
}

func TestBridgeBeforeDialResponseKeepsTheProviderConfirmedWinner(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	provider := &interruptibleDialProvider{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	calling, identity, offer := readyOfferAt(
		t,
		provider,
		"bridge-before-dial-result",
		func() time.Time { return now },
	)
	if _, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"bridge-before-dial-result-browser",
		offer.ID,
	); err != nil {
		t.Fatalf("accept bridge-before-result offer: %v", err)
	}
	for range 2 {
		if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
			t.Fatalf("process pre-Dial command: processed=%t err=%v", processed, err)
		}
	}
	result := make(chan error, 1)
	go func() {
		_, err := calling.ProcessNextCommand(context.Background())
		result <- err
	}()
	<-provider.entered
	clientState, _ := provider.dialCommand.Payload["client_state"].(string)
	for _, fact := range []humancalling.ProviderFact{
		{
			EventID:       "bridge-before-result-staff-initiated",
			Type:          humancalling.FactCallInitiated,
			OccurredAt:    now.Add(time.Second),
			CallControlID: "late-staff-control",
			CallLegID:     "late-staff-leg",
			CallSessionID: "bridge-before-dial-result-provider-session",
			ClientState:   clientState,
		},
		{
			EventID:       "bridge-before-result-bridge",
			Type:          humancalling.FactCallBridged,
			OccurredAt:    now.Add(2 * time.Second),
			CallControlID: "late-staff-control",
			CallLegID:     "late-staff-leg",
			CallSessionID: "bridge-before-dial-result-provider-session",
			ClientState:   clientState,
		},
	} {
		if err := calling.ApplyProviderFact(context.Background(), fact); err != nil {
			t.Fatalf("apply fact before Dial response: %v", err)
		}
	}
	close(provider.release)
	if err := <-result; err != nil {
		t.Fatalf("finish Dial after bridge: %v", err)
	}
	call, err := calling.ReadCall(context.Background(), identity, offer.ID)
	if err != nil || call.State != humancalling.CallConnected ||
		call.ExpectedStaffLegID != "late-staff-leg" {
		t.Fatalf("bridge-before-result Call = %#v, err = %v", call, err)
	}
	if len(provider.hangupTargets) != 0 {
		t.Fatalf("winning leg cleanup commands = %#v", provider.hangupTargets)
	}
}

func TestLateSuccessfulDialAfterCallerHangupCleansUpTheExactStaffLeg(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	provider := &interruptibleDialProvider{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	calling, identity, offer := readyOfferAt(
		t,
		provider,
		"late-dial-cleanup",
		func() time.Time { return now },
	)
	if _, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"late-dial-cleanup-browser",
		offer.ID,
	); err != nil {
		t.Fatalf("accept late-Dial offer: %v", err)
	}
	for range 2 {
		if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
			t.Fatalf("process pre-Dial command: processed=%t err=%v", processed, err)
		}
	}
	result := make(chan error, 1)
	go func() {
		_, err := calling.ProcessNextCommand(context.Background())
		result <- err
	}()
	<-provider.entered
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "late-dial-caller-hangup",
		Type:          humancalling.FactCallHangup,
		OccurredAt:    now.Add(time.Second),
		CallControlID: "late-dial-cleanup-caller-control",
		CallLegID:     "late-dial-cleanup-caller-leg",
		CallSessionID: "late-dial-cleanup-provider-session",
		HangupCause:   "caller_hangup",
	}); err != nil {
		t.Fatalf("terminate caller while Dial is in flight: %v", err)
	}
	close(provider.release)
	if err := <-result; err != nil {
		t.Fatalf("finish late successful Dial: %v", err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("process exact late-leg cleanup: processed=%t err=%v", processed, err)
	}
	call, err := calling.ReadCall(context.Background(), identity, offer.ID)
	if err != nil || call.State != humancalling.CallUnanswered {
		t.Fatalf("late-Dial terminal Call = %#v, err = %v", call, err)
	}
	if len(provider.hangupTargets) != 1 ||
		provider.hangupTargets[0] != "late-staff-control" {
		t.Fatalf("late-Dial cleanup targets = %#v", provider.hangupTargets)
	}
}

func TestDelayedHistoricalWinnerDoesNotConflictWithANewerActiveCall(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	provisioned, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "historical-winner-test",
		Practices: []access.PracticeProvision{{
			Key:       "historical-practice",
			Name:      "Historical Practice",
			Locations: []access.LocationProvision{{Key: "historical-location", Name: "Historical Location"}},
			Invitations: []access.InvitationProvision{
				{
					Key:           "historical-staff-one",
					Email:         "one@historical.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(time.Hour),
				},
				{
					Key:           "historical-staff-two",
					Email:         "two@historical.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(time.Hour),
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("provision historical-winner fixture: %v", err)
	}
	identities := []access.Identity{
		{Subject: "historical-one", Email: "one@historical.test", EmailVerified: true},
		{Subject: "historical-two", Email: "two@historical.test", EmailVerified: true},
	}
	authorizations := make([]access.Authorization, len(identities))
	for index := range identities {
		authorizations[index], err = accessModule.AcceptInvitation(
			context.Background(),
			identities[index],
			provisioned.Invitations[index].Token,
		)
		if err != nil {
			t.Fatalf("accept historical invitation %d: %v", index, err)
		}
	}
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{
		{CallControlID: "historical-a1-control", CallLegID: "historical-a1-leg"},
		{CallControlID: "historical-b-control", CallLegID: "historical-b-leg"},
		{CallControlID: "historical-a2-control", CallLegID: "historical-a2-leg"},
	}}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		OfferDuration:    20 * time.Second,
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		RecordingBucket:  "synthetic-recordings",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	for index, identity := range identities {
		sessionID := fmt.Sprintf("historical-browser-%d", index+1)
		if _, err := calling.AcquireSoftphone(
			context.Background(),
			identity,
			sessionID,
			false,
		); err != nil {
			t.Fatalf("acquire historical softphone %d: %v", index, err)
		}
		if _, err := calling.SetReadiness(
			context.Background(),
			ready(identity, sessionID),
		); err != nil {
			t.Fatalf("ready historical softphone %d: %v", index, err)
		}
	}
	admit := func(key string) string {
		t.Helper()
		handoff, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
			Service: humancalling.ServiceIdentity{
				Subject:    "abita-historical",
				PracticeID: authorizations[0].Practice.ID,
			},
			LocationID:     authorizations[0].Locations[0].ID,
			SourceCallID:   key + "-source",
			IdempotencyKey: key + "-idempotency",
			Contact: humancalling.ContactContext{
				DisplayName:    key,
				NameSource:     "Abita",
				TransferReason: "Historical winner proof",
				ReasonSource:   "Abita AI",
			},
		})
		if err != nil {
			t.Fatalf("create %s handoff: %v", key, err)
		}
		if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
			EventID:       key + "-initiated",
			Type:          humancalling.FactCallInitiated,
			OccurredAt:    now,
			CallControlID: key + "-caller-control",
			CallLegID:     key + "-caller-leg",
			CallSessionID: key + "-session",
			To:            handoff.SIPDestination,
		}); err != nil {
			t.Fatalf("admit %s Call: %v", key, err)
		}
		var callID string
		if err := pool.QueryRow(context.Background(), `
			SELECT id::text
			FROM human_calling_calls
			WHERE caller_call_control_id = $1
		`, key+"-caller-control").Scan(&callID); err != nil {
			t.Fatalf("load %s Call: %v", key, err)
		}
		return callID
	}
	processAll := func() {
		t.Helper()
		for {
			processed, err := calling.ProcessNextCommand(context.Background())
			if err != nil {
				t.Fatalf("process historical provider command: %v", err)
			}
			if !processed {
				return
			}
		}
	}

	callA := admit("historical-a")
	if result, err := calling.AcceptOffer(
		context.Background(),
		identities[0],
		"historical-browser-1",
		callA,
	); err != nil || result.Status != humancalling.Accepted {
		t.Fatalf("accept first historical attempt: result=%#v err=%v", result, err)
	}
	processAll()
	var firstClientState string
	provider.mu.Lock()
	for _, command := range provider.commands {
		if command.Action == humancalling.CommandDialStaff {
			firstClientState, _ = command.Payload["client_state"].(string)
			break
		}
	}
	provider.mu.Unlock()
	now = now.Add(16 * time.Second)
	if count, err := calling.ExpireConnections(context.Background()); err != nil || count != 1 {
		t.Fatalf("expire first historical attempt: count=%d err=%v", count, err)
	}
	processAll()
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "historical-a1-hangup",
		Type:          humancalling.FactCallHangup,
		OccurredAt:    now,
		CallControlID: "historical-a1-control",
		CallLegID:     "historical-a1-leg",
		CallSessionID: "historical-a-session",
		ClientState:   firstClientState,
		HangupCause:   "timeout",
	}); err != nil {
		t.Fatalf("reopen first historical attempt: %v", err)
	}
	for index, identity := range identities {
		if _, err := calling.SetReadiness(
			context.Background(),
			ready(identity, fmt.Sprintf("historical-browser-%d", index+1)),
		); err != nil {
			t.Fatalf("refresh historical readiness %d: %v", index, err)
		}
	}

	callB := admit("historical-b")
	if result, err := calling.AcceptOffer(
		context.Background(),
		identities[0],
		"historical-browser-1",
		callB,
	); err != nil || result.Status != humancalling.Accepted {
		t.Fatalf("accept newer Call: result=%#v err=%v", result, err)
	}
	if result, err := calling.AcceptOffer(
		context.Background(),
		identities[1],
		"historical-browser-2",
		callA,
	); err != nil || result.Status != humancalling.Accepted {
		t.Fatalf("accept second historical attempt: result=%#v err=%v", result, err)
	}
	processAll()
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "historical-a1-delayed-initiated",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now.Add(time.Second),
		CallControlID: "historical-a1-control",
		CallLegID:     "historical-a1-leg",
		CallSessionID: "historical-a-session",
		ClientState:   firstClientState,
	}); err != nil {
		t.Fatalf("project delayed initiated fact: %v", err)
	}
	secondAttempt, err := calling.ReadCall(context.Background(), identities[1], callA)
	if err != nil ||
		secondAttempt.State != humancalling.CallConnecting ||
		secondAttempt.ExpectedStaffLegID != "historical-a2-leg" {
		t.Fatalf("second attempt after stale initiated fact = %#v, err = %v", secondAttempt, err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "historical-a1-delayed-bridge",
		Type:          humancalling.FactCallBridged,
		OccurredAt:    now.Add(-14 * time.Second),
		CallControlID: "historical-a1-control",
		CallLegID:     "historical-a1-leg",
		CallSessionID: "historical-a-session",
		ClientState:   firstClientState,
	}); err != nil {
		t.Fatalf("project delayed historical winner: %v", err)
	}
	historical, err := calling.ReadCall(context.Background(), identities[0], callA)
	if err != nil ||
		historical.State != humancalling.CallNeedsDisposition ||
		historical.WinnerSubject != identities[0].Subject ||
		historical.ClaimantSubject != "" {
		t.Fatalf("historical winner Call = %#v, err = %v", historical, err)
	}
	active, err := calling.ReadCall(context.Background(), identities[0], callB)
	if err != nil || active.State != humancalling.CallConnecting {
		t.Fatalf("newer active Call = %#v, err = %v", active, err)
	}
}

func TestOperationalUserGetsOneManagedCredentialAndLeaseBoundJWT(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, identity := provisionStaff(t, accessModule, now)
	provider := &recordingProvider{}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain:       "synthetic.sip.telnyx.com",
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CredentialConnectionID: "credential-connection-1",
	}, func() time.Time { return now })

	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("reconcile operational credentials: %v", err)
	}
	processed, err := calling.ProcessNextCommand(context.Background())
	if err != nil || !processed {
		t.Fatalf("create managed credential: processed=%t err=%v", processed, err)
	}
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("reconcile existing credential: %v", err)
	}
	if provider.count(humancalling.CommandCreateCredential) != 1 {
		t.Fatalf("credential commands = %#v", provider.commands)
	}

	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"credential-browser",
		false,
	); err != nil {
		t.Fatalf("acquire credential softphone: %v", err)
	}
	token, err := calling.IssueMediaJWT(
		context.Background(),
		identity,
		"credential-browser",
	)
	if err != nil {
		t.Fatalf("issue lease-bound media JWT: %v", err)
	}
	if token.Token != "synthetic-media-jwt" ||
		!token.ExpiresAt.Equal(now.Add(time.Hour)) ||
		provider.count(humancalling.CommandCreateJWT) != 1 {
		t.Fatalf("media token = %#v, commands = %#v", token, provider.commands)
	}
}

func TestConcurrentCredentialReconciliationCommitsOneCreate(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, identity := provisionStaff(t, accessModule, now)
	calling := humancalling.New(
		pool,
		accessModule,
		&recordingProvider{},
		humancalling.Config{
			CredentialConnectionID: "credential-connection-1",
		},
		func() time.Time { return now },
	)
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- calling.ReconcileCredentials(context.Background())
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent credential reconciliation: %v", err)
		}
	}
	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM human_calling_provider_commands
		WHERE user_subject = $1
			AND action = 'CREATE_CREDENTIAL'
			AND state IN ('PENDING', 'SENDING', 'AMBIGUOUS')
	`, identity.Subject).Scan(&count); err != nil {
		t.Fatalf("count credential creates: %v", err)
	}
	if count != 1 {
		t.Fatalf("active credential Create commands = %d, want 1", count)
	}
}

func TestMediaJWTIssuanceRejectsConcurrentMembershipRevocation(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, identity := provisionStaff(t, accessModule, now)
	provider := &blockingJWTProvider{
		jwtEntered: make(chan struct{}),
		jwtRelease: make(chan struct{}),
	}
	var releaseJWT sync.Once
	release := func() {
		releaseJWT.Do(func() { close(provider.jwtRelease) })
	}
	t.Cleanup(release)
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain:       "synthetic.sip.telnyx.com",
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CredentialConnectionID: "credential-connection-1",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"linearized-jwt-browser",
		false,
	); err != nil {
		t.Fatalf("acquire JWT softphone: %v", err)
	}

	issued := make(chan error, 1)
	go func() {
		_, err := calling.IssueMediaJWT(
			context.Background(),
			identity,
			"linearized-jwt-browser",
		)
		issued <- err
	}()
	select {
	case <-provider.jwtEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider JWT request did not start")
	}
	revoked := make(chan error, 1)
	go func() {
		_, err := pool.Exec(context.Background(), `
			UPDATE access_memberships
			SET revoked_at = $1
			WHERE user_subject = $2
		`, now, identity.Subject)
		revoked <- err
	}()
	select {
	case err := <-revoked:
		if err != nil {
			release()
			t.Fatalf("revoke Membership during provider request: %v", err)
		}
	case <-time.After(5 * time.Second):
		release()
		t.Fatal("Membership revocation remained blocked by provider request")
	}
	release()
	if err := <-issued; !errors.Is(err, humancalling.ErrDenied) {
		t.Fatalf("JWT after concurrent revocation error = %v, want denied", err)
	}
	if _, err := calling.IssueMediaJWT(
		context.Background(),
		identity,
		"linearized-jwt-browser",
	); !errors.Is(err, humancalling.ErrDenied) {
		t.Fatalf("post-revocation JWT error = %v, want denied", err)
	}
	if provider.count(humancalling.CommandCreateJWT) != 1 {
		t.Fatalf("JWT provider commands = %#v", provider.commands)
	}
}

func TestMediaJWTIssuanceRejectsConcurrentPlatformOperatorPromotion(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, identity := provisionStaff(t, accessModule, now)
	provider := &blockingJWTProvider{
		jwtEntered: make(chan struct{}),
		jwtRelease: make(chan struct{}),
	}
	var releaseJWT sync.Once
	release := func() {
		releaseJWT.Do(func() { close(provider.jwtRelease) })
	}
	t.Cleanup(release)
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain:       "synthetic.sip.telnyx.com",
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CredentialConnectionID: "credential-connection-1",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"operator-race-jwt-browser",
		false,
	); err != nil {
		t.Fatalf("acquire operator-race softphone: %v", err)
	}

	issued := make(chan error, 1)
	go func() {
		_, err := calling.IssueMediaJWT(
			context.Background(),
			identity,
			"operator-race-jwt-browser",
		)
		issued <- err
	}()
	select {
	case <-provider.jwtEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("operator-race provider JWT request did not start")
	}
	promoted := make(chan error, 1)
	go func() {
		_, err := accessModule.Provision(context.Background(), access.Provisioning{
			Environment:       "test",
			RequestedBy:       "operator-race-test",
			PlatformOperators: []string{identity.Email},
			Practices: []access.PracticeProvision{{
				Key:       "synthetic-practice",
				Name:      "Synthetic Practice",
				Locations: []access.LocationProvision{{Key: "synthetic-location", Name: "Synthetic Location"}},
			}},
		})
		promoted <- err
	}()
	select {
	case err := <-promoted:
		if err != nil {
			release()
			t.Fatalf("promote Platform Operator during provider request: %v", err)
		}
	case <-time.After(5 * time.Second):
		release()
		t.Fatal("Platform Operator promotion remained blocked by provider request")
	}
	release()
	if err := <-issued; !errors.Is(err, humancalling.ErrDenied) {
		t.Fatalf("JWT after concurrent promotion error = %v, want denied", err)
	}
	if _, err := calling.IssueMediaJWT(
		context.Background(),
		identity,
		"operator-race-jwt-browser",
	); !errors.Is(err, humancalling.ErrDenied) {
		t.Fatalf("post-promotion JWT error = %v, want denied", err)
	}
}

func TestConcurrentMediaJWTIssuanceCompletesWithOnePoolConnection(t *testing.T) {
	_ = testdb.Open(t)
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse limited pool config: %v", err)
	}
	config.MaxConns = 1
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open limited pool: %v", err)
	}
	t.Cleanup(pool.Close)

	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, identity := provisionStaff(t, accessModule, now)
	provider := &recordingProvider{}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain:       "synthetic.sip.telnyx.com",
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CredentialConnectionID: "credential-connection-1",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"limited-pool-jwt-browser",
		false,
	); err != nil {
		t.Fatalf("acquire limited-pool softphone: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := calling.IssueMediaJWT(ctx, identity, "limited-pool-jwt-browser")
			results <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent limited-pool JWT issuance: %v", err)
		}
	}
	if provider.count(humancalling.CommandCreateJWT) != 2 {
		t.Fatalf("JWT provider commands = %#v", provider.commands)
	}
}

func TestPlatformOperatorPromotionRevokesOperationalCalling(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	provider := &recordingProvider{}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain:       "synthetic.sip.telnyx.com",
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CredentialConnectionID: "credential-connection-1",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	handoff, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-operator-promotion",
			PracticeID: authorization.Practice.ID,
		},
		LocationID:     authorization.Locations[0].ID,
		SourceCallID:   "operator-promotion-source",
		IdempotencyKey: "operator-promotion-idempotency",
	})
	if err != nil {
		t.Fatalf("create operator promotion handoff: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "operator-promotion-caller",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		CallControlID: "operator-promotion-caller-control",
		CallLegID:     "operator-promotion-caller-leg",
		CallSessionID: "operator-promotion-session",
		To:            handoff.SIPDestination,
	}); err != nil {
		t.Fatalf("admit operator promotion Call: %v", err)
	}
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"operator-promotion-browser",
		false,
	); err != nil {
		t.Fatalf("acquire pre-promotion softphone: %v", err)
	}
	if _, err := calling.SetReadiness(
		context.Background(),
		ready(identity, "operator-promotion-browser"),
	); err != nil {
		t.Fatalf("ready pre-promotion softphone: %v", err)
	}
	offers, err := calling.ListOffers(context.Background(), identity)
	if err != nil || len(offers) != 1 {
		t.Fatalf("pre-promotion offers = %#v, err = %v", offers, err)
	}

	if _, err := pool.Exec(context.Background(), `
		INSERT INTO access_platform_operators (email)
		VALUES ($1)
	`, identity.Email); err != nil {
		t.Fatalf("promote Platform Operator: %v", err)
	}
	if _, err := calling.SetReadiness(
		context.Background(),
		ready(identity, "operator-promotion-browser"),
	); !errors.Is(err, humancalling.ErrDenied) {
		t.Fatalf("operator readiness error = %v, want denied", err)
	}
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"operator-promotion-browser",
		false,
	); !errors.Is(err, humancalling.ErrDenied) {
		t.Fatalf("operator acquisition error = %v, want denied", err)
	}
	if _, err := calling.IssueMediaJWT(
		context.Background(),
		identity,
		"operator-promotion-browser",
	); !errors.Is(err, humancalling.ErrDenied) {
		t.Fatalf("operator media JWT error = %v, want denied", err)
	}
	if _, err := calling.ListOffers(context.Background(), identity); !errors.Is(err, humancalling.ErrDenied) {
		t.Fatalf("operator offers error = %v, want denied", err)
	}
	result, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"operator-promotion-browser",
		offers[0].ID,
	)
	if err != nil || result.Status != humancalling.AcceptIneligible {
		t.Fatalf("operator acceptance = %#v, err = %v", result, err)
	}

	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("reconcile promoted operator credential: %v", err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("disable promoted operator credential: %v", err)
		}
		if !processed {
			break
		}
	}
	var credentialState string
	if err := pool.QueryRow(context.Background(), `
		SELECT state
		FROM human_calling_credentials
		WHERE user_subject = $1
	`, identity.Subject).Scan(&credentialState); err != nil {
		t.Fatalf("read promoted operator credential: %v", err)
	}
	if credentialState != "DISABLED" {
		t.Fatalf("promoted operator credential state = %s, want DISABLED", credentialState)
	}
}

func TestDefinitiveCredentialDisableFailureIsRetriedWhileAccessIsRevoked(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, identity := provisionStaff(t, accessModule, now)
	provider := &recordingProvider{
		disableErrors: []error{
			fmt.Errorf("%w: temporary policy mismatch", humancalling.ErrDefinitiveProviderFailure),
			nil,
		},
	}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain:       "synthetic.sip.telnyx.com",
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CredentialConnectionID: "credential-connection-1",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)

	if _, err := pool.Exec(context.Background(), `
		UPDATE access_memberships
		SET revoked_at = $1
		WHERE user_subject = $2
	`, now, identity.Subject); err != nil {
		t.Fatalf("revoke credential owner: %v", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := calling.ReconcileCredentials(context.Background()); err != nil {
			t.Fatalf("reconcile revoked credential attempt %d: %v", attempt+1, err)
		}
		if processed, err := calling.ProcessNextCommand(context.Background()); err != nil ||
			!processed {
			t.Fatalf(
				"process revoked credential attempt %d: processed=%t err=%v",
				attempt+1,
				processed,
				err,
			)
		}
	}
	var state string
	if err := pool.QueryRow(context.Background(), `
		SELECT state
		FROM human_calling_credentials
		WHERE user_subject = $1
	`, identity.Subject).Scan(&state); err != nil {
		t.Fatalf("read retried credential disable: %v", err)
	}
	if state != "DISABLED" ||
		provider.count(humancalling.CommandDisableCredential) != 2 {
		t.Fatalf("credential state=%s commands=%#v", state, provider.commands)
	}
}

func TestReauthorizationFencesPendingAndAmbiguousCredentialDisable(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, identity := provisionStaff(t, accessModule, now)
	provider := &reauthorizationProvider{}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	setAuthorized := func(authorized bool) {
		t.Helper()
		var revokedAt *time.Time
		if !authorized {
			value := now
			revokedAt = &value
		}
		if _, err := pool.Exec(context.Background(), `
			UPDATE access_memberships
			SET revoked_at = $2
			WHERE user_subject = $1
		`, identity.Subject, revokedAt); err != nil {
			t.Fatalf("set membership authorization to %t: %v", authorized, err)
		}
	}
	credentialState := func() string {
		t.Helper()
		var state string
		if err := pool.QueryRow(context.Background(), `
			SELECT state
			FROM human_calling_credentials
			WHERE user_subject = $1
		`, identity.Subject).Scan(&state); err != nil {
			t.Fatalf("read credential state: %v", err)
		}
		return state
	}

	setAuthorized(false)
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("commit pending credential disable: %v", err)
	}
	setAuthorized(true)
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("fence pending credential disable: %v", err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || processed {
		t.Fatalf("obsolete pending Disable processed=%t err=%v", processed, err)
	}
	if provider.disableCalls != 0 || credentialState() != "ACTIVE" {
		t.Fatalf(
			"pending reauthorization: disableCalls=%d state=%s",
			provider.disableCalls,
			credentialState(),
		)
	}

	setAuthorized(false)
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("commit ambiguous credential disable: %v", err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("execute ambiguous credential disable: processed=%t err=%v", processed, err)
	}
	setAuthorized(true)
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("restore credential while Disable is ambiguous: %v", err)
	}
	now = now.Add(6 * time.Second)
	if processed, err := calling.ProcessNextCredentialReconciliation(
		context.Background(),
	); err != nil || !processed {
		t.Fatalf("reconcile ambiguous Disable after reauthorization: processed=%t err=%v", processed, err)
	}
	if credentialState() != "PENDING" {
		t.Fatalf("absent reauthorized credential state = %s", credentialState())
	}
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("commit replacement credential: %v", err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("create replacement credential: processed=%t err=%v", processed, err)
	}
	if provider.disableCalls != 1 ||
		provider.createCalls != 2 ||
		credentialState() != "ACTIVE" {
		t.Fatalf(
			"ambiguous reauthorization: creates=%d disables=%d state=%s",
			provider.createCalls,
			provider.disableCalls,
			credentialState(),
		)
	}
}

func TestMediaJWTRejectsProviderResultsOutsideTheBrowserSafetyBoundary(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		result humancalling.ProviderResult
	}{
		{
			name: "missing token",
			result: humancalling.ProviderResult{
				JWTExpiresAt: now.Add(time.Hour),
			},
		},
		{
			name: "expired token",
			result: humancalling.ProviderResult{
				JWT:          "expired-media-jwt",
				JWTExpiresAt: now,
			},
		},
		{
			name: "unexpectedly long token",
			result: humancalling.ProviderResult{
				JWT:          "overlong-media-jwt",
				JWTExpiresAt: now.Add(29*24*time.Hour + time.Second),
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := testdb.Open(t)
			accessModule := access.New(pool, func() time.Time { return now })
			_, identity := provisionStaff(t, accessModule, now)
			provider := &recordingProvider{jwtResult: &testCase.result}
			calling := humancalling.New(
				pool,
				accessModule,
				provider,
				humancalling.Config{
					HandoffSIPDomain:       "synthetic.sip.telnyx.com",
					HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
					CredentialConnectionID: "credential-connection-1",
				},
				func() time.Time { return now },
			)

			if err := calling.ReconcileCredentials(context.Background()); err != nil {
				t.Fatalf("reconcile operational credential: %v", err)
			}
			if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
				t.Fatalf("create managed credential: processed=%t err=%v", processed, err)
			}
			if _, err := calling.AcquireSoftphone(
				context.Background(),
				identity,
				"invalid-jwt-browser",
				false,
			); err != nil {
				t.Fatalf("acquire softphone: %v", err)
			}
			if _, err := calling.IssueMediaJWT(
				context.Background(),
				identity,
				"invalid-jwt-browser",
			); err == nil || !strings.Contains(err.Error(), "invalid media JWT") {
				t.Fatalf("invalid provider JWT error = %v", err)
			}
		})
	}
}

func TestAmbiguousCredentialWritesReconcileThroughProviderState(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, identity := provisionStaff(t, accessModule, now)
	provider := &credentialRecoveryProvider{}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain:       "synthetic.sip.telnyx.com",
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CredentialConnectionID: "credential-connection-1",
	}, func() time.Time { return now })

	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("commit credential creation: %v", err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("execute ambiguous credential creation: processed=%t err=%v", processed, err)
	}
	now = now.Add(6 * time.Second)
	if processed, err := calling.ProcessNextCredentialReconciliation(context.Background()); err != nil || !processed {
		t.Fatalf("reconcile created credential: processed=%t err=%v", processed, err)
	}
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		"credential-recovery-browser",
		false,
	); err != nil {
		t.Fatalf("acquire recovered credential softphone: %v", err)
	}
	if _, err := calling.IssueMediaJWT(
		context.Background(),
		identity,
		"credential-recovery-browser",
	); err != nil {
		t.Fatalf("use reconciled credential: %v", err)
	}

	if _, err := pool.Exec(context.Background(), `
		UPDATE access_memberships
		SET revoked_at = $1
		WHERE user_subject = $2
	`, now, identity.Subject); err != nil {
		t.Fatalf("revoke recovered credential owner: %v", err)
	}
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("commit credential disable: %v", err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("execute ambiguous credential disable: processed=%t err=%v", processed, err)
	}
	now = now.Add(6 * time.Second)
	if processed, err := calling.ProcessNextCredentialReconciliation(context.Background()); err != nil || !processed {
		t.Fatalf("reconcile disabled credential: processed=%t err=%v", processed, err)
	}
	var credentialState string
	if err := pool.QueryRow(context.Background(), `
		SELECT state
		FROM human_calling_credentials
		WHERE user_subject = $1
	`, identity.Subject).Scan(&credentialState); err != nil {
		t.Fatalf("read reconciled credential state: %v", err)
	}
	if credentialState != "DISABLED" {
		t.Fatalf("credential state = %s, want DISABLED", credentialState)
	}
}

func TestPlatformOperatorReadsOnlyTheSanitizedDurableTimeline(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionStaff(t, accessModule, now)
	provider := &recordingProvider{}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
	}, func() time.Time { return now })
	handoff, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-operator-timeline",
			PracticeID: authorization.Practice.ID,
		},
		LocationID:     authorization.Locations[0].ID,
		SourceCallID:   "operator-timeline-source",
		IdempotencyKey: "operator-timeline-idempotency",
	})
	if err != nil {
		t.Fatalf("create operator timeline handoff: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "operator-timeline-event",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		CallControlID: "operator-timeline-control",
		CallLegID:     "operator-timeline-leg",
		CallSessionID: "operator-timeline-session",
		To:            handoff.SIPDestination,
	}); err != nil {
		t.Fatalf("admit operator timeline call: %v", err)
	}
	var callID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text
		FROM human_calling_calls
		WHERE handoff_id = $1
	`, handoff.ID).Scan(&callID); err != nil {
		t.Fatalf("load operator timeline call: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO access_platform_operators (email)
		VALUES ('operator@synthetic.test')
	`); err != nil {
		t.Fatalf("provision Platform Operator: %v", err)
	}
	timeline, err := calling.ReadOperatorTimeline(
		context.Background(),
		access.Identity{
			Subject:       "operator-subject",
			Email:         "operator@synthetic.test",
			EmailVerified: true,
		},
		callID,
	)
	if err != nil {
		t.Fatalf("read operator timeline: %v", err)
	}
	if timeline.CallID != callID ||
		timeline.State != humancalling.CallOffering ||
		len(timeline.Entries) != 3 ||
		timeline.Entries[0].Kind != "offer.created" {
		t.Fatalf("timeline = %#v", timeline)
	}
	pendingActions := map[string]bool{}
	for _, entry := range timeline.Entries {
		if entry.Kind == "provider.command.committed" &&
			entry.CommandState == "PENDING" {
			pendingActions[entry.CommandAction] = true
		}
	}
	if !pendingActions["ANSWER_CALLER"] || !pendingActions["START_RINGBACK"] {
		t.Fatalf("pending provider diagnostics = %#v", timeline.Entries)
	}
	if timeline.Entries[0].OpaqueReference != "" ||
		timeline.Entries[0].ErrorCode != "" {
		t.Fatalf("timeline leaked unrequested detail: %#v", timeline.Entries[0])
	}
}

func TestOfferExpiryCommitsOneProviderHangupWithoutAClaim(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	provider := &recordingProvider{}
	calling, identity, _ := readyOfferAt(
		t,
		provider,
		"expiry",
		func() time.Time { return now },
	)
	now = now.Add(21 * time.Second)
	expired, err := calling.ExpireOffers(context.Background())
	if err != nil || expired != 1 {
		t.Fatalf("expire offers: count=%d err=%v", expired, err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process expiry command: %v", err)
		}
		if !processed {
			break
		}
	}
	if provider.count(humancalling.CommandHangup) != 1 {
		t.Fatalf("expiry hangup commands = %#v", provider.commands)
	}
	offers, err := calling.ListOffers(context.Background(), identity)
	if err != nil || len(offers) != 0 {
		t.Fatalf("expired offers = %#v, err = %v", offers, err)
	}
	if count, err := calling.ExpireOffers(context.Background()); err != nil || count != 0 {
		t.Fatalf("re-expire offers: count=%d err=%v", count, err)
	}
}

func TestConnectionTimeoutEndsTheClaimAndHangsUpExactKnownLegs(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	provider := &recordingProvider{}
	calling, identity, offer := readyOfferAt(
		t,
		provider,
		"connection-expiry",
		func() time.Time { return now },
	)
	if _, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"connection-expiry-browser",
		offer.ID,
	); err != nil {
		t.Fatalf("accept connection-expiry offer: %v", err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process connection-expiry setup: %v", err)
		}
		if !processed {
			break
		}
	}
	now = now.Add(16 * time.Second)
	expired, err := calling.ExpireConnections(context.Background())
	if err != nil || expired != 1 {
		t.Fatalf("expire connecting Call: count=%d err=%v", expired, err)
	}
	call, err := calling.ReadCall(context.Background(), identity, offer.ID)
	if err != nil || call.State != humancalling.CallReconciling {
		t.Fatalf("expired connecting Call = %#v, err = %v", call, err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("process timed-out staff hangup: processed=%t err=%v", processed, err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "connection-expiry-staff-hangup",
		Type:          humancalling.FactCallHangup,
		OccurredAt:    now,
		CallControlID: "staff-control-1",
		CallLegID:     "staff-leg-1",
		CallSessionID: "connection-expiry-provider-session",
		HangupCause:   "timeout",
	}); err != nil {
		t.Fatalf("apply timed-out staff hangup: %v", err)
	}
	if _, err := calling.SetReadiness(
		context.Background(),
		ready(identity, "connection-expiry-browser"),
	); err != nil {
		t.Fatalf("refresh readiness after connection timeout: %v", err)
	}
	reopened, err := calling.ListOffers(context.Background(), identity)
	if err != nil || len(reopened) != 1 || reopened[0].ID != offer.ID {
		t.Fatalf("timeout-reopened offer = %#v, err = %v", reopened, err)
	}
	now = now.Add(5 * time.Second)
	if expired, err := calling.ExpireOffers(context.Background()); err != nil || expired != 1 {
		t.Fatalf("expire reopened offer: count=%d err=%v", expired, err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process connection-expiry hangup: %v", err)
		}
		if !processed {
			break
		}
	}
	if provider.count(humancalling.CommandDialStaff) != 1 ||
		provider.count(humancalling.CommandHangup) != 2 {
		t.Fatalf("connection-expiry provider commands = %#v", provider.commands)
	}
}

func TestBridgeAfterAnEndedAttemptDoesNotHangUpTheSharedCallerLeg(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	provider := &recordingProvider{}
	calling, identity, offer := readyOfferAt(
		t,
		provider,
		"post-attempt-bridge",
		func() time.Time { return now },
	)
	if _, err := calling.AcceptOffer(
		context.Background(),
		identity,
		"post-attempt-bridge-browser",
		offer.ID,
	); err != nil {
		t.Fatalf("accept post-attempt bridge offer: %v", err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process post-attempt setup: %v", err)
		}
		if !processed {
			break
		}
	}
	var clientState string
	provider.mu.Lock()
	for _, command := range provider.commands {
		if command.Action == humancalling.CommandDialStaff {
			clientState, _ = command.Payload["client_state"].(string)
			break
		}
	}
	provider.mu.Unlock()
	now = now.Add(16 * time.Second)
	if count, err := calling.ExpireConnections(context.Background()); err != nil || count != 1 {
		t.Fatalf("expire post-attempt connection: count=%d err=%v", count, err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("process timed-out staff cleanup: processed=%t err=%v", processed, err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "post-attempt-staff-hangup",
		Type:          humancalling.FactCallHangup,
		OccurredAt:    now,
		CallControlID: "staff-control-1",
		CallLegID:     "staff-leg-1",
		CallSessionID: "post-attempt-bridge-provider-session",
		ClientState:   clientState,
		HangupCause:   "timeout",
	}); err != nil {
		t.Fatalf("reopen after timed-out staff cleanup: %v", err)
	}
	if _, err := calling.SetReadiness(
		context.Background(),
		ready(identity, "post-attempt-bridge-browser"),
	); err != nil {
		t.Fatalf("refresh readiness after post-attempt cleanup: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "post-attempt-delayed-bridge",
		Type:          humancalling.FactCallBridged,
		OccurredAt:    now.Add(time.Second),
		CallControlID: "staff-control-1",
		CallLegID:     "staff-leg-1",
		CallSessionID: "post-attempt-bridge-provider-session",
		ClientState:   clientState,
	}); err != nil {
		t.Fatalf("project bridge after ended attempt: %v", err)
	}
	offers, err := calling.ListOffers(context.Background(), identity)
	if err != nil || len(offers) != 1 || offers[0].ID != offer.ID {
		t.Fatalf("offer after stale bridge = %#v, err = %v", offers, err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	for _, command := range provider.commands {
		if command.Action == humancalling.CommandHangup &&
			command.TargetID == "post-attempt-bridge-caller-control" {
			t.Fatalf("stale bridge queued shared-caller Hangup: %#v", provider.commands)
		}
	}
}

func TestHistoricalBridgeCannotRegressADispositionedCall(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, winner := provisionStaff(t, accessModule, now)
	calling := humancalling.New(
		pool,
		accessModule,
		&recordingProvider{},
		humancalling.Config{
			HandoffSIPDomain: "synthetic.sip.telnyx.com",
			HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		},
		func() time.Time { return now.Add(time.Hour) },
	)
	handoff, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-terminal-bridge",
			PracticeID: authorization.Practice.ID,
		},
		LocationID:     authorization.Locations[0].ID,
		SourceCallID:   "terminal-bridge-source",
		IdempotencyKey: "terminal-bridge-idempotency",
	})
	if err != nil {
		t.Fatalf("create terminal bridge handoff: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "terminal-bridge-caller",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		CallControlID: "terminal-bridge-caller-control",
		CallLegID:     "terminal-bridge-caller-leg",
		CallSessionID: "terminal-bridge-session",
		To:            handoff.SIPDestination,
	}); err != nil {
		t.Fatalf("admit terminal bridge Call: %v", err)
	}

	var callID, oldAttemptID, winningAttemptID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text
		FROM human_calling_calls
		WHERE handoff_id = $1
	`, handoff.ID).Scan(&callID); err != nil {
		t.Fatalf("load terminal bridge Call: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO human_calling_connection_attempts (
			call_id, claimant_subject, claimant_session_id,
			connection_deadline, staff_call_control_id, staff_call_leg_id,
			ended_at, created_at, updated_at
		)
		VALUES (
			$1, 'historical-subject', 'historical-session',
			$2, 'historical-staff-control', 'historical-staff-leg',
			$2, $3, $3
		)
		RETURNING id::text
	`, callID, now.Add(15*time.Second), now.Add(time.Second)).Scan(&oldAttemptID); err != nil {
		t.Fatalf("insert historical attempt: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO human_calling_connection_attempts (
			call_id, claimant_subject, claimant_session_id,
			connection_deadline, staff_call_control_id, staff_call_leg_id,
			bridge_occurred_at, ended_at, created_at, updated_at
		)
		VALUES (
			$1, $2, 'winning-session',
			$3, 'winning-staff-control', 'winning-staff-leg',
			$4, $5, $4, $5
		)
		RETURNING id::text
	`,
		callID,
		winner.Subject,
		now.Add(45*time.Second),
		now.Add(20*time.Second),
		now.Add(30*time.Second),
	).Scan(&winningAttemptID); err != nil {
		t.Fatalf("insert winning attempt: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_calls
		SET
			state = 'RESOLVED',
			claimant_subject = $2,
			claimant_session_id = 'winning-session',
			winner_subject = $2,
			current_attempt_id = $3,
			expected_staff_call_control_id = 'winning-staff-control',
			expected_staff_call_leg_id = 'winning-staff-leg',
			connected_at = $4,
			ended_at = $5,
			disposition_actor_subject = $2,
			disposition_at = $6
		WHERE id = $1
	`,
		callID,
		winner.Subject,
		winningAttemptID,
		now.Add(20*time.Second),
		now.Add(30*time.Second),
		now.Add(31*time.Second),
	); err != nil {
		t.Fatalf("prepare dispositioned Call: %v", err)
	}
	clientState := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"v":1,"call":"%s","leg":"staff","attempt":"%s"}`,
		callID,
		oldAttemptID,
	)))
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "terminal-bridge-delayed-historical",
		Type:          humancalling.FactCallBridged,
		OccurredAt:    now.Add(10 * time.Second),
		CallControlID: "historical-staff-control",
		CallLegID:     "historical-staff-leg",
		CallSessionID: "terminal-bridge-session",
		ClientState:   clientState,
	}); err != nil {
		t.Fatalf("apply delayed historical bridge: %v", err)
	}

	var state, winnerSubject, expectedStaffLegID, currentAttemptID string
	if err := pool.QueryRow(context.Background(), `
		SELECT
			state,
			winner_subject,
			expected_staff_call_leg_id,
			current_attempt_id::text
		FROM human_calling_calls
		WHERE id = $1
	`, callID).Scan(
		&state,
		&winnerSubject,
		&expectedStaffLegID,
		&currentAttemptID,
	); err != nil {
		t.Fatalf("read terminal Call: %v", err)
	}
	if state != string(humancalling.CallResolved) ||
		winnerSubject != winner.Subject ||
		expectedStaffLegID != "winning-staff-leg" {
		t.Fatalf(
			"terminal Call regressed: state=%s winner=%s staff_leg=%s",
			state,
			winnerSubject,
			expectedStaffLegID,
		)
	}
	if currentAttemptID != winningAttemptID {
		t.Fatalf("current attempt = %s, want %s", currentAttemptID, winningAttemptID)
	}
}

type recordingProvider struct {
	mu            sync.Mutex
	commands      []humancalling.ProviderCommand
	answerError   error
	dialError     error
	hangupErrors  []error
	disableErrors []error
	dialResults   []humancalling.ProviderResult
	jwtResult     *humancalling.ProviderResult
	callAlive     *bool
}

type interruptibleDialProvider struct {
	entered       chan struct{}
	release       chan struct{}
	enteredOnce   sync.Once
	dialCount     int
	commandIDs    []string
	dialCommand   humancalling.ProviderCommand
	hangupTargets []string
}

type blockingJWTProvider struct {
	recordingProvider
	jwtEntered chan struct{}
	jwtRelease chan struct{}
	jwtOnce    sync.Once
}

type credentialRecoveryProvider struct {
	present bool
}

type reauthorizationProvider struct {
	present      bool
	credentialID string
	sipUsername  string
	createCalls  int
	disableCalls int
}

func (provider *blockingJWTProvider) Execute(
	ctx context.Context,
	command humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	if command.Action == humancalling.CommandCreateJWT {
		provider.jwtOnce.Do(func() { close(provider.jwtEntered) })
		select {
		case <-provider.jwtRelease:
		case <-ctx.Done():
			return humancalling.ProviderResult{}, ctx.Err()
		}
	}
	return provider.recordingProvider.Execute(ctx, command)
}

func (provider *credentialRecoveryProvider) Execute(
	_ context.Context,
	command humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	switch command.Action {
	case humancalling.CommandCreateCredential:
		provider.present = true
		return humancalling.ProviderResult{}, humancalling.ErrAmbiguousEffect
	case humancalling.CommandDisableCredential:
		provider.present = false
		return humancalling.ProviderResult{}, humancalling.ErrAmbiguousEffect
	case humancalling.CommandCreateJWT:
		return humancalling.ProviderResult{
			JWT:          "recovered-media-jwt",
			JWTExpiresAt: time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
		}, nil
	default:
		return humancalling.ProviderResult{}, nil
	}
}

func (provider *reauthorizationProvider) Execute(
	_ context.Context,
	command humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	switch command.Action {
	case humancalling.CommandCreateCredential:
		provider.createCalls++
		provider.present = true
		provider.credentialID = fmt.Sprintf("reauthorized-credential-%d", provider.createCalls)
		provider.sipUsername = fmt.Sprintf("reauthorized-sip-%d", provider.createCalls)
		return humancalling.ProviderResult{
			CredentialID: provider.credentialID,
			SIPUsername:  provider.sipUsername,
		}, nil
	case humancalling.CommandDisableCredential:
		provider.disableCalls++
		provider.present = false
		return humancalling.ProviderResult{}, humancalling.ErrAmbiguousEffect
	default:
		return humancalling.ProviderResult{}, nil
	}
}

func (provider *reauthorizationProvider) FindCredentialByName(
	_ context.Context,
	_ string,
) (humancalling.ProviderResult, bool, error) {
	return humancalling.ProviderResult{
		CredentialID: provider.credentialID,
		SIPUsername:  provider.sipUsername,
	}, provider.present, nil
}

func (provider *credentialRecoveryProvider) FindCredentialByName(
	_ context.Context,
	name string,
) (humancalling.ProviderResult, bool, error) {
	if !provider.present {
		return humancalling.ProviderResult{}, false, nil
	}
	return humancalling.ProviderResult{
		CredentialID: "credential-" + name,
		SIPUsername:  "sip-" + name,
	}, true, nil
}

func (provider *interruptibleDialProvider) Execute(
	_ context.Context,
	command humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	if command.Action != humancalling.CommandDialStaff {
		if command.Action == humancalling.CommandHangup {
			provider.hangupTargets = append(provider.hangupTargets, command.TargetID)
		}
		if command.Action == humancalling.CommandCreateCredential {
			return humancalling.ProviderResult{
				CredentialID: "interrupted-credential",
				SIPUsername:  "interrupted-sip-user",
			}, nil
		}
		return humancalling.ProviderResult{}, nil
	}
	provider.dialCount++
	provider.commandIDs = append(provider.commandIDs, command.ID)
	provider.dialCommand = command
	provider.enteredOnce.Do(func() { close(provider.entered) })
	<-provider.release
	return humancalling.ProviderResult{
		CallControlID: "late-staff-control",
		CallLegID:     "late-staff-leg",
	}, nil
}

func (provider *recordingProvider) Execute(
	_ context.Context,
	command humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.commands = append(provider.commands, command)
	if command.Action == humancalling.CommandAnswerCaller {
		return humancalling.ProviderResult{}, provider.answerError
	}
	if command.Action == humancalling.CommandDialStaff {
		if len(provider.dialResults) > 0 {
			result := provider.dialResults[0]
			provider.dialResults = provider.dialResults[1:]
			return result, provider.dialError
		}
		return humancalling.ProviderResult{
			CallControlID: "staff-control-1",
			CallLegID:     "staff-leg-1",
		}, provider.dialError
	}
	if command.Action == humancalling.CommandHangup &&
		len(provider.hangupErrors) > 0 {
		err := provider.hangupErrors[0]
		provider.hangupErrors = provider.hangupErrors[1:]
		return humancalling.ProviderResult{}, err
	}
	if command.Action == humancalling.CommandDisableCredential &&
		len(provider.disableErrors) > 0 {
		err := provider.disableErrors[0]
		provider.disableErrors = provider.disableErrors[1:]
		return humancalling.ProviderResult{}, err
	}
	if command.Action == humancalling.CommandCreateCredential {
		name, _ := command.Payload["name"].(string)
		return humancalling.ProviderResult{
			CredentialID: "credential-" + name,
			SIPUsername:  "sip-" + name,
		}, nil
	}
	if command.Action == humancalling.CommandCreateJWT {
		if provider.jwtResult != nil {
			return *provider.jwtResult, nil
		}
		return humancalling.ProviderResult{
			JWT:          "synthetic-media-jwt",
			JWTExpiresAt: time.Date(2026, time.July, 27, 13, 0, 0, 0, time.UTC),
		}, nil
	}
	return humancalling.ProviderResult{}, nil
}

func (provider *recordingProvider) IsCallAlive(
	_ context.Context,
	_ string,
) (bool, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.callAlive == nil {
		return true, nil
	}
	return *provider.callAlive, nil
}

func assertCallControlWaitsForIdentityFence(
	t *testing.T,
	pool *pgxpool.Pool,
	identity access.Identity,
	callID string,
	action func() error,
) {
	t.Helper()
	ctx := context.Background()
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin identity-fence blocker: %v", err)
	}
	release := func() { _ = blocker.Rollback(ctx) }
	defer release()
	if _, err := blocker.Exec(ctx, `
		SELECT pg_advisory_xact_lock(1094927189, hashtext($1))
	`, identity.Subject); err != nil {
		t.Fatalf("hold AcquireSoftphone identity fence: %v", err)
	}
	finished := make(chan error, 1)
	go func() { finished <- action() }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-finished:
			t.Fatalf("Call control returned before identity fence released: %v", err)
		default:
		}
		var waiting bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
					AND wait_event = 'advisory'
					AND query LIKE '%1094927189%'
			)
		`).Scan(&waiting); err != nil {
			t.Fatalf("observe Call control identity wait: %v", err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Call control did not reach AcquireSoftphone identity fence")
		}
		time.Sleep(10 * time.Millisecond)
	}
	probe, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin Call lock probe: %v", err)
	}
	_, lockErr := probe.Exec(ctx, `
		SELECT 1
		FROM human_calling_calls
		WHERE id = $1
		FOR UPDATE NOWAIT
	`, callID)
	_ = probe.Rollback(ctx)
	if lockErr != nil {
		release()
		<-finished
		t.Fatalf("Call control locked Call before identity fence: %v", lockErr)
	}
	release()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Call control after identity fence: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Call control did not finish after identity fence released")
	}
}

func readyOffer(
	t *testing.T,
	provider *recordingProvider,
	key string,
) (*humancalling.Module, access.Identity, humancalling.Offer) {
	t.Helper()
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	return readyOfferAt(t, provider, key, func() time.Time { return now })
}

func readyOfferAt(
	t *testing.T,
	provider humancalling.Provider,
	key string,
	clock func() time.Time,
) (*humancalling.Module, access.Identity, humancalling.Offer) {
	t.Helper()
	pool := testdb.Open(t)
	now := clock()
	accessModule := access.New(pool, clock)
	authorization, identity := provisionStaff(t, accessModule, now)
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		StaffSIPDomain:   "sip.telnyx.com",
		OfferDuration:    20 * time.Second,
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		RecordingBucket:  "synthetic-recordings",
	}, clock)
	prepareCredentials(t, calling)
	handoff, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-" + key,
			PracticeID: authorization.Practice.ID,
		},
		LocationID:     authorization.Locations[0].ID,
		SourceCallID:   key + "-source",
		IdempotencyKey: key + "-idempotency",
		Contact: humancalling.ContactContext{
			Phone:          "+15555550100",
			PhoneSource:    "Abita",
			DisplayName:    "Recovery Caller",
			NameSource:     "Abita",
			TransferReason: "Recovery proof",
			ReasonSource:   "Abita AI",
		},
	})
	if err != nil {
		t.Fatalf("create %s handoff: %v", key, err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       key + "-inbound-event",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		CallControlID: key + "-caller-control",
		CallLegID:     key + "-caller-leg",
		CallSessionID: key + "-provider-session",
		To:            handoff.SIPDestination,
	}); err != nil {
		t.Fatalf("admit %s caller: %v", key, err)
	}
	sessionID := key + "-browser"
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		sessionID,
		false,
	); err != nil {
		t.Fatalf("acquire %s softphone: %v", key, err)
	}
	if _, err := calling.SetReadiness(context.Background(), humancalling.ReadinessCommand{
		Identity:        identity,
		SessionID:       sessionID,
		Registered:      true,
		MicrophoneReady: true,
		AudioReady:      true,
		SessionHealthy:  true,
		Available:       true,
	}); err != nil {
		t.Fatalf("ready %s softphone: %v", key, err)
	}
	offers, err := calling.ListOffers(context.Background(), identity)
	if err != nil || len(offers) != 1 {
		t.Fatalf("%s offers = %#v, err = %v", key, offers, err)
	}
	return calling, identity, offers[0]
}

func prepareCredentials(t *testing.T, calling *humancalling.Module) {
	t.Helper()
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("reconcile managed credentials: %v", err)
	}
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("create managed credential: %v", err)
		}
		if !processed {
			return
		}
	}
}

func (provider *recordingProvider) count(action humancalling.CommandAction) int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	count := 0
	for _, command := range provider.commands {
		if command.Action == action {
			count++
		}
	}
	return count
}

func (provider *recordingProvider) ordered(
	first humancalling.CommandAction,
	second humancalling.CommandAction,
) bool {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	firstIndex := -1
	secondIndex := -1
	for index, command := range provider.commands {
		if command.Action == first && firstIndex == -1 {
			firstIndex = index
		}
		if command.Action == second && secondIndex == -1 {
			secondIndex = index
		}
	}
	return firstIndex >= 0 && secondIndex > firstIndex
}

func provisionStaff(
	t *testing.T,
	accessModule *access.Module,
	now time.Time,
) (access.Authorization, access.Identity) {
	t.Helper()
	provisioned, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "slice-2-test",
		Practices: []access.PracticeProvision{{
			Key:       "synthetic-practice",
			Name:      "Synthetic Practice",
			Locations: []access.LocationProvision{{Key: "synthetic-location", Name: "Synthetic Location"}},
			Invitations: []access.InvitationProvision{{
				Key:           "synthetic-staff",
				Email:         "staff@synthetic.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
				ExpiresAt:     now.Add(time.Hour),
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision staff: %v", err)
	}
	identity := access.Identity{
		Subject:       "synthetic-staff-subject",
		Email:         "staff@synthetic.test",
		EmailVerified: true,
	}
	authorization, err := accessModule.AcceptInvitation(
		context.Background(),
		identity,
		provisioned.Invitations[0].Token,
	)
	if err != nil {
		t.Fatalf("accept staff invitation: %v", err)
	}
	return authorization, identity
}
