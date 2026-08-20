package humancalling_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/worker"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProvisionedGoogleUserReceivesManagedCallingCredential(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	if _, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "google-calling-readiness-test",
		Practices: []access.PracticeProvision{{
			Key:       "google-calling-practice",
			Name:      "Google Calling Practice",
			Locations: []access.LocationProvision{{Key: "google-calling-location", Name: "Google Calling Location"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "google-calling-staff",
				Email:         "staff@google-calling.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatalf("provision Google Staff access: %v", err)
	}
	identity := access.Identity{
		Subject:       "google-calling-staff-subject",
		Email:         "staff@google-calling.test",
		EmailVerified: true,
	}
	if _, err := accessModule.DiscoverActor(context.Background(), identity); err != nil {
		t.Fatalf("activate provisioned Google Staff access: %v", err)
	}

	provider := &recordingProvider{}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		CredentialConnectionID: "staff-credential-connection",
	}, func() time.Time { return now })
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("discover managed Staff credential: %v", err)
	}
	processed, err := calling.ProcessNextCommand(context.Background())
	if err != nil || !processed {
		t.Fatalf("create managed Staff credential: processed=%t err=%v", processed, err)
	}

	var state, credentialID, sipUsername string
	if err := pool.QueryRow(context.Background(), `
		SELECT state, provider_credential_id, provider_sip_username
		FROM human_calling_credentials WHERE user_subject = $1
	`, identity.Subject).Scan(&state, &credentialID, &sipUsername); err != nil {
		t.Fatalf("read managed Staff credential: %v", err)
	}
	if state != "ACTIVE" || credentialID == "" || sipUsername == "" ||
		provider.count(humancalling.CommandCreateCredential) != 1 {
		t.Fatalf("managed Staff credential = state=%q id=%q sip=%q commands=%d",
			state, credentialID, sipUsername,
			provider.count(humancalling.CommandCreateCredential))
	}
	created := provider.last(humancalling.CommandCreateCredential)
	if created.Payload["connection_id"] != "staff-credential-connection" ||
		created.Payload["tag"] != "acuity-portal" {
		t.Fatalf("managed Staff credential command = %#v", created.Payload)
	}
}

func TestInboundTransferFansOutToStaffButNotAdminForItsLocation(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 8, 13, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	if _, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "location-ring-test",
		Practices: []access.PracticeProvision{{
			Key:                                 "location-ring-practice",
			Name:                                "Location Ring Practice",
			ConnectedCallRecordingEnabled:       true,
			ConnectedCallRecordingRetentionDays: 30,
			Locations: []access.LocationProvision{
				{Key: "sweetwater", Name: "Sweetwater"},
				{Key: "north-miami-beach-optical", Name: "North Miami Beach Optical"},
			},
			AccessGrants: []access.AccessGrantProvision{
				{Key: "sweetwater-staff", Email: "sweetwater@ring.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"sweetwater"}},
				{Key: "north-miami-beach-staff", Email: "north-miami-beach@ring.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"north-miami-beach-optical"}},
				{Key: "practice-admin", Email: "admin@ring.test", Role: access.RoleAdmin, LocationScope: access.LocationScopeAll},
			},
		}},
	}); err != nil {
		t.Fatalf("provision Location-scoped Staff: %v", err)
	}
	staff := []access.Identity{
		{Subject: "sweetwater-staff-subject", Email: "sweetwater@ring.test", EmailVerified: true},
		{Subject: "north-miami-beach-staff-subject", Email: "north-miami-beach@ring.test", EmailVerified: true},
		{Subject: "practice-admin-subject", Email: "admin@ring.test", EmailVerified: true},
	}
	var sweetwater access.Location
	var practiceID string
	for index, identity := range staff {
		discovery, err := accessModule.DiscoverActor(context.Background(), identity)
		if err != nil {
			t.Fatalf("activate Location-scoped user %d: %v", index+1, err)
		}
		if index == 0 {
			practiceID = discovery.Practices[0].ID
			sweetwater = discovery.Practices[0].Locations[0]
		}
		if !discovery.Practices[0].CallingEnabled {
			t.Fatalf("portal CallingEnabled for %q = false", identity.Email)
		}
	}

	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: "sweetwater-staff-control",
		CallLegID:     "sweetwater-staff-leg",
	}}}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain:       "synthetic.sip.telnyx.com",
		StaffSIPDomain:         "sip.telnyx.com",
		RingWindowDuration:     20 * time.Second,
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
		FromNumber:             "+14843336938",
		RingbackURL:            "https://media.synthetic.test/ringback.wav",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	readyConcurrentStaff(t, calling, staff, "location-ring-browser")

	if _, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject: "abita-location-ring", PracticeID: practiceID,
		},
		LocationID: sweetwater.ID, SourceCallID: "location-ring-source",
		IdempotencyKey: "location-ring-handoff",
		Contact:        humancalling.ContactContext{Phone: "+15555550100"},
	}); err != nil {
		t.Fatalf("create Sweetwater handoff: %v", err)
	}
	caller := humancalling.ProviderFact{
		EventID: "location-ring-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now, ConnectionID: "staff-call-control-connection",
		CallControlID: "location-ring-caller-control", CallLegID: "location-ring-caller-leg",
		CallSessionID: "location-ring-caller-session", From: "+15555550100",
		To: "+14843989071",
	}
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatalf("admit Sweetwater caller: %v", err)
	}
	processAllCommands(t, calling)
	caller.EventID = "location-ring-answered"
	caller.Type = humancalling.FactCallAnswered
	caller.OccurredAt = now.Add(time.Second)
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatalf("fan out Sweetwater caller: %v", err)
	}
	processAllCommands(t, calling)

	var subjects []string
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(array_agg(staff_subject ORDER BY staff_subject), '{}')
		FROM human_calling_call_legs WHERE role = 'STAFF'
	`).Scan(&subjects); err != nil {
		t.Fatalf("read Location-scoped Staff fanout: %v", err)
	}
	if len(subjects) != 1 || subjects[0] != "sweetwater-staff-subject" {
		t.Fatalf("Sweetwater fanout subjects = %#v", subjects)
	}
}

func TestPlatformOperatorReceivesCallsWithoutPracticeMembership(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	operator := access.Identity{
		Subject: "demo-operator-subject", Email: "operator@acuity.test", EmailVerified: true,
	}
	if _, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test", RequestedBy: "demo-operator-calling-test",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key: "acuity-demo", Name: "Acuity Demo",
			Locations: []access.LocationProvision{{Key: "demo", Name: "Demo"}},
		}},
	}); err != nil {
		t.Fatalf("provision demo operator: %v", err)
	}
	discovery, err := accessModule.DiscoverActor(context.Background(), operator)
	if err != nil {
		t.Fatalf("discover demo operator: %v", err)
	}
	if len(discovery.Practices) != 1 || discovery.Practices[0].Membership != nil ||
		!discovery.Practices[0].CallingEnabled {
		t.Fatalf("demo operator discovery = %#v", discovery)
	}
	demo := discovery.Practices[0]

	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: "demo-operator-control", CallLegID: "demo-operator-provider-leg",
	}}}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com", StaffSIPDomain: "sip.telnyx.com",
		RingWindowDuration:     20 * time.Second,
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
		FromNumber:             "+14843336938", RingbackURL: "https://media.synthetic.test/ringback.mp3",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	readyConcurrentStaff(t, calling, []access.Identity{operator}, "demo-operator-browser")

	handoff, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service:    humancalling.ServiceIdentity{Subject: "demo-agent", PracticeID: demo.ID},
		LocationID: demo.Locations[0].ID, SourceCallID: "demo-operator-source-call",
		IdempotencyKey: "demo-operator-handoff",
		Contact: humancalling.ContactContext{
			Phone: "+15555550100", PhoneSource: "Demo", DisplayName: "Demo Caller",
			NameSource: "Demo", TransferReason: "Demo test", ReasonSource: "Demo",
		},
	})
	if err != nil {
		t.Fatalf("create demo handoff: %v", err)
	}
	if handoff.SIPDestination == "" {
		t.Fatal("demo handoff is missing its SIP destination")
	}
	fact := humancalling.ProviderFact{
		EventID: "demo-operator-caller-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now, ConnectionID: "staff-call-control-connection",
		CallControlID: "demo-caller-control", CallLegID: "demo-caller-provider-leg",
		CallSessionID: "demo-caller-session", From: "+15555550100", To: "+14843989071",
	}
	if err := calling.ApplyProviderFact(context.Background(), fact); err != nil {
		t.Fatalf("admit demo caller: %v", err)
	}
	processAllCommands(t, calling)
	fact.EventID = "demo-operator-caller-answered"
	fact.Type = humancalling.FactCallAnswered
	fact.OccurredAt = now.Add(time.Second)
	if err := calling.ApplyProviderFact(context.Background(), fact); err != nil {
		t.Fatalf("answer demo caller: %v", err)
	}
	processAllCommands(t, calling)
	if got := provider.count(humancalling.CommandDialStaff); got != 1 {
		t.Fatalf("demo Staff dials = %d, want 1", got)
	}
}

func TestInboundReferFansOutCallLegsAndBridgesOneStaffWinner(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	currentTime := now
	accessModule := access.New(pool, func() time.Time { return currentTime })
	authorization, staff := provisionConcurrentStaff(
		t, accessModule, now, "call-leg-fanout", 2,
	)
	if _, err := pool.Exec(context.Background(), `
		UPDATE access_practices SET
			connected_call_recording_retention_days = 30,
			connected_call_recording_enabled = true
		WHERE id = $1
	`, authorization.Practice.ID); err != nil {
		t.Fatalf("enable inbound connected recording: %v", err)
	}
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{
		{CallControlID: "staff-control-1", CallLegID: "staff-provider-leg-1"},
		{CallControlID: "staff-control-2", CallLegID: "staff-provider-leg-2"},
	}}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain:       "synthetic.sip.telnyx.com",
		StaffSIPDomain:         "sip.telnyx.com",
		RingWindowDuration:     20 * time.Second,
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
		FromNumber:             "+14843336938",
		RingbackURL:            "https://media.synthetic.test/ringback.mp3",
	}, func() time.Time { return currentTime })

	prepareCredentials(t, calling)
	readyConcurrentStaff(t, calling, staff, "call-leg-browser")
	handoff, err := calling.CreateHandoff(
		context.Background(),
		humancalling.CreateHandoffCommand{
			Service: humancalling.ServiceIdentity{
				Subject:    "abita-call-leg-fanout",
				PracticeID: authorization.Practice.ID,
			},
			LocationID:     authorization.Locations[0].ID,
			SourceCallID:   "abita-source-call",
			IdempotencyKey: "abita-handoff-attempt",
			Contact: humancalling.ContactContext{
				Phone:          "+15555550100",
				PhoneSource:    "Abita",
				DisplayName:    "Synthetic Caller",
				NameSource:     "Abita",
				TransferReason: "Scheduling help",
				ReasonSource:   "Abita AI",
			},
		},
	)
	if err != nil {
		t.Fatalf("create handoff: %v", err)
	}
	if handoff.SIPDestination != "sip:acuity-handoff@synthetic.sip.telnyx.com" {
		t.Fatalf("handoff SIP destination = %q", handoff.SIPDestination)
	}
	caller := humancalling.ProviderFact{
		EventID:       "caller-initiated",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		ConnectionID:  "staff-call-control-connection",
		CallControlID: "caller-control",
		CallLegID:     "caller-provider-leg",
		CallSessionID: "caller-session",
		From:          "+15555550100",
		To:            "+14843989071",
	}
	wrongConnection := caller
	wrongConnection.EventID = "caller-initiated-wrong-connection"
	wrongConnection.ConnectionID = "unrelated-call-control-connection"
	if err := calling.ApplyProviderFact(
		context.Background(), wrongConnection,
	); !errors.Is(err, humancalling.ErrInvalidHandoff) {
		t.Fatalf("wrong-connection REFER error = %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatalf("admit REFER caller: %v", err)
	}
	processAllCommands(t, calling)

	caller.EventID = "caller-answered"
	caller.Type = humancalling.FactCallAnswered
	caller.OccurredAt = now.Add(time.Second)
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatalf("fan out caller answer: %v", err)
	}
	processAllCommands(t, calling)

	if provider.count(humancalling.CommandStartRingWindow) != 1 ||
		provider.count(humancalling.CommandDialStaff) != 2 {
		t.Fatalf("provider commands = %#v, want ring and two independent Staff Dials", provider.commands)
	}

	ring := provider.last(humancalling.CommandStartRingWindow)
	if _, hasLoop := ring.Payload["loop"]; hasLoop {
		t.Fatalf("ring-window playback unexpectedly loops: %#v", ring.Payload)
	}
	dials := provider.all(humancalling.CommandDialStaff)
	var dependentDials int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_provider_commands
		WHERE action = 'DIAL_STAFF' AND depends_on_command_id IS NOT NULL
	`).Scan(&dependentDials); err != nil {
		t.Fatal(err)
	}
	if dependentDials != 0 {
		t.Fatalf("Staff Dials gated by another provider command = %d", dependentDials)
	}
	answers := make([]humancalling.ProviderFact, len(dials))
	for index, dial := range dials {
		if dial.Payload["retry_on_timeout"] != false {
			t.Fatalf("Staff Dial retry_on_timeout = %#v", dial.Payload["retry_on_timeout"])
		}
		clientState, _ := dial.Payload["client_state"].(string)
		initiated := humancalling.ProviderFact{
			EventID:       fmt.Sprintf("staff-initiated-%d", index+1),
			Type:          humancalling.FactCallInitiated,
			OccurredAt:    now.Add(2 * time.Second),
			ConnectionID:  "staff-call-control-connection",
			CallControlID: fmt.Sprintf("staff-control-%d", index+1),
			CallLegID:     fmt.Sprintf("staff-provider-leg-%d", index+1),
			CallSessionID: fmt.Sprintf("staff-session-%d", index+1),
			ClientState:   clientState,
		}
		if err := calling.ApplyProviderFact(context.Background(), initiated); err != nil {
			t.Fatalf("project Staff initiation %d: %v", index+1, err)
		}
		initiated.EventID = fmt.Sprintf("staff-answered-%d", index+1)
		initiated.Type = humancalling.FactCallAnswered
		initiated.OccurredAt = now.Add(3 * time.Second)
		answers[index] = initiated
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO access_platform_operators (email, user_subject)
		VALUES ($1, $2)
	`, staff[1].Email, staff[1].Subject); err != nil {
		t.Fatalf("promote ringing Staff to Platform Operator: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_softphone_leases
		SET desired_available = false, updated_at = $3
		WHERE user_subject IN ($1, $2)
	`, staff[0].Subject, staff[1].Subject, now.Add(3*time.Second)); err != nil {
		t.Fatalf("reserve browsers after local answer: %v", err)
	}
	answerErrors := make(chan error, len(answers))
	var answersDone sync.WaitGroup
	for _, answer := range answers {
		answersDone.Add(1)
		go func(fact humancalling.ProviderFact) {
			defer answersDone.Done()
			answerErrors <- calling.ApplyProviderFact(context.Background(), fact)
		}(answer)
	}
	answersDone.Wait()
	close(answerErrors)
	for err := range answerErrors {
		if err != nil {
			t.Fatalf("project concurrent Staff answer: %v", err)
		}
	}
	ringClientState, _ := ring.Payload["client_state"].(string)
	currentTime = now.Add(6 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "ring-window-completed", Type: humancalling.FactPlaybackEnded,
		OccurredAt: now.Add(4 * time.Second), CallControlID: "caller-control",
		CallLegID: "caller-provider-leg", CallSessionID: "caller-session",
		ClientState: ringClientState, PlaybackStatus: "completed",
	}); err != nil {
		t.Fatalf("complete ring window after provisional winner: %v", err)
	}
	processAllCommands(t, calling)

	bridge := provider.last(humancalling.CommandBridge)
	if bridge.CallLegID == "" || bridge.PeerCallLegID == "" ||
		bridge.Payload["call_control_id"] != "caller-control" ||
		bridge.Payload["prevent_double_bridge"] != true ||
		bridge.Payload["record"] != "record-from-answer" ||
		bridge.Payload["record_channels"] != "dual" ||
		bridge.Payload["record_format"] != "mp3" ||
		bridge.Payload["record_track"] != "both" {
		t.Fatalf("explicit Bridge command = %#v", bridge)
	}
	var callID, recordingState string
	if err := pool.QueryRow(context.Background(), `
		SELECT call_id::text FROM human_calling_call_legs WHERE id = $1
	`, bridge.CallLegID).Scan(&callID); err != nil {
		t.Fatalf("read connected recording Call: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT audio_state FROM human_calling_call_recordings WHERE call_id = $1
	`, callID).Scan(&recordingState); err != nil || recordingState != "PROCESSING" {
		t.Fatalf("prepared connected recording = %q, err = %v", recordingState, err)
	}
	if provider.count(humancalling.CommandBridge) != 1 ||
		provider.count(humancalling.CommandHangupLeg) != 1 ||
		provider.count(humancalling.CommandSpeakVoicemail) != 0 {
		t.Fatalf("provider winner commands = %#v", provider.commands)
	}
	bridgeClientState, _ := bridge.Payload["client_state"].(string)
	var winnerControlID, winnerProviderLegID, winnerSessionID string
	if err := pool.QueryRow(context.Background(), `
		SELECT provider_call_control_id, provider_call_leg_id, provider_call_session_id
		FROM human_calling_call_legs WHERE id = $1
	`, bridge.CallLegID).Scan(&winnerControlID, &winnerProviderLegID, &winnerSessionID); err != nil {
		t.Fatalf("read winning provider identity: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "winner-bridge-mismatched", Type: humancalling.FactCallBridged,
		OccurredAt: now.Add(5 * time.Second), CallControlID: winnerControlID,
		CallLegID: "wrong-provider-leg", CallSessionID: winnerSessionID,
		ClientState: bridgeClientState,
	}); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("mismatched Bridge identity error = %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "winner-bridge-confirmed", Type: humancalling.FactCallBridged,
		OccurredAt: now.Add(5 * time.Second), CallControlID: winnerControlID,
		CallLegID: winnerProviderLegID, CallSessionID: winnerSessionID,
	}); err != nil {
		t.Fatalf("confirm explicit Bridge: %v", err)
	}
	var resolvedPhone, resolutionKind, resolutionSource string
	var resolvedAt time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT phone, resolved_at, kind, source_id
		FROM work_recovery_resolution_checkpoints
		WHERE practice_id = $1
	`, authorization.Practice.ID).Scan(
		&resolvedPhone,
		&resolvedAt,
		&resolutionKind,
		&resolutionSource,
	); err != nil ||
		resolvedPhone != "+15555550100" ||
		!resolvedAt.Equal(now.Add(5*time.Second)) ||
		resolutionKind != "INBOUND_CALL" ||
		resolutionSource == "" {
		t.Fatalf(
			"inbound Bridge recovery checkpoint = (%q, %s, %q, %q), %v",
			resolvedPhone,
			resolvedAt,
			resolutionKind,
			resolutionSource,
			err,
		)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "winner-recording-saved", Type: humancalling.FactRecordingSaved,
		OccurredAt: now.Add(36 * time.Second), CallControlID: winnerControlID,
		CallLegID: winnerProviderLegID, CallSessionID: winnerSessionID,
		ClientState: bridgeClientState, RecordingID: "connected-recording-id",
		RecordingStartedAt: now.Add(5 * time.Second),
		RecordingEndedAt:   now.Add(35 * time.Second),
	}); err != nil {
		t.Fatalf("save connected recording: %v", err)
	}
	recordedCall, err := calling.ReadCall(context.Background(), staff[0], callID)
	if err != nil || recordedCall.Recording.AudioState != humancalling.RecordingReady ||
		recordedCall.Recording.DurationSeconds != 30 {
		t.Fatalf("connected recording projection = %#v, err = %v", recordedCall.Recording, err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "caller-bridge-confirmed", Type: humancalling.FactCallBridged,
		OccurredAt: now.Add(5 * time.Second), CallControlID: "caller-control",
		CallLegID: "caller-provider-leg", CallSessionID: "caller-session",
	}); err != nil {
		t.Fatalf("confirm caller Bridge: %v", err)
	}
	processAllCommands(t, calling)
	stop := provider.last(humancalling.CommandStopRingWindow)
	if stop.Payload["stop"] != "all" {
		t.Fatalf("Stop ring-window payload = %#v", stop.Payload)
	}
	var bridgedLegs, endingLosers int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM human_calling_call_legs
		WHERE state = 'BRIDGED'
	`).Scan(&bridgedLegs); err != nil {
		t.Fatalf("count bridged CallLegs: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_call_legs
		WHERE role = 'STAFF' AND state = 'ENDING'
	`).Scan(&endingLosers); err != nil {
		t.Fatalf("count ending Staff losers: %v", err)
	}
	if bridgedLegs != 2 || endingLosers != 1 {
		t.Fatalf("CallLeg states = %d bridged, %d ending; want 2 and 1",
			bridgedLegs, endingLosers)
	}
	var loserControlID, loserProviderLegID, loserSessionID string
	if err := pool.QueryRow(context.Background(), `
		SELECT provider_call_control_id, provider_call_leg_id, provider_call_session_id
		FROM human_calling_call_legs
		WHERE role = 'STAFF' AND state = 'ENDING'
	`).Scan(&loserControlID, &loserProviderLegID, &loserSessionID); err != nil {
		t.Fatal(err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "loser-cleanup-hangup", Type: humancalling.FactCallHangup,
		OccurredAt: now.Add(6 * time.Second), CallControlID: loserControlID,
		CallLegID: loserProviderLegID, CallSessionID: loserSessionID,
		HangupCause: "NORMAL_CLEARING", TerminationSource: "CALL_CONTROL",
	}); err != nil {
		t.Fatalf("project losing Staff cleanup Hangup: %v", err)
	}
	var prematureTerminal *string
	if err := pool.QueryRow(context.Background(), `
		SELECT terminal_outcome FROM human_calling_calls
	`).Scan(&prematureTerminal); err != nil {
		t.Fatal(err)
	}
	if prematureTerminal != nil {
		t.Fatalf("loser cleanup ended connected Call as %s", *prematureTerminal)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "winner-hangup", Type: humancalling.FactCallHangup,
		OccurredAt: now.Add(6 * time.Second), CallControlID: winnerControlID,
		CallLegID: winnerProviderLegID, CallSessionID: winnerSessionID,
		HangupCause: "NORMAL_CLEARING", TerminationSource: "STAFF",
	}); err != nil {
		t.Fatalf("end connected winner: %v", err)
	}
	var bridgedCallID string
	if err := pool.QueryRow(context.Background(), `
		SELECT call_id::text FROM human_calling_call_legs WHERE id = $1
	`, bridge.CallLegID).Scan(&bridgedCallID); err != nil {
		t.Fatal(err)
	}
	connectedCall, err := calling.ReadCall(context.Background(), staff[0], bridgedCallID)
	if err != nil || connectedCall.State != humancalling.CallNeedsDisposition ||
		connectedCall.DispositionDeadline == nil {
		t.Fatalf("completed connected Call = %#v, %v", connectedCall, err)
	}
	processAllCommands(t, calling)
	currentTime = now.Add(30 * time.Second)
	if expired, err := calling.ExpireDispositions(context.Background()); err != nil || expired != 1 {
		t.Fatalf("expire connected disposition = %d, %v", expired, err)
	}
	connectedCall, err = calling.ReadCall(context.Background(), staff[0], bridgedCallID)
	if err != nil || connectedCall.State != humancalling.CallResolved ||
		connectedCall.DispositionDeadline != nil {
		t.Fatalf("auto-resolved connected Call = %#v, %v", connectedCall, err)
	}
	if err := calling.RecoverInterruptedCommands(context.Background()); err != nil {
		t.Fatalf("recover interrupted commands: %v", err)
	}
	if _, err := calling.ReconcileStaleCalls(context.Background()); err != nil {
		t.Fatalf("reconcile stale CallLegs: %v", err)
	}
	var connectedTasks, connectedAcknowledgements int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM work_tasks WHERE call_id = $1),
			(SELECT count(*)
			 FROM work_task_acknowledgements acknowledgement
			 JOIN work_tasks task ON task.id = acknowledgement.task_id
			 WHERE task.call_id = $1)
	`, bridgedCallID).Scan(&connectedTasks, &connectedAcknowledgements); err != nil {
		t.Fatalf("read connected transfer Task effects: %v", err)
	}
	if connectedTasks != 0 || connectedAcknowledgements != 0 {
		t.Fatalf(
			"connected transfer effects = %d Tasks, %d acknowledgements; want none",
			connectedTasks,
			connectedAcknowledgements,
		)
	}
}

func TestInboundReferRejectsAmbiguousCallerReservations(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 5, 12, 30, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(
		t, accessModule, now, "ambiguous-handoff", 1,
	)
	calling := humancalling.New(pool, accessModule, &recordingProvider{}, humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:    "staff-call-control-connection",
	}, func() time.Time { return now })
	for _, suffix := range []string{"one", "two"} {
		if _, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
			Service: humancalling.ServiceIdentity{
				Subject: "abita-ambiguous-handoff", PracticeID: authorization.Practice.ID,
			},
			LocationID:     authorization.Locations[0].ID,
			SourceCallID:   "ambiguous-source-" + suffix,
			IdempotencyKey: "ambiguous-attempt-" + suffix,
			Contact:        humancalling.ContactContext{Phone: "+15555550100"},
		}); err != nil {
			t.Fatalf("create handoff %s: %v", suffix, err)
		}
	}

	err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "ambiguous-caller-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now, ConnectionID: "staff-call-control-connection",
		CallControlID: "ambiguous-caller-control", CallLegID: "ambiguous-caller-leg",
		CallSessionID: "ambiguous-caller-session", From: "+15555550100",
		To: "+14843989071",
	})
	if !errors.Is(err, humancalling.ErrInvalidHandoff) {
		t.Fatalf("ambiguous REFER error = %v", err)
	}
	var calls, consumed int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM human_calling_calls),
			(SELECT count(*) FROM human_calling_handoffs WHERE consumed_at IS NOT NULL)
	`).Scan(&calls, &consumed); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || consumed != 0 {
		t.Fatalf("ambiguous REFER mutated calls=%d consumed=%d", calls, consumed)
	}
}

func TestOutboundCallUsesCallLegEvidenceAndExplicitBridge(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 5, 13, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, staff := provisionConcurrentStaff(
		t, accessModule, now, "call-leg-outbound", 2,
	)
	if _, err := pool.Exec(context.Background(), `
		UPDATE access_practices SET
			connected_call_recording_retention_days = 30,
			connected_call_recording_enabled = true
		WHERE id = $1
	`, authorization.Practice.ID); err != nil {
		t.Fatalf("enable outbound connected recording: %v", err)
	}
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{
		{CallControlID: "outbound-staff-control", CallLegID: "outbound-staff-provider-leg"},
		{CallControlID: "destination-control", CallLegID: "destination-provider-leg"},
	}}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		StaffSIPDomain:         "sip.telnyx.com",
		RingWindowDuration:     20 * time.Second,
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	readyConcurrentStaff(t, calling, staff, "outbound-browser")
	if err := calling.ProvisionLocationVoices(context.Background(),
		[]humancalling.LocationVoiceProvision{{
			PracticeKey: "call-leg-outbound-practice",
			LocationKey: "call-leg-outbound-location",
			Number:      "+14843336938", Enabled: true,
		}}); err != nil {
		t.Fatalf("provision outbound caller ID: %v", err)
	}

	call, err := calling.StartOutboundCall(context.Background(),
		humancalling.StartOutboundCallCommand{
			Identity: staff[0], SessionID: "outbound-browser-1",
			IdempotencyKey: "outbound-callleg-proof",
			PracticeID:     authorization.Practice.ID,
			LocationID:     authorization.Locations[0].ID,
			Destination:    "+15555550123",
		})
	if err != nil {
		t.Fatalf("start outbound Call: %v", err)
	}
	var callerLegs int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_call_legs
		WHERE call_id = $1 AND role = 'CALLER'
	`, call.ID).Scan(&callerLegs); err != nil || callerLegs != 1 {
		t.Fatalf("outbound caller CallLegs = %d, err = %v", callerLegs, err)
	}
	processAllCommands(t, calling)
	staffDial := provider.last(humancalling.CommandDialOutboundStaff)
	staffClientState, _ := staffDial.Payload["client_state"].(string)
	staffFact := humancalling.ProviderFact{
		EventID: "outbound-staff-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: "outbound-staff-control", CallLegID: "outbound-staff-provider-leg",
		CallSessionID: "outbound-staff-session", ClientState: staffClientState,
	}
	if err := calling.ApplyProviderFact(context.Background(), staffFact); err != nil {
		t.Fatalf("project outbound Staff initiation: %v", err)
	}
	staffFact.EventID = "outbound-staff-answered"
	staffFact.Type = humancalling.FactCallAnswered
	staffFact.OccurredAt = now.Add(2 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), staffFact); err != nil {
		t.Fatalf("project outbound Staff answer: %v", err)
	}
	callingState, err := calling.ReadCallingState(context.Background(), staff[0])
	if err != nil || len(callingState.Ringing) != 1 {
		t.Fatalf("read outbound media state: %#v, err = %v", callingState, err)
	}
	if _, err := calling.ConfirmOutboundMedia(context.Background(),
		humancalling.ConfirmOutboundMediaCommand{
			Identity: staff[0], SessionID: "outbound-browser-1", CallID: call.ID,
			MediaToken: callingState.Ringing[0].MediaToken,
		}); err != nil {
		t.Fatalf("confirm outbound Staff media: %v", err)
	}
	processAllCommands(t, calling)
	destinationDial := provider.last(humancalling.CommandDialOutboundDestination)
	if destinationDial.Payload["bridge_on_answer"] != false ||
		destinationDial.Payload["link_to"] != "outbound-staff-control" ||
		destinationDial.Payload["from"] != "+14843336938" {
		t.Fatalf("outbound destination Dial = %#v", destinationDial)
	}
	destinationClientState, _ := destinationDial.Payload["client_state"].(string)
	destinationFact := humancalling.ProviderFact{
		EventID: "destination-answered-first", Type: humancalling.FactCallAnswered,
		OccurredAt: now.Add(3 * time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: "destination-control", CallLegID: "destination-provider-leg",
		CallSessionID: "destination-session", ClientState: destinationClientState,
	}
	if err := calling.ApplyProviderFact(context.Background(), destinationFact); err != nil {
		t.Fatalf("project reordered destination answer: %v", err)
	}
	destinationFact.EventID = "destination-initiated-after-answer"
	destinationFact.Type = humancalling.FactCallInitiated
	destinationFact.OccurredAt = now.Add(4 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), destinationFact); err != nil {
		t.Fatalf("project reordered destination initiation: %v", err)
	}
	processAllCommands(t, calling)
	bridge := provider.last(humancalling.CommandBridge)
	if bridge.TargetID != "destination-control" ||
		bridge.Payload["call_control_id"] != "outbound-staff-control" ||
		bridge.Payload["prevent_double_bridge"] != true ||
		bridge.Payload["record"] != "record-from-answer" ||
		bridge.Payload["record_channels"] != "dual" ||
		bridge.Payload["record_format"] != "mp3" ||
		bridge.Payload["record_track"] != "both" {
		t.Fatalf("outbound explicit Bridge = %#v", bridge)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "outbound-destination-hangup-delivered-first", Type: humancalling.FactCallHangup,
		OccurredAt: now.Add(6 * time.Second), CallControlID: "destination-control",
		CallLegID: "destination-provider-leg", CallSessionID: "destination-session",
		HangupCause: "NORMAL_CLEARING", TerminationSource: "DESTINATION",
	}); err != nil {
		t.Fatalf("project outbound Hangup before Bridge delivery: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "outbound-bridge-confirmed", Type: humancalling.FactCallBridged,
		OccurredAt: now.Add(5 * time.Second), CallControlID: "destination-control",
		CallLegID: "destination-provider-leg", CallSessionID: "destination-session",
	}); err != nil {
		t.Fatalf("confirm outbound Bridge: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "outbound-staff-bridge-confirmed", Type: humancalling.FactCallBridged,
		OccurredAt: now.Add(5 * time.Second), CallControlID: "outbound-staff-control",
		CallLegID: "outbound-staff-provider-leg", CallSessionID: "outbound-staff-session",
	}); err != nil {
		t.Fatalf("confirm outbound Staff Bridge: %v", err)
	}
	destinationFact.EventID = "destination-initiated-after-bridge"
	destinationFact.OccurredAt = now.Add(6 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), destinationFact); err != nil {
		t.Fatalf("project late destination initiation: %v", err)
	}
	processAllCommands(t, calling)
	if provider.count(humancalling.CommandBridge) != 1 {
		t.Fatalf("reordered destination facts created duplicate Bridge: %#v", provider.commands)
	}
	var bridgedLegs int
	var terminal string
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_call_legs
		WHERE call_id = $1 AND bridged_at IS NOT NULL
	`, call.ID).Scan(&bridgedLegs); err != nil || bridgedLegs != 2 {
		t.Fatalf("outbound historical bridged CallLegs = %d, err = %v", bridgedLegs, err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT terminal_outcome FROM human_calling_calls WHERE id = $1
	`, call.ID).Scan(&terminal); err != nil || terminal != "ENDED" {
		t.Fatalf("outbound reordered Bridge terminal = %s, err = %v", terminal, err)
	}
	terminalCall, err := calling.RequestHangup(
		context.Background(), staff[1], "outbound-browser-2", call.ID,
	)
	if err != nil || terminalCall.State != humancalling.CallNeedsDisposition {
		t.Fatalf("authorized terminal Hangup = %#v, %v", terminalCall, err)
	}
}

func TestTerminalStaffHangupReconciliationReleasesSoftphone(t *testing.T) {
	pool, calling, provider, staff, callID, legID, commandID :=
		prepareTerminalStaffHangup(t, false)

	before, err := calling.ReadCallingState(context.Background(), staff)
	if err != nil || before.Softphone.Available || before.Softphone.ActiveCallID != callID {
		t.Fatalf("terminal Staff occupancy before reconciliation = %#v, %v", before.Softphone, err)
	}
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("reconcile terminal inactive Staff leg = %d, %v", reconciled, err)
	}

	var legState, commandState string
	if err := pool.QueryRow(context.Background(), `
		SELECT leg.state, command.state
		FROM human_calling_call_legs leg
		JOIN human_calling_provider_commands command ON command.call_leg_id = leg.id
		WHERE leg.id = $1 AND command.id = $2
	`, legID, commandID).Scan(&legState, &commandState); err != nil {
		t.Fatal(err)
	}
	after, err := calling.ReadCallingState(context.Background(), staff)
	if err != nil || legState != "ENDED" || commandState != "RECONCILED" ||
		!after.Softphone.Available || after.Softphone.ActiveCallID != "" {
		t.Fatalf("terminal Staff cleanup = leg=%s command=%s softphone=%#v err=%v observations=%d",
			legState, commandState, after.Softphone, err, len(provider.observations))
	}
}

func TestTerminalNeverStartedCallerReconciliationFailsOnce(t *testing.T) {
	now := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	provider := &recordingProvider{}
	pool, calling, terminalCallID, terminalLegID, activeLegID :=
		prepareTerminalNeverStartedCaller(
			t, now, "terminal-never-started-caller", provider,
		)

	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("reconcile terminal never-started caller = %d, %v", reconciled, err)
	}
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 0 {
		t.Fatalf("repeat terminal never-started caller = %d, %v", reconciled, err)
	}

	var terminalOutcome, terminalLegState, terminalError, activeLegState string
	var terminalEndingAt, terminalEndedAt *time.Time
	var evidenceCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome, terminal_leg.state,
			COALESCE(terminal_leg.error_code, ''),
			terminal_leg.ending_at, terminal_leg.ended_at,
			active_leg.state,
			(SELECT count(*) FROM human_calling_timeline timeline
			 WHERE timeline.call_id = call.id
				AND timeline.kind = 'call_leg.failed'
				AND timeline.error_code = 'CALL_TERMINATED_BEFORE_PROVIDER_START')
		FROM human_calling_calls call
		JOIN human_calling_call_legs terminal_leg
			ON terminal_leg.id = $2 AND terminal_leg.call_id = call.id
		JOIN human_calling_call_legs active_leg ON active_leg.id = $3
		WHERE call.id = $1
	`, terminalCallID, terminalLegID, activeLegID).Scan(
		&terminalOutcome, &terminalLegState, &terminalError,
		&terminalEndingAt, &terminalEndedAt, &activeLegState, &evidenceCount,
	); err != nil {
		t.Fatal(err)
	}
	if terminalOutcome != "UNANSWERED" || terminalLegState != "FAILED" ||
		terminalError != "CALL_TERMINATED_BEFORE_PROVIDER_START" ||
		terminalEndingAt == nil || terminalEndedAt == nil || evidenceCount != 1 ||
		activeLegState != "PENDING" || len(provider.observations) != 0 {
		t.Fatalf("terminal caller cleanup = Call:%s leg:%s/%s times:%v/%v evidence:%d active:%s observations:%d",
			terminalOutcome, terminalLegState, terminalError,
			terminalEndingAt, terminalEndedAt, evidenceCount,
			activeLegState, len(provider.observations))
	}

	lateCallerState := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"v":2,"call":%q,"call_leg":%q,"role":"CALLER","kind":"answer"}`,
		terminalCallID,
		terminalLegID,
	)))
	err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "late-terminal-caller-answer", Type: humancalling.FactCallAnswered,
		OccurredAt: now.Add(time.Second), ConnectionID: "late-connection",
		CallControlID: "late-control", CallLegID: "late-provider-leg",
		CallSessionID: "late-session", ClientState: lateCallerState,
	})
	if !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("correlated terminal caller fact classification = %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome, leg.state
		FROM human_calling_calls call
		JOIN human_calling_call_legs leg ON leg.call_id = call.id
		WHERE call.id = $1 AND leg.id = $2
	`, terminalCallID, terminalLegID).Scan(
		&terminalOutcome, &terminalLegState,
	); err != nil {
		t.Fatal(err)
	}
	if terminalOutcome != "UNANSWERED" || terminalLegState != "FAILED" {
		t.Fatalf("late caller fact revived terminal state = Call:%s leg:%s",
			terminalOutcome, terminalLegState)
	}
}

func TestTerminalNeverStartedCallerCleanupDoesNotRequireProviderObservation(t *testing.T) {
	now := time.Date(2026, time.August, 14, 11, 0, 0, 0, time.UTC)
	pool, calling, terminalCallID, terminalLegID, activeLegID :=
		prepareTerminalNeverStartedCaller(
			t, now, "terminal-cleanup-without-observation", commandOnlyProvider{},
		)

	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("cleanup without provider observation = %d, %v", reconciled, err)
	}
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 0 {
		t.Fatalf("repeat cleanup without provider observation = %d, %v", reconciled, err)
	}

	var terminalLegState, terminalError, activeLegState string
	var evidenceCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT terminal_leg.state, COALESCE(terminal_leg.error_code, ''),
			active_leg.state,
			(SELECT count(*) FROM human_calling_timeline timeline
			 WHERE timeline.call_id = $1
				AND timeline.kind = 'call_leg.failed'
				AND timeline.error_code = 'CALL_TERMINATED_BEFORE_PROVIDER_START')
		FROM human_calling_call_legs terminal_leg
		JOIN human_calling_call_legs active_leg ON active_leg.id = $3
		WHERE terminal_leg.call_id = $1 AND terminal_leg.id = $2
	`, terminalCallID, terminalLegID, activeLegID).Scan(
		&terminalLegState, &terminalError, &activeLegState, &evidenceCount,
	); err != nil {
		t.Fatal(err)
	}
	if terminalLegState != "FAILED" ||
		terminalError != "CALL_TERMINATED_BEFORE_PROVIDER_START" ||
		evidenceCount != 1 || activeLegState != "PENDING" {
		t.Fatalf("cleanup without observation = terminal:%s/%s evidence:%d active:%s",
			terminalLegState, terminalError, evidenceCount, activeLegState)
	}
}

func prepareTerminalNeverStartedCaller(
	t *testing.T,
	now time.Time,
	prefix string,
	provider humancalling.Provider,
) (*pgxpool.Pool, *humancalling.Module, string, string, string) {
	t.Helper()
	pool := testdb.Open(t)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(t, accessModule, now, prefix, 1)
	calling := humancalling.New(
		pool, accessModule, provider, humancalling.Config{}, func() time.Time { return now },
	)

	var terminalCallID, terminalLegID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO human_calling_calls (
			practice_id, location_id, direction, entry_point,
			terminal_outcome, ended_at, created_at, updated_at
		) VALUES (
			$1, $2, 'OUTBOUND', 'STANDALONE', 'UNANSWERED',
			$3::timestamptz - interval '2 minutes',
			$3::timestamptz - interval '2 minutes',
			$3::timestamptz - interval '2 minutes'
		)
		RETURNING id::text
	`, authorization.Practice.ID, authorization.Locations[0].ID, now).Scan(
		&terminalCallID,
	); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, state, created_at, updated_at
		) VALUES (
			$1, 'CALLER', 1, 'PENDING',
			$2::timestamptz - interval '2 minutes',
			$2::timestamptz - interval '2 minutes'
		)
		RETURNING id::text
	`, terminalCallID, now).Scan(&terminalLegID); err != nil {
		t.Fatal(err)
	}

	var activeCallID, activeLegID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO human_calling_calls (
			practice_id, location_id, direction, entry_point, created_at, updated_at
		) VALUES (
			$1, $2, 'OUTBOUND', 'STANDALONE',
			$3::timestamptz - interval '2 minutes',
			$3::timestamptz - interval '2 minutes'
		)
		RETURNING id::text
	`, authorization.Practice.ID, authorization.Locations[0].ID, now).Scan(
		&activeCallID,
	); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, state, created_at, updated_at
		) VALUES (
			$1, 'CALLER', 1, 'PENDING',
			$2::timestamptz - interval '2 minutes',
			$2::timestamptz - interval '2 minutes'
		)
		RETURNING id::text
	`, activeCallID, now).Scan(&activeLegID); err != nil {
		t.Fatal(err)
	}
	return pool, calling, terminalCallID, terminalLegID, activeLegID
}

type terminalStopRingWindowFixture struct {
	pool               *pgxpool.Pool
	calling            *humancalling.Module
	staff              []access.Identity
	callID             string
	callerLegID        string
	practiceID         string
	commandID          string
	beforeCommandCount int
}

func prepareTerminalStopRingWindow(
	t *testing.T,
	now time.Time,
	prefix string,
	provider *recordingProvider,
	commandState string,
) terminalStopRingWindowFixture {
	t.Helper()
	pool, calling, _, staff := prepareInboundFanout(t, now, prefix, provider, 1)
	var callID, callerLegID, practiceID string
	if err := pool.QueryRow(context.Background(), `
		SELECT call.id::text, caller.id::text, call.practice_id::text
		FROM human_calling_calls call
		JOIN human_calling_call_legs caller
			ON caller.call_id = call.id AND caller.role = 'CALLER'
	`).Scan(&callID, &callerLegID, &practiceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET state = 'RECONCILED', updated_at = $2
		WHERE call_id = $1
	`, callID, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("reconcile existing terminal Call commands: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_calls
		SET terminal_outcome = 'ENDED', ended_at = $2::timestamptz,
			disposition_deadline = $2::timestamptz + interval '30 seconds',
			updated_at = $2::timestamptz
		WHERE id = $1
	`, callID, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("terminalize ring-window Call: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs
		SET state = 'ENDED',
			ending_at = COALESCE(ending_at, answered_at, $2::timestamptz),
			ended_at = COALESCE(ended_at, ending_at, answered_at, $2::timestamptz),
			updated_at = $2::timestamptz
		WHERE call_id = $1
	`, callID, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("terminalize ring-window CallLegs: %v", err)
	}
	var commandID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO human_calling_provider_commands (
			call_id, call_leg_id, action, target_id, payload, state,
			next_attempt_at, sent_at, created_at, updated_at
		)
		SELECT $1, $2, 'STOP_RING_WINDOW', provider_call_control_id,
			jsonb_build_object(
				'stop', 'all',
				'client_state', (
					SELECT payload->>'client_state'
					FROM human_calling_provider_commands
					WHERE call_id = $1 AND action = 'START_RING_WINDOW'
					ORDER BY created_at, id LIMIT 1
				)
			),
			$4, $3,
			CASE WHEN $4 = 'SENT' THEN $3::timestamptz ELSE NULL END,
			$3, $3
		FROM human_calling_call_legs
		WHERE id = $2
		RETURNING id::text
	`, callID, callerLegID, now.Add(-2*time.Minute), commandState,
	).Scan(&commandID); err != nil {
		t.Fatalf("seed accepted Stop ring-window command: %v", err)
	}
	var beforeCommandCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_provider_commands WHERE call_id = $1
	`, callID).Scan(&beforeCommandCount); err != nil {
		t.Fatal(err)
	}
	return terminalStopRingWindowFixture{
		pool: pool, calling: calling, staff: staff,
		callID: callID, callerLegID: callerLegID, practiceID: practiceID,
		commandID: commandID, beforeCommandCount: beforeCommandCount,
	}
}

func TestTerminalCallerReconcilesAcceptedStopRingWindowOnce(t *testing.T) {
	now := time.Date(2026, time.August, 12, 15, 0, 0, 0, time.UTC)
	prefix := "terminal-stop-ring-window"
	fixture := prepareTerminalStopRingWindow(
		t, now, prefix, &recordingProvider{}, "SENT",
	)
	pool, calling, staff := fixture.pool, fixture.calling, fixture.staff
	callID, callerLegID := fixture.callID, fixture.callerLegID
	practiceID, commandID := fixture.practiceID, fixture.commandID
	beforeCommandCount := fixture.beforeCommandCount
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_timeline (
			call_id, practice_id, kind, provider_command_id,
			opaque_reference, error_code, occurred_at
		)
		VALUES ($1, $2, 'caller_audio.degraded', $3, $4,
			'STOP_RING_WINDOW_EVENT_ABSENT', $5)
	`, callID, practiceID, commandID, prefix, now.Add(-time.Minute)); err != nil {
		t.Fatalf("seed degraded caller-audio evidence: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET target_id = 'mismatched-provider-target'
		WHERE id = $1
	`, commandID); err != nil {
		t.Fatal(err)
	}
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 0 {
		t.Fatalf("mismatched terminal Stop ring-window = %d, %v", reconciled, err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands command
		SET target_id = caller.provider_call_control_id
		FROM human_calling_call_legs caller
		WHERE command.id = $1 AND caller.id = command.call_leg_id
	`, commandID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET payload = jsonb_set(payload, '{client_state}', '"malformed-client-state"')
		WHERE id = $1
	`, commandID); err != nil {
		t.Fatal(err)
	}
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 0 {
		t.Fatalf("malformed terminal Stop ring-window = %d, %v", reconciled, err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands command
		SET payload = jsonb_set(command.payload, '{client_state}', to_jsonb(start.payload->>'client_state'))
		FROM human_calling_provider_commands start
		WHERE command.id = $1 AND start.call_id = command.call_id
			AND start.action = 'START_RING_WINDOW'
	`, commandID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs
		SET updated_at = $1
		WHERE id = $2
	`, now.Add(-2*time.Minute), callerLegID); err != nil {
		t.Fatal(err)
	}

	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("reconcile terminal Stop ring-window = %d, %v", reconciled, err)
	}
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 0 {
		t.Fatalf("repeat terminal Stop ring-window = %d, %v", reconciled, err)
	}

	var commandState, commandReason, terminalOutcome, callerState string
	var terminalEvidenceReason, convergedAudioReason string
	var runnableCommands, terminalizedEvents, convergedAudio, commandCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT command.state, COALESCE(command.last_error_code, ''),
			call.terminal_outcome, caller.state,
			(SELECT count(*) FROM human_calling_provider_commands
			 WHERE call_id = call.id AND state IN ('PENDING', 'SENDING', 'AMBIGUOUS')),
			(SELECT count(*) FROM human_calling_timeline
			 WHERE call_id = call.id AND kind = 'ring_window.terminalized'),
			(SELECT count(*) FROM human_calling_timeline
			 WHERE call_id = call.id AND kind = 'caller_audio.converged'),
			COALESCE((SELECT max(error_code) FROM human_calling_timeline
			 WHERE call_id = call.id AND kind = 'ring_window.terminalized'), ''),
			COALESCE((SELECT max(error_code) FROM human_calling_timeline
			 WHERE call_id = call.id AND kind = 'caller_audio.converged'), ''),
			(SELECT count(*) FROM human_calling_provider_commands
			 WHERE call_id = call.id)
		FROM human_calling_provider_commands command
		JOIN human_calling_calls call ON call.id = command.call_id
		JOIN human_calling_call_legs caller ON caller.id = command.call_leg_id
		WHERE command.id = $1
	`, commandID).Scan(
		&commandState, &commandReason, &terminalOutcome, &callerState,
		&runnableCommands, &terminalizedEvents, &convergedAudio,
		&terminalEvidenceReason, &convergedAudioReason, &commandCount,
	); err != nil {
		t.Fatal(err)
	}
	callingState, err := calling.ReadCallingState(context.Background(), staff[0])
	if err != nil {
		t.Fatalf("read Staff state after terminal Stop convergence: %v", err)
	}
	if commandState != "RECONCILED" || commandReason != "" ||
		terminalOutcome != "ENDED" || callerState != "ENDED" ||
		runnableCommands != 0 || terminalizedEvents != 1 || convergedAudio != 1 ||
		terminalEvidenceReason != "CALL_TERMINAL" ||
		convergedAudioReason != "CALL_TERMINAL" ||
		commandCount != beforeCommandCount || callingState.Softphone.ActiveCallID != "" {
		t.Fatalf("terminal Stop convergence = command:%s/%s Call:%s caller:%s runnable:%d timeline:%d/%d reasons:%s/%s commands:%d softphone:%#v",
			commandState, commandReason, terminalOutcome, callerState,
			runnableCommands, terminalizedEvents, convergedAudio,
			terminalEvidenceReason, convergedAudioReason, commandCount,
			callingState.Softphone)
	}
}

func TestTerminalCallerTreatsAbsentStopRingWindowTargetAsConvergedOnce(t *testing.T) {
	now := time.Date(2026, time.August, 18, 20, 30, 0, 0, time.UTC)
	provider := &recordingProvider{actionErrors: map[humancalling.CommandAction][]error{
		humancalling.CommandStopRingWindow: {
			fmt.Errorf("%w: synthetic terminal Stop target absent",
				humancalling.ErrProviderTargetAbsent),
		},
	}}
	fixture := prepareTerminalStopRingWindow(
		t, now, "terminal-stop-target-absent", provider, "PENDING",
	)

	processed, err := fixture.calling.ProcessNextCommand(context.Background())
	if err != nil || !processed {
		t.Fatalf("process terminal absent Stop target = %t, %v", processed, err)
	}
	processed, err = fixture.calling.ProcessNextCommand(context.Background())
	if err != nil || processed {
		t.Fatalf("repeat terminal absent Stop target = %t, %v", processed, err)
	}

	var commandState, commandError string
	var degradedAudio, terminalized int
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT command.state, COALESCE(command.last_error_code, ''),
			(SELECT count(*) FROM human_calling_timeline
			 WHERE call_id = command.call_id AND provider_command_id = command.id
				AND kind = 'caller_audio.degraded'),
			(SELECT count(*) FROM human_calling_timeline
			 WHERE call_id = command.call_id AND provider_command_id = command.id
				AND kind = 'ring_window.terminalized')
		FROM human_calling_provider_commands command
		WHERE command.id = $1
	`, fixture.commandID).Scan(
		&commandState, &commandError, &degradedAudio, &terminalized,
	); err != nil {
		t.Fatal(err)
	}
	if commandState != "RECONCILED" || commandError != "" ||
		degradedAudio != 0 || terminalized != 1 ||
		provider.count(humancalling.CommandStopRingWindow) != 1 {
		t.Fatalf("terminal absent Stop convergence = command:%s/%s degraded:%d terminalized:%d effects:%d",
			commandState, commandError, degradedAudio, terminalized,
			provider.count(humancalling.CommandStopRingWindow))
	}
}

func TestTerminalCallerReconcilesPreviouslyRejectedStopRingWindowWithoutProviderEffect(t *testing.T) {
	now := time.Date(2026, time.August, 18, 20, 45, 0, 0, time.UTC)
	provider := &recordingProvider{}
	fixture := prepareTerminalStopRingWindow(
		t, now, "terminal-rejected-stop", provider, "FAILED",
	)
	if _, err := fixture.pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET last_error_code = 'STOP_RING_WINDOW_EVENT_ABSENT'
		WHERE id = $1
	`, fixture.commandID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(context.Background(), `
		INSERT INTO human_calling_timeline (
			call_id, practice_id, kind, provider_command_id,
			opaque_reference, error_code, occurred_at
		) VALUES ($1, $2, 'caller_audio.degraded', $3, $4,
			'STOP_RING_WINDOW_EVENT_ABSENT', $5)
	`, fixture.callID, fixture.practiceID, fixture.commandID,
		"terminal-rejected-stop", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	if reconciled, err := fixture.calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("reconcile rejected terminal Stop = %d, %v", reconciled, err)
	}
	var commandState, commandError string
	var convergedAudio, terminalized int
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT command.state, COALESCE(command.last_error_code, ''),
			(SELECT count(*) FROM human_calling_timeline
			 WHERE provider_command_id = command.id AND kind = 'caller_audio.converged'),
			(SELECT count(*) FROM human_calling_timeline
			 WHERE provider_command_id = command.id AND kind = 'ring_window.terminalized')
		FROM human_calling_provider_commands command WHERE command.id = $1
	`, fixture.commandID).Scan(
		&commandState, &commandError, &convergedAudio, &terminalized,
	); err != nil {
		t.Fatal(err)
	}
	if commandState != "RECONCILED" || commandError != "" ||
		convergedAudio != 1 || terminalized != 1 ||
		provider.count(humancalling.CommandStopRingWindow) != 0 {
		t.Fatalf("rejected terminal Stop convergence = command:%s/%s converged:%d terminalized:%d effects:%d",
			commandState, commandError, convergedAudio, terminalized,
			provider.count(humancalling.CommandStopRingWindow))
	}
}

func TestStopRingWindowCompletionAndTerminalReconciliationUseConsistentLockOrder(t *testing.T) {
	now := time.Date(2026, time.August, 17, 20, 0, 0, 0, time.UTC)
	prefix := "terminal-stop-ring-window-lock-order"
	provider := &recordingProvider{}
	fixture := prepareTerminalStopRingWindow(t, now, prefix, provider, "PENDING")
	pool, calling := fixture.pool, fixture.calling

	provider.mu.Lock()
	provider.blockAction = humancalling.CommandStopRingWindow
	provider.blockStarted = make(chan struct{})
	provider.blockRelease = make(chan struct{})
	started, release := provider.blockStarted, provider.blockRelease
	provider.mu.Unlock()

	commandResult := make(chan error, 1)
	go func() {
		processed, err := calling.ProcessNextCommand(context.Background())
		if !processed && err == nil {
			err = errors.New("no Stop ring-window command processed")
		}
		commandResult <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop ring-window provider execution did not start")
	}

	const barrierKey int64 = 817200001
	barrier := holdPostgresAdvisoryLock(t, pool, barrierKey)
	defer barrier.close()

	const triggerName = "test_block_terminal_stop_reconciliation"
	const functionName = "test_wait_for_terminal_stop_reconciliation"
	installPostgresTestTrigger(t, pool, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $function$
		BEGIN
			PERFORM pg_advisory_xact_lock(TG_ARGV[0]::bigint);
			RETURN NEW;
		END
		$function$;
		CREATE TRIGGER %s
		BEFORE UPDATE ON human_calling_provider_commands
		FOR EACH ROW WHEN (NEW.id = '%s'::uuid AND NEW.state = 'RECONCILED')
		EXECUTE FUNCTION %s('%d')
	`, functionName, triggerName, fixture.commandID, functionName, barrierKey), fmt.Sprintf(`
		DROP TRIGGER IF EXISTS %s ON human_calling_provider_commands;
		DROP FUNCTION IF EXISTS %s()
	`, triggerName, functionName))

	reconciliationResult := make(chan error, 1)
	go func() {
		reconciled, err := calling.ReconcileStaleCalls(context.Background())
		if reconciled != 1 && err == nil {
			err = fmt.Errorf("reconciled %d terminal Calls, want 1", reconciled)
		}
		reconciliationResult <- err
	}()
	reconciliationPID := waitForPostgresLockWaiter(
		t, barrier.connection, "advisory", barrier.pid,
	)
	close(release)
	waitForPostgresLockWaiter(
		t, barrier.connection, "transactionid", reconciliationPID,
	)
	barrier.release()

	select {
	case err := <-reconciliationResult:
		if err != nil {
			t.Fatalf("terminalize concurrent Stop ring-window: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("terminal Stop ring-window reconciliation did not finish")
	}
	select {
	case err := <-commandResult:
		if err != nil {
			t.Fatalf("finish concurrent Stop ring-window: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop ring-window command did not finish")
	}

	var commandState string
	var terminalizedEvents int
	if err := pool.QueryRow(context.Background(), `
		SELECT command.state,
			(SELECT count(*) FROM human_calling_timeline
			 WHERE call_id = command.call_id AND kind = 'ring_window.terminalized')
		FROM human_calling_provider_commands command
		WHERE command.id = $1
	`, fixture.commandID).Scan(&commandState, &terminalizedEvents); err != nil {
		t.Fatal(err)
	}
	if commandState != "RECONCILED" || terminalizedEvents != 1 ||
		provider.count(humancalling.CommandStopRingWindow) != 1 {
		t.Fatalf("terminal Stop convergence = command:%s events:%d executions:%d",
			commandState, terminalizedEvents,
			provider.count(humancalling.CommandStopRingWindow))
	}
}

func TestTerminalActiveStaffHangupRetriesExactCleanup(t *testing.T) {
	pool, calling, provider, _, callID, legID, commandID :=
		prepareTerminalStaffHangup(t, true)
	dialCount := provider.count(humancalling.CommandDialStaff)

	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("reconcile terminal active Staff leg = %d, %v", reconciled, err)
	}

	var terminal, originalState, retryLegID, retryTarget string
	if err := pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome, original.state,
			retry.call_leg_id::text, retry.target_id
		FROM human_calling_calls call
		JOIN human_calling_provider_commands original ON original.id = $2
		JOIN human_calling_provider_commands retry
			ON retry.call_id = call.id AND retry.action = 'HANGUP_LEG'
			AND retry.state = 'PENDING' AND retry.id <> original.id
		WHERE call.id = $1
	`, callID, commandID).Scan(
		&terminal, &originalState, &retryLegID, &retryTarget,
	); err != nil {
		t.Fatal(err)
	}
	if terminal != "ENDED" || originalState != "FAILED" || retryLegID != legID ||
		retryTarget != "terminal-staff-control" ||
		provider.count(humancalling.CommandDialStaff) != dialCount {
		t.Fatalf("active terminal cleanup = terminal=%s original=%s retry=%s/%s dials=%d want %d",
			terminal, originalState, retryLegID, retryTarget,
			provider.count(humancalling.CommandDialStaff), dialCount)
	}
}

func TestWorkerMetricsReportTerminalStaffCleanupInvariant(t *testing.T) {
	var metrics bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimeWorker,
		"worker-terminal-cleanup-test",
		slog.New(slog.NewJSONHandler(&metrics, nil)),
	)
	pool, calling, _, _, _, legID, _ := prepareTerminalStaffHangup(t, false, observer)
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs
		SET updated_at = ending_at + interval '2 minutes'
		WHERE id = $1
	`, legID); err != nil {
		t.Fatal(err)
	}

	if err := calling.ReportReceiptQueue(context.Background()); err != nil {
		t.Fatalf("report terminal cleanup invariant: %v", err)
	}
	for _, fragment := range []string{
		`"metric":"acuity_call_center_terminal_cleanup"`,
		`"staff_occupancy":1`,
		`"unresolved_hangups":1`,
		`"oldest_staff_occupancy_seconds":120`,
		`"oldest_hangup_seconds":120`,
	} {
		if !strings.Contains(metrics.String(), fragment) {
			t.Fatalf("terminal cleanup metric omitted %s: %s", fragment, metrics.String())
		}
	}
}

func TestConcurrentCommandWorkersSerializePerCallWithoutStarvingOtherCalls(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 10, 15, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(t, accessModule, now, "command-claim", 1)
	provider := &recordingProvider{
		blockAction:  humancalling.CommandStopRingWindow,
		blockStarted: make(chan struct{}),
		blockRelease: make(chan struct{}),
	}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain:       "synthetic.sip.telnyx.com",
		StaffSIPDomain:         "sip.telnyx.com",
		RingWindowDuration:     20 * time.Second,
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
		FromNumber:             "+14843336938",
		RingbackURL:            "https://media.synthetic.test/ringback.wav",
	}, func() time.Time { return now })

	callIDs := make([]string, 2)
	for index := range callIDs {
		suffix := fmt.Sprint(index + 1)
		if _, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
			Service: humancalling.ServiceIdentity{
				Subject: "abita-command-claim", PracticeID: authorization.Practice.ID,
			},
			LocationID:     authorization.Locations[0].ID,
			SourceCallID:   "command-claim-source-" + suffix,
			IdempotencyKey: "command-claim-handoff-" + suffix,
			Contact:        humancalling.ContactContext{Phone: "+1555555010" + suffix},
		}); err != nil {
			t.Fatalf("create command claim handoff %s: %v", suffix, err)
		}
		if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
			EventID: "command-claim-initiated-" + suffix,
			Type:    humancalling.FactCallInitiated, OccurredAt: now,
			ConnectionID:  "staff-call-control-connection",
			CallControlID: "command-claim-control-" + suffix,
			CallLegID:     "command-claim-leg-" + suffix,
			CallSessionID: "command-claim-session-" + suffix,
			From:          "+1555555010" + suffix, To: "+14843989071",
		}); err != nil {
			t.Fatalf("create command claim Call %s: %v", suffix, err)
		}
		if err := pool.QueryRow(context.Background(), `
			SELECT call.id::text
			FROM human_calling_calls call
			JOIN human_calling_call_legs leg ON leg.call_id = call.id
			WHERE leg.role = 'CALLER' AND leg.provider_call_control_id = $1
		`, "command-claim-control-"+suffix).Scan(&callIDs[index]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(context.Background(), `
		DELETE FROM human_calling_provider_commands
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_provider_commands (
			call_id, action, target_id, payload, created_at, next_attempt_at
		) VALUES
			($1, 'STOP_RING_WINDOW', 'call-a-first', '{}', $3::timestamptz - interval '3 seconds', $3),
			($1, 'STOP_RING_WINDOW', 'call-a-second', '{}', $3::timestamptz - interval '2 seconds', $3),
			($2, 'STOP_RING_WINDOW', 'call-b', '{}', $3::timestamptz - interval '1 second', $3)
	`, callIDs[0], callIDs[1], now); err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan error, 1)
	go func() {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err == nil && !processed {
			err = errors.New("first worker found no command")
		}
		firstDone <- err
	}()
	select {
	case <-provider.blockStarted:
	case <-time.After(time.Second):
		t.Fatal("first command did not reach provider barrier")
	}

	processed, secondErr := calling.ProcessNextCommand(context.Background())
	close(provider.blockRelease)
	if firstErr := <-firstDone; firstErr != nil {
		t.Fatalf("first command worker: %v", firstErr)
	}
	if secondErr != nil || !processed {
		t.Fatalf("second command worker = processed=%t err=%v", processed, secondErr)
	}
	var pendingA, sentB int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			count(*) FILTER (WHERE call_id = $1 AND state = 'PENDING'),
			count(*) FILTER (WHERE call_id = $2 AND state = 'SENT')
		FROM human_calling_provider_commands
	`, callIDs[0], callIDs[1]).Scan(&pendingA, &sentB); err != nil {
		t.Fatal(err)
	}
	if pendingA != 1 || sentB != 1 {
		t.Fatalf("concurrent command claims left pending Call A=%d sent Call B=%d", pendingA, sentB)
	}
}

func TestConcurrentCommandWorkersDialIndependentStaffCallLegsForSameCall(t *testing.T) {
	const staffCount = 10
	now := time.Date(2026, time.August, 19, 18, 30, 0, 0, time.UTC)
	seedProvider := &recordingProvider{}
	pool, _, _, _ := prepareInboundFanout(
		t, now, "concurrent-staff-dial", seedProvider, staffCount,
	)
	if _, err := pool.Exec(context.Background(), `
		DELETE FROM human_calling_provider_commands WHERE action <> 'DIAL_STAFF'
	`); err != nil {
		t.Fatal(err)
	}
	workerConfig, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	workerConfig.MaxConns = 2
	workerPool, err := pgxpool.NewWithConfig(context.Background(), workerConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workerPool.Close)

	provider := &blockingDialProvider{
		started: make(chan struct{}, staffCount),
		release: make(chan struct{}),
	}
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(provider.release) }) })
	accessModule := access.New(workerPool, func() time.Time { return now })
	calling := humancalling.New(
		workerPool,
		accessModule,
		provider,
		humancalling.Config{},
		func() time.Time { return now },
	)
	runner, err := worker.New(worker.Config{
		WorkInterval:                  5 * time.Millisecond,
		WorkTimeout:                   time.Second,
		CredentialInterval:            time.Hour,
		CredentialTimeout:             time.Second,
		HealthInterval:                time.Hour,
		HealthTimeout:                 time.Second,
		MetricInterval:                time.Hour,
		MetricTimeout:                 time.Second,
		ReceiptBatchSize:              1,
		RecoveryAndMessagingBatchSize: 1,
		ProviderCommandBatchSize:      1,
		CommandWorkers:                staffCount,
		IdleBackoffMax:                20 * time.Millisecond,
		ErrorBackoffMin:               5 * time.Millisecond,
		ErrorBackoffMax:               20 * time.Millisecond,
	}, calling, idleMessagingWork{}, idleInteractionWork{}, workerPool)
	if err != nil {
		t.Fatal(err)
	}
	runnerContext, cancelRunner := context.WithCancel(context.Background())
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- runner.Run(runnerContext) }()
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(provider.release) })
		cancelRunner()
		select {
		case err := <-runnerDone:
			if err != nil {
				t.Errorf("stop concurrent worker runner: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("concurrent worker runner did not stop")
		}
	})
	for index := 1; index <= staffCount; index++ {
		select {
		case <-provider.started:
		case <-time.After(time.Second):
			t.Fatalf("Staff Dial %d did not start while prior Dials remained in flight", index)
		}
	}
	if got := workerPool.Stat().MaxConns(); got != 2 {
		t.Fatalf("worker pool maximum connections = %d, want 2", got)
	}
	receiptBody := []byte(`{"data":{"record_type":"event","event_type":"call.synthetic_unknown","id":"concurrent-staff-dial-receipt","occurred_at":"2026-08-19T18:30:00Z","payload":{}}}`)
	if _, err := workerPool.Exec(context.Background(), `
		INSERT INTO human_calling_provider_receipts (
			event_id, event_type, occurred_at, received_at,
			signature_timestamp, raw_body, next_attempt_at
		) VALUES (
			'concurrent-staff-dial-receipt', 'call.synthetic_unknown',
			$1, $1, $2, $3, $1
		)
	`, now, now.Unix(), receiptBody); err != nil {
		t.Fatalf("seed receipt during concurrent Staff Dials: %v", err)
	}
	receiptDeadline := time.Now().Add(500 * time.Millisecond)
	for {
		var receiptState string
		err := workerPool.QueryRow(context.Background(), `
			SELECT state FROM human_calling_provider_receipts
			WHERE event_id = 'concurrent-staff-dial-receipt'
		`).Scan(&receiptState)
		if err == nil && receiptState == "UNKNOWN" {
			break
		}
		if time.Now().After(receiptDeadline) {
			t.Fatalf("receipt did not progress while Staff Dials were in flight: state=%q err=%v",
				receiptState, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	releaseOnce.Do(func() { close(provider.release) })
	commandDeadline := time.Now().Add(time.Second)
	for {
		var sent int
		if err := workerPool.QueryRow(context.Background(), `
			SELECT count(*) FROM human_calling_provider_commands
			WHERE action = 'DIAL_STAFF' AND state = 'SENT'
		`).Scan(&sent); err != nil {
			t.Fatal(err)
		}
		if sent == staffCount {
			break
		}
		if time.Now().After(commandDeadline) {
			t.Fatalf("completed concurrent Staff Dials = %d, want %d", sent, staffCount)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if provider.count() != staffCount {
		t.Fatalf("concurrent Staff Dial effects = %d, want %d",
			provider.count(), staffCount)
	}
}

type idleMessagingWork struct{}

func (idleMessagingWork) QueueNextTaskAcknowledgement(context.Context) (bool, error) {
	return false, nil
}

func (idleMessagingWork) ProcessNextReceipt(context.Context) (bool, error) {
	return false, nil
}

func (idleMessagingWork) ProcessNextCommand(context.Context) (bool, error) {
	return false, nil
}

func (idleMessagingWork) RecoverInterruptedCommands(context.Context) error {
	return nil
}

func (idleMessagingWork) ReconcileNextCommand(context.Context) (bool, error) {
	return false, nil
}

func (idleMessagingWork) ProcessNextAttachment(context.Context) (bool, error) {
	return false, nil
}

func (idleMessagingWork) ExpirePendingAttachments(context.Context) error {
	return nil
}

type idleInteractionWork struct{}

func (idleInteractionWork) ProcessNextReceipt(context.Context) (bool, error) {
	return false, nil
}

func TestConcurrentStaffDialsPreserveOneWinnerAndCleanUpLosers(t *testing.T) {
	const staffCount = 3
	now := time.Date(2026, time.August, 19, 19, 30, 0, 0, time.UTC)
	seedProvider := &recordingProvider{}
	pool, _, _, _ := prepareInboundFanout(
		t, now, "concurrent-dial-winner", seedProvider, staffCount,
	)
	if _, err := pool.Exec(context.Background(), `
		DELETE FROM human_calling_provider_commands WHERE action <> 'DIAL_STAFF'
	`); err != nil {
		t.Fatal(err)
	}
	provider := &blockingDialProvider{
		started: make(chan struct{}, staffCount),
		release: make(chan struct{}),
	}
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(provider.release) }) })
	accessModule := access.New(pool, func() time.Time { return now })
	calling := humancalling.New(
		pool, accessModule, provider, humancalling.Config{}, func() time.Time { return now },
	)
	type processResult struct {
		processed bool
		err       error
	}
	results := make(chan processResult, staffCount)
	for index := 1; index <= staffCount; index++ {
		go func() {
			processed, err := calling.ProcessNextCommand(context.Background())
			results <- processResult{processed: processed, err: err}
		}()
		select {
		case <-provider.started:
		case result := <-results:
			t.Fatalf("Staff Dial worker %d exited before provider execution = %t, %v",
				index, result.processed, result.err)
		case <-time.After(time.Second):
			t.Fatalf("Staff Dial %d did not start concurrently", index)
		}
	}
	dials := provider.dials()
	if len(dials) != staffCount {
		t.Fatalf("in-flight Staff Dials = %d, want %d", len(dials), staffCount)
	}
	facts := make([]humancalling.ProviderFact, staffCount)
	for index, dial := range dials {
		clientState, _ := dial.command.Payload["client_state"].(string)
		facts[index] = humancalling.ProviderFact{
			EventID: fmt.Sprintf("concurrent-dial-winner-initiated-%d", index+1),
			Type:    humancalling.FactCallInitiated, OccurredAt: now.Add(time.Second),
			ConnectionID:  "staff-call-control-connection",
			CallControlID: dial.result.CallControlID,
			CallLegID:     dial.result.CallLegID,
			CallSessionID: fmt.Sprintf("concurrent-dial-winner-session-%d", index+1),
			ClientState:   clientState,
		}
		if err := calling.ApplyProviderFact(context.Background(), facts[index]); err != nil {
			t.Fatalf("project in-flight Staff Dial %d: %v", index+1, err)
		}
	}
	answerErrors := make(chan error, 2)
	for index := range 2 {
		answer := facts[index]
		answer.EventID = fmt.Sprintf("concurrent-dial-winner-answered-%d", index+1)
		answer.Type = humancalling.FactCallAnswered
		go func() {
			answerErrors <- calling.ApplyProviderFact(context.Background(), answer)
		}()
	}
	for range 2 {
		if err := <-answerErrors; err != nil {
			t.Fatalf("project concurrent in-flight Staff answer: %v", err)
		}
	}
	var provisionalWinners, bridgeCommands int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM human_calling_call_legs
			 WHERE state = 'BRIDGE_PENDING'),
			(SELECT count(*) FROM human_calling_provider_commands
			 WHERE action = 'BRIDGE')
	`).Scan(&provisionalWinners, &bridgeCommands); err != nil {
		t.Fatal(err)
	}
	if provisionalWinners != 1 || bridgeCommands != 1 {
		t.Fatalf("concurrent in-flight answers = %d winners/%d Bridge commands, want 1/1",
			provisionalWinners, bridgeCommands)
	}

	releaseOnce.Do(func() { close(provider.release) })
	for range staffCount {
		result := <-results
		if result.err != nil || !result.processed {
			t.Fatalf("complete concurrent Staff Dial = %t, %v", result.processed, result.err)
		}
	}
	processAllCommands(t, calling)
	bridges := provider.all(humancalling.CommandBridge)
	if len(bridges) != 1 {
		t.Fatalf("executed Bridge commands = %d, want 1", len(bridges))
	}
	bridge := bridges[0]
	var winner blockingDialExecution
	var winnerFact humancalling.ProviderFact
	for index, dial := range dials {
		if dial.command.CallLegID == bridge.CallLegID {
			winner = dial
			winnerFact = facts[index]
			break
		}
	}
	if winner.command.CallLegID == "" {
		t.Fatalf("Bridge winner %q was not an in-flight Staff Dial", bridge.CallLegID)
	}
	bridgeClientState, _ := bridge.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "concurrent-dial-winner-bridged", Type: humancalling.FactCallBridged,
		OccurredAt:    now.Add(2 * time.Second),
		CallControlID: winner.result.CallControlID,
		CallLegID:     winner.result.CallLegID,
		CallSessionID: winnerFact.CallSessionID,
		ClientState:   bridgeClientState,
	}); err != nil {
		t.Fatalf("confirm concurrent Dial winner Bridge: %v", err)
	}
	processAllCommands(t, calling)
	var bridged, ending int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			count(*) FILTER (WHERE state = 'BRIDGED'),
			count(*) FILTER (WHERE state = 'ENDING')
		FROM human_calling_call_legs WHERE role = 'STAFF'
	`).Scan(&bridged, &ending); err != nil {
		t.Fatal(err)
	}
	if bridged != 1 || ending != staffCount-1 ||
		len(provider.all(humancalling.CommandHangupLeg)) != staffCount-1 {
		t.Fatalf("concurrent Dial cleanup = %d bridged/%d ending/%d Hangups, want 1/%d/%d",
			bridged, ending, len(provider.all(humancalling.CommandHangupLeg)),
			staffCount-1, staffCount-1)
	}
}

func TestCommandWorkerRetriesAfterLosingClaimRace(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 13, 10, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(t, accessModule, now, "lost-command-claim", 1)

	callIDs := make([]string, 2)
	legIDs := make([]string, 2)
	for index := range callIDs {
		if err := pool.QueryRow(context.Background(), `
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point, created_at, updated_at
			) VALUES ($1, $2, 'INBOUND', 'STANDALONE', $3, $3)
			RETURNING id::text
		`, authorization.Practice.ID, authorization.Locations[0].ID, now).Scan(&callIDs[index]); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(context.Background(), `
			INSERT INTO human_calling_call_legs (
				call_id, role, sequence, state, created_at, updated_at
			) VALUES ($1, 'CALLER', 1, 'RINGING', $2, $2)
			RETURNING id::text
		`, callIDs[index], now).Scan(&legIDs[index]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_provider_commands (
			call_id, call_leg_id, action, target_id, payload, created_at, next_attempt_at
		) VALUES
			($1, $2, 'STOP_RING_WINDOW', 'claimed-elsewhere', '{}', $5::timestamptz - interval '1 second', $5),
			($3, $4, 'STOP_RING_WINDOW', 'available-after-race', '{}', $5, $5)
	`, callIDs[0], legIDs[0], callIDs[1], legIDs[1], now); err != nil {
		t.Fatal(err)
	}

	claimStarted := make(chan struct{})
	claimRelease := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-claimRelease:
		default:
			close(claimRelease)
		}
	})
	config, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	config.ConnConfig.Tracer = &commandClaimTracer{
		started: claimStarted,
		release: claimRelease,
	}
	workerPool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workerPool.Close)
	provider := &recordingProvider{}
	calling := humancalling.New(
		workerPool, nil, provider, humancalling.Config{}, func() time.Time { return now },
	)

	type processResult struct {
		processed bool
		err       error
	}
	result := make(chan processResult, 1)
	go func() {
		processed, err := calling.ProcessNextCommand(context.Background())
		result <- processResult{processed: processed, err: err}
	}()
	select {
	case <-claimStarted:
	case <-time.After(time.Second):
		t.Fatal("provider command claim did not reach the race barrier")
	}
	tag, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET state = 'SENT', sent_at = $2, updated_at = $2
		WHERE target_id = $1 AND state = 'PENDING'
	`, "claimed-elsewhere", now)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("competing command claims = %d, want 1", tag.RowsAffected())
	}
	close(claimRelease)

	var processed processResult
	select {
	case processed = <-result:
	case <-time.After(time.Second):
		t.Fatal("command worker did not recover from the lost claim")
	}
	if processed.err != nil || !processed.processed {
		t.Fatalf("process command after lost claim = %t, %v", processed.processed, processed.err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.commands) != 1 || provider.commands[0].TargetID != "available-after-race" {
		t.Fatalf("provider commands after lost claim = %#v", provider.commands)
	}
}

type commandClaimTracer struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (tracer *commandClaimTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	if strings.Contains(data.SQL, "attempts = attempts + 1") {
		tracer.once.Do(func() {
			close(tracer.started)
			<-tracer.release
		})
	}
	return ctx
}

func (*commandClaimTracer) TraceQueryEnd(
	context.Context,
	*pgx.Conn,
	pgx.TraceQueryEndData,
) {
}

func TestCommandWorkerPreservesGlobalOrderAcrossCallAndCredentialWork(t *testing.T) {
	now := time.Date(2026, time.August, 10, 16, 0, 0, 0, time.UTC)
	provider := &recordingProvider{}
	pool, calling, _, staff := prepareInboundFanout(
		t, now, "command-global-order", provider, 1,
	)
	var callID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text FROM human_calling_calls LIMIT 1
	`).Scan(&callID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		DELETE FROM human_calling_provider_commands
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_provider_commands (
			user_subject, action, payload, created_at, next_attempt_at
		) VALUES (
			$1, 'CREATE_CREDENTIAL',
			jsonb_build_object(
				'connection_id', 'staff-credential-connection',
				'name', 'global-order-credential',
				'tag', 'acuity-portal'
			),
			$2::timestamptz - interval '2 seconds', $2
		)
	`, staff[0].Subject, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_provider_commands (
			call_id, action, target_id, payload, created_at, next_attempt_at
		) VALUES (
			$1, 'STOP_RING_WINDOW', 'global-order-call', '{}',
			$2::timestamptz - interval '1 second', $2
		)
	`, callID, now); err != nil {
		t.Fatal(err)
	}

	processed, err := calling.ProcessNextCommand(context.Background())
	if err != nil || !processed {
		t.Fatalf("process globally oldest provider command = %t, %v", processed, err)
	}
	var credentialState, callState string
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT state FROM human_calling_provider_commands
				WHERE call_id IS NULL AND action = 'CREATE_CREDENTIAL'),
			(SELECT state FROM human_calling_provider_commands
				WHERE call_id = $1 AND action = 'STOP_RING_WINDOW')
	`, callID).Scan(&credentialState, &callState); err != nil {
		t.Fatal(err)
	}
	if credentialState != "SENT" || callState != "PENDING" {
		t.Fatalf("global command order = credential %s, Call %s; want SENT/PENDING",
			credentialState, callState)
	}
}

func TestCredentialCommandExecutionExpiresInFailedQuarantine(t *testing.T) {
	for _, action := range []humancalling.CommandAction{
		humancalling.CommandCreateCredential,
		humancalling.CommandDisableCredential,
	} {
		t.Run(string(action), func(t *testing.T) {
			pool := testdb.Open(t)
			now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
			accessModule := access.New(pool, func() time.Time { return now })
			authorization, staff := provisionConcurrentStaff(
				t, accessModule, now, "expired-execution-"+strings.ToLower(string(action)), 1,
			)
			provider := &credentialFailureProvider{executeErr: errors.New("synthetic provider timeout")}
			calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
				CredentialConnectionID: "staff-credential-connection",
			}, func() time.Time { return now })
			if err := calling.ReconcileCredentials(context.Background()); err != nil {
				t.Fatal(err)
			}
			if action == humancalling.CommandDisableCredential {
				if _, err := pool.Exec(context.Background(), `
					UPDATE access_memberships SET revoked_at = $2
					WHERE id = $1
				`, authorization.Membership.ID, now); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(context.Background(), `
					UPDATE human_calling_credentials
					SET state = 'DISABLING', provider_credential_id = 'expired-provider-credential',
						provider_sip_username = 'expired-sip-user'
					WHERE user_subject = $1
				`, staff[0].Subject); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(context.Background(), `
					UPDATE human_calling_provider_commands
					SET action = 'DISABLE_CREDENTIAL', target_id = 'expired-provider-credential',
						payload = '{}'::jsonb
					WHERE user_subject = $1
				`, staff[0].Subject); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pool.Exec(context.Background(), `
				UPDATE human_calling_provider_commands
				SET created_at = $2, updated_at = $2
				WHERE user_subject = $1
			`, staff[0].Subject, now.Add(-6*time.Minute)); err != nil {
				t.Fatal(err)
			}

			processed, _ := calling.ProcessNextCommand(context.Background())
			if !processed {
				t.Fatal("expired credential execution did not claim the command")
			}
			assertCredentialQuarantined(t, pool, staff[0].Subject)
			if err := calling.ReconcileCredentials(context.Background()); err != nil {
				t.Fatal(err)
			}
			var activeDisableCommands int
			if err := pool.QueryRow(context.Background(), `
				SELECT count(*) FROM human_calling_provider_commands
				WHERE user_subject = $1 AND action = 'DISABLE_CREDENTIAL'
					AND target_id = 'expired-provider-credential'
					AND state IN ('PENDING', 'SENDING', 'AMBIGUOUS')
			`, staff[0].Subject).Scan(&activeDisableCommands); err != nil {
				t.Fatal(err)
			}
			wantActiveDisableCommands := 0
			if action == humancalling.CommandDisableCredential {
				wantActiveDisableCommands = 1
			}
			if activeDisableCommands != wantActiveDisableCommands {
				t.Fatalf("active disable cleanup commands = %d, want %d",
					activeDisableCommands, wantActiveDisableCommands)
			}
		})
	}
}

func TestCredentialReconciliationExpiresWithoutProviderLookup(t *testing.T) {
	for _, action := range []humancalling.CommandAction{
		humancalling.CommandCreateCredential,
		humancalling.CommandDisableCredential,
	} {
		t.Run(string(action), func(t *testing.T) {
			pool := testdb.Open(t)
			now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
			accessModule := access.New(pool, func() time.Time { return now })
			_, staff := provisionConcurrentStaff(
				t, accessModule, now, "expired-reconciliation-"+strings.ToLower(string(action)), 1,
			)
			provider := &credentialFailureProvider{lookupErr: errors.New("synthetic provider timeout")}
			calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
				CredentialConnectionID: "staff-credential-connection",
			}, func() time.Time { return now })
			if err := calling.ReconcileCredentials(context.Background()); err != nil {
				t.Fatal(err)
			}
			credentialState := "PENDING"
			providerID := ""
			providerUsername := ""
			if action == humancalling.CommandDisableCredential {
				credentialState = "DISABLING"
				providerID = "expired-provider-credential"
				providerUsername = "expired-sip-user"
			}
			if _, err := pool.Exec(context.Background(), `
				UPDATE human_calling_credentials
				SET state = $2, provider_credential_id = NULLIF($3, ''),
					provider_sip_username = NULLIF($4, '')
				WHERE user_subject = $1
			`, staff[0].Subject, credentialState, providerID, providerUsername); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(context.Background(), `
				UPDATE human_calling_provider_commands
				SET action = $2, target_id = NULLIF($3, ''), state = 'AMBIGUOUS',
					created_at = $4, updated_at = $4, next_attempt_at = $5
				WHERE user_subject = $1
			`, staff[0].Subject, action, providerID,
				now.Add(-6*time.Minute), now); err != nil {
				t.Fatal(err)
			}

			processed, err := calling.ProcessNextCredentialReconciliation(context.Background())
			if !processed || err != nil {
				t.Fatalf("expired credential reconciliation = processed:%t err:%v", processed, err)
			}
			if provider.lookupCalls != 0 {
				t.Fatalf("expired credential reconciliation made %d provider lookups", provider.lookupCalls)
			}
			assertCredentialQuarantined(t, pool, staff[0].Subject)
			if err := calling.ReconcileCredentials(context.Background()); err != nil {
				t.Fatal(err)
			}
			var activeCommands int
			if err := pool.QueryRow(context.Background(), `
				SELECT count(*) FROM human_calling_provider_commands
				WHERE user_subject = $1 AND state IN ('PENDING', 'SENDING', 'AMBIGUOUS')
			`, staff[0].Subject).Scan(&activeCommands); err != nil {
				t.Fatal(err)
			}
			if activeCommands != 0 {
				t.Fatalf("credential quarantine created %d active commands", activeCommands)
			}
		})
	}
}

func TestCredentialReconciliationRetriesLookupFailureBeforeDeadline(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, staff := provisionConcurrentStaff(
		t, accessModule, now, "credential-lookup-retry", 1,
	)
	providerErr := errors.New("synthetic provider timeout")
	provider := &credentialFailureProvider{lookupErr: providerErr}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		CredentialConnectionID: "staff-credential-connection",
	}, func() time.Time { return now })
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET state = 'AMBIGUOUS', created_at = $2, updated_at = $2,
			next_attempt_at = $3
		WHERE user_subject = $1
	`, staff[0].Subject, now.Add(-time.Minute), now); err != nil {
		t.Fatal(err)
	}

	processed, err := calling.ProcessNextCredentialReconciliation(context.Background())
	if !processed || !errors.Is(err, providerErr) {
		t.Fatalf("credential lookup retry = processed:%t err:%v", processed, err)
	}
	var state, code string
	var nextAttemptAt time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT state, COALESCE(last_error_code, ''), next_attempt_at
		FROM human_calling_provider_commands
		WHERE user_subject = $1
	`, staff[0].Subject).Scan(&state, &code, &nextAttemptAt); err != nil {
		t.Fatal(err)
	}
	if provider.lookupCalls != 1 || state != "AMBIGUOUS" ||
		code != "PROVIDER_STATE_UNAVAILABLE" || !nextAttemptAt.Equal(now.Add(5*time.Second)) {
		t.Fatalf("credential lookup retry = calls:%d state:%s/%s next:%s",
			provider.lookupCalls, state, code, nextAttemptAt)
	}
}

func assertCredentialQuarantined(t *testing.T, pool *pgxpool.Pool, subject string) {
	t.Helper()
	var commandState, commandCode, credentialState, credentialCode string
	if err := pool.QueryRow(context.Background(), `
		SELECT command.state, COALESCE(command.last_error_code, ''),
			credential.state, COALESCE(credential.last_error_code, '')
		FROM human_calling_provider_commands command
		JOIN human_calling_credentials credential
			ON credential.user_subject = command.user_subject
		WHERE command.user_subject = $1
		ORDER BY command.created_at, command.id
		LIMIT 1
	`, subject).Scan(
		&commandState, &commandCode, &credentialState, &credentialCode,
	); err != nil {
		t.Fatal(err)
	}
	if commandState != "FAILED" || commandCode != "CREDENTIAL_RETRY_EXHAUSTED" ||
		credentialState != "FAILED" || credentialCode != "CREDENTIAL_RETRY_EXHAUSTED" {
		t.Fatalf("credential quarantine = command:%s/%s credential:%s/%s",
			commandState, commandCode, credentialState, credentialCode)
	}
}

func prepareTerminalStaffHangup(
	t *testing.T,
	providerActive bool,
	observers ...observability.Observer,
) (*pgxpool.Pool, *humancalling.Module, *recordingProvider, access.Identity, string, string, string) {
	t.Helper()
	now := time.Date(2026, time.August, 10, 14, 0, 0, 0, time.UTC)
	provider := &recordingProvider{observations: []humancalling.ProviderCallObservation{{
		Active:        providerActive,
		CallControlID: "terminal-staff-control",
		CallLegID:     "terminal-staff-provider-leg",
		CallSessionID: "terminal-staff-session",
	}}}
	pool, calling, _, staff := prepareInboundFanout(
		t, now, "terminal-staff-cleanup", provider, 1, observers...,
	)
	var callID, legID, commandID string
	if err := pool.QueryRow(context.Background(), `
		SELECT call.id::text, leg.id::text
		FROM human_calling_calls call
		JOIN human_calling_call_legs leg ON leg.call_id = call.id
		WHERE leg.role = 'STAFF'
	`).Scan(&callID, &legID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_calls
		SET terminal_outcome = 'ENDED', ended_at = $2::timestamptz,
			disposition_deadline = $2::timestamptz + interval '30 seconds',
			updated_at = $2::timestamptz
		WHERE id = $1
	`, callID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs
		SET state = 'ENDED',
			ending_at = COALESCE(answered_at, $2::timestamptz),
			ended_at = COALESCE(answered_at, $2::timestamptz),
			updated_at = $2::timestamptz
		WHERE call_id = $1 AND id <> $3
	`, callID, now, legID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs
		SET state = 'ENDING', provider_connection_id = 'staff-call-control-connection',
			provider_call_control_id = 'terminal-staff-control',
			provider_call_leg_id = 'terminal-staff-provider-leg',
			provider_call_session_id = 'terminal-staff-session',
			answered_at = $2::timestamptz - interval '2 minutes',
			ending_at = $2::timestamptz - interval '2 minutes',
			updated_at = $2::timestamptz - interval '2 minutes'
		WHERE id = $1
	`, legID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET state = 'RECONCILED', updated_at = $2
		WHERE call_id = $1
	`, callID, now); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO human_calling_provider_commands (
			call_id, call_leg_id, user_subject, action, target_id, payload,
			state, sent_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, 'HANGUP_LEG', 'terminal-staff-control', '{}',
			'SENT', $4::timestamptz - interval '2 minutes',
			$4::timestamptz - interval '2 minutes',
			$4::timestamptz - interval '2 minutes'
		)
		RETURNING id::text
	`, callID, legID, staff[0].Subject, now).Scan(&commandID); err != nil {
		t.Fatal(err)
	}
	return pool, calling, provider, staff[0], callID, legID, commandID
}

func TestOutboundCallUsesPracticeVoiceFallbackForLocationWithoutNumber(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 10, 15, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	if _, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test", RequestedBy: "outbound-fallback-test",
		Practices: []access.PracticeProvision{{
			Key: "outbound-fallback-practice", Name: "Outbound Fallback Practice",
			Locations: []access.LocationProvision{
				{Key: "optical", Name: "Optical"},
				{Key: "sweetwater", Name: "Sweetwater"},
			},
			AccessGrants: []access.AccessGrantProvision{{
				Key: "outbound-fallback-staff", Email: "staff@outbound-fallback.test",
				Role: access.RoleStaff, LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatalf("provision outbound fallback access: %v", err)
	}
	identity := access.Identity{
		Subject: "outbound-fallback-staff", Email: "staff@outbound-fallback.test",
		EmailVerified: true,
	}
	authorization := testaccess.Activate(t, accessModule, identity)
	var opticalLocationID string
	for _, location := range authorization.Locations {
		if location.Name == "Optical" {
			opticalLocationID = location.ID
		}
	}
	if opticalLocationID == "" {
		t.Fatal("Optical Location was not provisioned")
	}
	provider := &recordingProvider{}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		StaffSIPDomain:         "sip.telnyx.com",
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	readyConcurrentStaff(t, calling, []access.Identity{identity}, "outbound-fallback-browser")
	if err := calling.ProvisionLocationVoices(context.Background(),
		[]humancalling.LocationVoiceProvision{{
			PracticeKey: "outbound-fallback-practice",
			LocationKey: "sweetwater",
			Number:      "+17864654836",
			Enabled:     true,
		}}); err != nil {
		t.Fatalf("provision Sweetwater caller ID: %v", err)
	}
	if err := calling.ProvisionOutboundVoiceFallbacks(context.Background(),
		[]humancalling.OutboundVoiceFallbackProvision{{
			PracticeKey: "outbound-fallback-practice",
			LocationKey: "sweetwater",
		}}); err != nil {
		t.Fatalf("provision Sweetwater outbound fallback: %v", err)
	}

	call, err := calling.StartOutboundCall(context.Background(),
		humancalling.StartOutboundCallCommand{
			Identity: identity, SessionID: "outbound-fallback-browser-1",
			IdempotencyKey: "outbound-fallback-call",
			PracticeID:     authorization.Practice.ID,
			LocationID:     opticalLocationID,
			Destination:    "+15555550123",
		})
	if err != nil {
		t.Fatalf("start fallback outbound Call: %v", err)
	}
	if call.LocationID != opticalLocationID || call.LocationName != "Optical" {
		t.Fatalf("fallback Call Location = %q/%q, want Optical", call.LocationID, call.LocationName)
	}
	if call.CallerID != "+17864654836" {
		t.Fatalf("fallback Call caller ID = %q, want Sweetwater number", call.CallerID)
	}
}

func TestUnconfirmedOutboundMediaExpiresAndReleasesSoftphone(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 10, 13, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, staff := provisionConcurrentStaff(
		t, accessModule, now, "outbound-media-timeout", 1,
	)
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: "outbound-media-timeout-control",
		CallLegID:     "outbound-media-timeout-provider-leg",
	}}}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		StaffSIPDomain:         "sip.telnyx.com",
		RingWindowDuration:     20 * time.Second,
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	readyConcurrentStaff(t, calling, staff, "outbound-media-timeout-browser")
	if err := calling.ProvisionLocationVoices(context.Background(),
		[]humancalling.LocationVoiceProvision{{
			PracticeKey: "outbound-media-timeout-practice",
			LocationKey: "outbound-media-timeout-location",
			Number:      "+14843336938", Enabled: true,
		}}); err != nil {
		t.Fatalf("provision outbound caller ID: %v", err)
	}

	call, err := calling.StartOutboundCall(context.Background(),
		humancalling.StartOutboundCallCommand{
			Identity: staff[0], SessionID: "outbound-media-timeout-browser-1",
			IdempotencyKey: "outbound-media-timeout",
			PracticeID:     authorization.Practice.ID,
			LocationID:     authorization.Locations[0].ID,
			Destination:    "+15555550123",
		})
	if err != nil {
		t.Fatalf("start outbound Call: %v", err)
	}
	processAllCommands(t, calling)
	dial := provider.last(humancalling.CommandDialOutboundStaff)
	clientState, _ := dial.Payload["client_state"].(string)
	staffFact := humancalling.ProviderFact{
		EventID: "outbound-media-timeout-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: "outbound-media-timeout-control",
		CallLegID:     "outbound-media-timeout-provider-leg",
		CallSessionID: "outbound-media-timeout-session", ClientState: clientState,
	}
	if err := calling.ApplyProviderFact(context.Background(), staffFact); err != nil {
		t.Fatalf("project outbound Staff initiation: %v", err)
	}
	staffFact.EventID = "outbound-media-timeout-answered"
	staffFact.Type = humancalling.FactCallAnswered
	staffFact.OccurredAt = now.Add(2 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), staffFact); err != nil {
		t.Fatalf("project outbound Staff answer: %v", err)
	}

	now = now.Add(21 * time.Second)
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 0 {
		t.Fatalf("early outbound media expiry = %d, %v", reconciled, err)
	}
	now = now.Add(2 * time.Second)
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("expire unconfirmed outbound media = %d, %v", reconciled, err)
	}
	var outcome, termination, staffState, staffError, hangupTarget string
	var destinationLegs int
	if err := pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome, call.provider_termination,
			staff_leg.state, staff_leg.error_code,
			(SELECT count(*) FROM human_calling_call_legs destination
			 WHERE destination.call_id = call.id AND destination.role = 'DESTINATION'),
			hangup.target_id
		FROM human_calling_calls call
		JOIN human_calling_call_legs staff_leg
			ON staff_leg.call_id = call.id AND staff_leg.role = 'STAFF'
		JOIN human_calling_provider_commands hangup
			ON hangup.call_leg_id = staff_leg.id AND hangup.action = 'HANGUP_LEG'
		WHERE call.id = $1
	`, call.ID).Scan(&outcome, &termination, &staffState, &staffError,
		&destinationLegs, &hangupTarget); err != nil {
		t.Fatal(err)
	}
	if outcome != "UNANSWERED" || termination != "MEDIA_READINESS_FAILED" ||
		staffState != "FAILED" || staffError != "MEDIA_READINESS_FAILED" ||
		destinationLegs != 0 || hangupTarget != "outbound-media-timeout-control" {
		t.Fatalf("expired media outcome=%s termination=%s staff=%s error=%s destinations=%d hangup=%s",
			outcome, termination, staffState, staffError, destinationLegs, hangupTarget)
	}
	readiness, err := calling.SetReadiness(context.Background(), humancalling.ReadinessCommand{
		Identity: staff[0], SessionID: "outbound-media-timeout-browser-1",
		Registered: true, MicrophoneReady: true, AudioReady: true,
		SessionHealthy: true, Available: true,
	})
	if err != nil || !readiness.Available || readiness.ActiveCallID != "" {
		t.Fatalf("released outbound softphone = %#v, err = %v", readiness, err)
	}
	processAllCommands(t, calling)
	if provider.count(humancalling.CommandHangupLeg) != 1 {
		t.Fatalf("expired outbound media Hangups = %#v", provider.commands)
	}
}

func TestCallerAbandonmentCancelsUnsentFanout(t *testing.T) {
	now := time.Date(2026, time.August, 5, 14, 0, 0, 0, time.UTC)
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: "abandon-staff-control", CallLegID: "abandon-staff-leg",
	}}}
	pool, calling, caller, _ := prepareInboundFanout(
		t, now, "call-leg-abandon", provider, 1,
	)
	caller.EventID = "abandon-caller-hangup"
	caller.Type = humancalling.FactCallHangup
	caller.OccurredAt = now.Add(2 * time.Second)
	caller.HangupCause = "NORMAL_CLEARING"
	caller.TerminationSource = "CALLER"
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatalf("apply caller abandonment: %v", err)
	}
	processAllCommands(t, calling)

	var cancelled, failedLegs int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_provider_commands
		WHERE action IN ('START_RING_WINDOW', 'DIAL_STAFF') AND state = 'FAILED'
	`).Scan(&cancelled); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_call_legs
		WHERE role = 'STAFF' AND state = 'FAILED'
	`).Scan(&failedLegs); err != nil {
		t.Fatal(err)
	}
	if cancelled != 2 || failedLegs != 1 ||
		provider.count(humancalling.CommandStartRingWindow) != 0 ||
		provider.count(humancalling.CommandDialStaff) != 0 ||
		provider.count(humancalling.CommandSpeakVoicemail) != 0 {
		t.Fatalf("abandonment cleanup = cancelled:%d failed:%d commands:%#v",
			cancelled, failedLegs, provider.commands)
	}
}

func TestCallerHangupBeforeAnswerDoesNotReviveFanout(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 5, 14, 5, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, staff := provisionConcurrentStaff(
		t, accessModule, now, "hangup-before-answer", 1,
	)
	provider := &recordingProvider{}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain:       "synthetic.sip.telnyx.com",
		StaffSIPDomain:         "sip.telnyx.com",
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
		FromNumber:             "+14843336938",
		RingbackURL:            "https://media.synthetic.test/ringback.wav",
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	readyConcurrentStaff(t, calling, staff, "hangup-before-answer-browser")
	_, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject: "abita-hangup-before-answer", PracticeID: authorization.Practice.ID,
		},
		LocationID:   authorization.Locations[0].ID,
		SourceCallID: "hangup-before-answer-source", IdempotencyKey: "hangup-before-answer",
		Contact: humancalling.ContactContext{Phone: "+15555550100"},
	})
	if err != nil {
		t.Fatal(err)
	}
	caller := humancalling.ProviderFact{
		EventID: "hangup-before-answer-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now, ConnectionID: "staff-call-control-connection",
		CallControlID: "hangup-before-answer-control", CallLegID: "hangup-before-answer-leg",
		CallSessionID: "hangup-before-answer-session", From: "+15555550100",
		To: "+14843989071",
	}
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	answer := provider.last(humancalling.CommandAnswerCaller)
	answerClientState, _ := answer.Payload["client_state"].(string)
	if answerClientState == "" {
		t.Fatalf("caller Answer client state = %#v", answer.Payload)
	}
	caller.EventID = "hangup-before-answer-hangup"
	caller.Type = humancalling.FactCallHangup
	caller.OccurredAt = now.Add(2 * time.Second)
	caller.HangupCause = "NORMAL_CLEARING"
	caller.TerminationSource = "CALLER"
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatal(err)
	}
	caller.EventID = "hangup-before-answer-late-answer"
	caller.Type = humancalling.FactCallAnswered
	caller.OccurredAt = now.Add(time.Second)
	caller.ClientState = answerClientState
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	var terminal, answerCommandState string
	var staffLegs, recoveryTasks int
	if err := pool.QueryRow(context.Background(), `
		SELECT terminal_outcome,
			(SELECT count(*) FROM human_calling_call_legs WHERE role = 'STAFF'),
			(SELECT count(*) FROM work_tasks),
			(SELECT state FROM human_calling_provider_commands
			 WHERE action = 'ANSWER_CALLER' LIMIT 1)
		FROM human_calling_calls
	`).Scan(&terminal, &staffLegs, &recoveryTasks, &answerCommandState); err != nil {
		t.Fatal(err)
	}
	if terminal != "ABANDONED" || staffLegs != 0 || recoveryTasks != 0 ||
		answerCommandState != "RECONCILED" ||
		provider.count(humancalling.CommandStartRingWindow) != 0 ||
		provider.count(humancalling.CommandDialStaff) != 0 {
		t.Fatalf("late caller answer convergence: terminal=%s legs=%d tasks=%d answer=%s commands=%#v",
			terminal, staffLegs, recoveryTasks, answerCommandState, provider.commands)
	}
}

func TestCallerHangupDuringVoicemailCreatesOneMissedCallRecovery(t *testing.T) {
	now := time.Date(2026, time.August, 9, 14, 10, 0, 0, time.UTC)
	provider := &recordingProvider{}
	pool, calling, caller, _ := prepareInboundFanout(
		t, now, "answered-caller-hangup", provider, 1,
	)
	processAllCommands(t, calling)
	ring := provider.last(humancalling.CommandStartRingWindow)
	ringState, _ := ring.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "answered-caller-hangup-ring-ended", Type: humancalling.FactPlaybackEnded,
		OccurredAt: now.Add(20 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: ringState, PlaybackStatus: "completed",
	}); err != nil {
		t.Fatalf("start voicemail before caller Hangup: %v", err)
	}
	processAllCommands(t, calling)
	caller.EventID = "answered-caller-hangup-ended"
	caller.Type = humancalling.FactCallHangup
	caller.OccurredAt = now.Add(21 * time.Second)
	caller.HangupCause = "NORMAL_CLEARING"
	caller.TerminationSource = "CALLER"
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatalf("project answered caller Hangup: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatalf("replay answered caller Hangup: %v", err)
	}

	var terminal, voicemailOutcome, taskOrigin, taskTitle string
	var taskCount, interactionCount, activityCount, acknowledgementCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome, voicemail.outcome, task.origin, task.title,
			(SELECT count(*) FROM work_tasks),
			(SELECT count(*) FROM work_task_interactions),
			(SELECT count(*) FROM work_task_activities),
			(SELECT count(*) FROM work_task_acknowledgements)
		FROM human_calling_calls call
		JOIN human_calling_voicemails voicemail ON voicemail.call_id = call.id
		JOIN work_tasks task ON task.id = voicemail.task_id
	`).Scan(
		&terminal, &voicemailOutcome, &taskOrigin, &taskTitle,
		&taskCount, &interactionCount, &activityCount, &acknowledgementCount,
	); err != nil {
		t.Fatalf("read answered caller recovery: %v", err)
	}
	if terminal != "MISSED" || voicemailOutcome != "MISSED_CALL" ||
		taskOrigin != "MISSED_CALL_RECOVERY" || taskTitle != "Return missed call" ||
		taskCount != 1 || interactionCount != 1 || activityCount != 1 ||
		acknowledgementCount != 0 {
		t.Fatalf(
			"answered caller recovery = terminal:%s outcome:%s origin:%s title:%s tasks:%d interactions:%d activities:%d acknowledgements:%d",
			terminal, voicemailOutcome, taskOrigin, taskTitle,
			taskCount, interactionCount, activityCount, acknowledgementCount,
		)
	}
}

func TestPeerBridgeEvidencePreventsAbandonmentAcrossEventOrder(t *testing.T) {
	for _, staffBridgeFirst := range []bool{true, false} {
		name := "caller-bridge-first"
		if staffBridgeFirst {
			name = "staff-bridge-first"
		}
		t.Run(name, func(t *testing.T) {
			now := time.Date(2026, time.August, 5, 14, 15, 0, 0, time.UTC)
			prefix := "peer-order-caller"
			if staffBridgeFirst {
				prefix = "peer-order-staff"
			}
			provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
				CallControlID: prefix + "-staff-control",
				CallLegID:     prefix + "-staff-leg",
			}}}
			pool, calling, caller, _ := prepareInboundFanout(t, now, prefix, provider, 1)
			processAllCommands(t, calling)
			dial := provider.last(humancalling.CommandDialStaff)
			staffState, _ := dial.Payload["client_state"].(string)
			staff := humancalling.ProviderFact{
				EventID: prefix + "-staff-initiated", Type: humancalling.FactCallInitiated,
				OccurredAt: now.Add(2 * time.Second), ConnectionID: "staff-call-control-connection",
				CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-leg",
				CallSessionID: prefix + "-staff-session", ClientState: staffState,
			}
			if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
				t.Fatal(err)
			}
			staff.EventID = prefix + "-staff-answered"
			staff.Type = humancalling.FactCallAnswered
			staff.OccurredAt = now.Add(3 * time.Second)
			if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
				t.Fatal(err)
			}
			processAllCommands(t, calling)
			if staffBridgeFirst {
				if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
					EventID: prefix + "-staff-bridged", Type: humancalling.FactCallBridged,
					OccurredAt: now.Add(4 * time.Second), CallControlID: staff.CallControlID,
					CallLegID: staff.CallLegID, CallSessionID: staff.CallSessionID,
				}); err != nil {
					t.Fatal(err)
				}
				caller.EventID = prefix + "-caller-hangup"
				caller.Type = humancalling.FactCallHangup
				caller.OccurredAt = now.Add(5 * time.Second)
				caller.HangupCause = "NORMAL_CLEARING"
				caller.TerminationSource = "CALLER"
				if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
					EventID: prefix + "-caller-bridged", Type: humancalling.FactCallBridged,
					OccurredAt: now.Add(4 * time.Second), CallControlID: caller.CallControlID,
					CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
				}); err != nil {
					t.Fatal(err)
				}
				staff.EventID = prefix + "-staff-hangup"
				staff.Type = humancalling.FactCallHangup
				staff.OccurredAt = now.Add(5 * time.Second)
				staff.HangupCause = "NORMAL_CLEARING"
				staff.TerminationSource = "STAFF"
				if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
					t.Fatal(err)
				}
			}
			var terminal string
			if err := pool.QueryRow(context.Background(), `
				SELECT terminal_outcome FROM human_calling_calls
			`).Scan(&terminal); err != nil {
				t.Fatal(err)
			}
			if terminal != "ENDED" {
				t.Fatalf("peer-first Bridge terminal outcome = %s", terminal)
			}
		})
	}
}

func TestBridgeBeforeHangupUpgradesWhenDeliveredAfterHangup(t *testing.T) {
	now := time.Date(2026, time.August, 5, 14, 35, 0, 0, time.UTC)
	prefix := "bridge-delivered-after-hangup"
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-leg",
	}}}
	pool, calling, caller, _ := prepareInboundFanout(t, now, prefix, provider, 1)
	processAllCommands(t, calling)
	dial := provider.last(humancalling.CommandDialStaff)
	staffState, _ := dial.Payload["client_state"].(string)
	staff := humancalling.ProviderFact{
		EventID: prefix + "-staff-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(2 * time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-leg",
		CallSessionID: prefix + "-staff-session", ClientState: staffState,
	}
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	staff.EventID = prefix + "-staff-answered"
	staff.Type = humancalling.FactCallAnswered
	staff.OccurredAt = now.Add(3 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	bridge := provider.last(humancalling.CommandBridge)
	bridgeState, _ := bridge.Payload["client_state"].(string)
	caller.EventID = prefix + "-caller-hangup-delivered-first"
	caller.Type = humancalling.FactCallHangup
	caller.OccurredAt = now.Add(5 * time.Second)
	caller.HangupCause = "NORMAL_CLEARING"
	caller.TerminationSource = "CALLER"
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatal(err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: prefix + "-staff-bridge-delivered-late", Type: humancalling.FactCallBridged,
		OccurredAt: now.Add(4 * time.Second), CallControlID: staff.CallControlID,
		CallLegID: staff.CallLegID, CallSessionID: staff.CallSessionID,
		ClientState: bridgeState,
	}); err != nil {
		t.Fatal(err)
	}
	var terminal string
	var bridgedAt, dispositionDeadline *time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome, leg.bridged_at, call.disposition_deadline
		FROM human_calling_calls call
		JOIN human_calling_call_legs leg ON leg.call_id = call.id AND leg.role = 'STAFF'
	`).Scan(&terminal, &bridgedAt, &dispositionDeadline); err != nil {
		t.Fatal(err)
	}
	if terminal != "ENDED" || bridgedAt == nil || dispositionDeadline == nil {
		t.Fatalf("historical Bridge upgrade = terminal:%s bridged:%v disposition:%v",
			terminal, bridgedAt, dispositionDeadline)
	}
}

func TestUnresolvedBridgeFencesVoicemailUntilReconciled(t *testing.T) {
	now := time.Date(2026, time.August, 5, 14, 40, 0, 0, time.UTC)
	prefix := "bridge-voicemail-fence"
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-leg",
	}}}
	pool, calling, caller, _ := prepareInboundFanout(t, now, prefix, provider, 1)
	processAllCommands(t, calling)
	dial := provider.last(humancalling.CommandDialStaff)
	staffState, _ := dial.Payload["client_state"].(string)
	staff := humancalling.ProviderFact{
		EventID: prefix + "-staff-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(2 * time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-leg",
		CallSessionID: prefix + "-staff-session", ClientState: staffState,
	}
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	staff.EventID = prefix + "-staff-answered"
	staff.Type = humancalling.FactCallAnswered
	staff.OccurredAt = now.Add(3 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	bridge := provider.last(humancalling.CommandBridge)
	bridgeState, _ := bridge.Payload["client_state"].(string)
	ring := provider.last(humancalling.CommandStartRingWindow)
	ringState, _ := ring.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: prefix + "-ring-completed", Type: humancalling.FactPlaybackEnded,
		OccurredAt: now.Add(4 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: ringState, PlaybackStatus: "completed",
	}); err != nil {
		t.Fatal(err)
	}
	staff.EventID = prefix + "-staff-hangup-delivered-first"
	staff.Type = humancalling.FactCallHangup
	staff.OccurredAt = now.Add(6 * time.Second)
	staff.HangupCause = "NORMAL_CLEARING"
	staff.TerminationSource = "STAFF"
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	if provider.count(humancalling.CommandSpeakVoicemail) != 0 {
		t.Fatalf("unresolved Bridge allowed voicemail: %#v", provider.commands)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1
	`, bridge.CallLegID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands SET created_at = $2, updated_at = $2
		WHERE id = $1
	`, bridge.ID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.observations = append(provider.observations, humancalling.ProviderCallObservation{
		Events: []humancalling.ProviderFact{{
			EventID: prefix + "-staff-bridge-reconciled", Type: humancalling.FactCallBridged,
			OccurredAt: now.Add(5 * time.Second), CallControlID: staff.CallControlID,
			CallLegID: staff.CallLegID, CallSessionID: staff.CallSessionID,
			ClientState: bridgeState,
		}},
	})
	provider.mu.Unlock()
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("reconcile delayed Bridge = %d, %v", reconciled, err)
	}
	var terminal string
	if err := pool.QueryRow(context.Background(), `
		SELECT terminal_outcome FROM human_calling_calls
	`).Scan(&terminal); err != nil {
		t.Fatal(err)
	}
	if terminal != "ENDED" || provider.count(humancalling.CommandSpeakVoicemail) != 0 {
		t.Fatalf("reconciled Bridge outcome=%s commands=%#v", terminal, provider.commands)
	}
}

func TestAbsentBridgeReconciliationReleasesVoicemailFence(t *testing.T) {
	now := time.Date(2026, time.August, 5, 14, 42, 0, 0, time.UTC)
	prefix := "absent-bridge-voicemail"
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-leg",
	}}}
	pool, calling, caller, _ := prepareInboundFanout(t, now, prefix, provider, 1)
	processAllCommands(t, calling)
	dial := provider.last(humancalling.CommandDialStaff)
	staffState, _ := dial.Payload["client_state"].(string)
	staff := humancalling.ProviderFact{
		EventID: prefix + "-staff-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(2 * time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-leg",
		CallSessionID: prefix + "-staff-session", ClientState: staffState,
	}
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	staff.EventID = prefix + "-staff-answered"
	staff.Type = humancalling.FactCallAnswered
	staff.OccurredAt = now.Add(3 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	bridge := provider.last(humancalling.CommandBridge)
	ring := provider.last(humancalling.CommandStartRingWindow)
	ringState, _ := ring.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: prefix + "-ring-completed", Type: humancalling.FactPlaybackEnded,
		OccurredAt: now.Add(4 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: ringState, PlaybackStatus: "completed",
	}); err != nil {
		t.Fatal(err)
	}
	staff.EventID = prefix + "-staff-hangup"
	staff.Type = humancalling.FactCallHangup
	staff.OccurredAt = now.Add(5 * time.Second)
	staff.HangupCause = "NORMAL_CLEARING"
	staff.TerminationSource = "STAFF"
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	if provider.count(humancalling.CommandSpeakVoicemail) != 0 {
		t.Fatalf("unresolved absent Bridge allowed voicemail early: %#v", provider.commands)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1
	`, bridge.CallLegID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands SET created_at = $2, updated_at = $2
		WHERE id = $1
	`, bridge.ID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.observations = append(provider.observations, humancalling.ProviderCallObservation{})
	provider.mu.Unlock()
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("reconcile absent Bridge = %d, %v", reconciled, err)
	}
	processAllCommands(t, calling)
	var bridgeState string
	if err := pool.QueryRow(context.Background(), `
		SELECT state FROM human_calling_provider_commands WHERE id = $1
	`, bridge.ID).Scan(&bridgeState); err != nil {
		t.Fatal(err)
	}
	if bridgeState != "FAILED" || provider.count(humancalling.CommandSpeakVoicemail) != 1 {
		t.Fatalf("absent Bridge release = state:%s commands:%#v", bridgeState, provider.commands)
	}
}

func TestInboundAnswerAndOutboundStartShareOneSoftphoneLease(t *testing.T) {
	now := time.Date(2026, time.August, 5, 14, 45, 0, 0, time.UTC)
	prefix := "lease-answer-outbound"
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-leg",
	}}}
	pool, calling, _, staff := prepareInboundFanout(t, now, prefix, provider, 1)
	if err := calling.ProvisionLocationVoices(context.Background(),
		[]humancalling.LocationVoiceProvision{{
			PracticeKey: prefix + "-practice", LocationKey: prefix + "-location",
			Number: "+14843336938", Enabled: true,
		}}); err != nil {
		t.Fatal(err)
	}
	var practiceID, locationID string
	if err := pool.QueryRow(context.Background(), `
		SELECT practice.id::text, location.id::text
		FROM access_practices practice
		JOIN access_locations location ON location.practice_id = practice.id
		WHERE practice.provisioning_key = $1 AND location.provisioning_key = $2
	`, prefix+"-practice", prefix+"-location").Scan(&practiceID, &locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := calling.StartOutboundCall(context.Background(),
		humancalling.StartOutboundCallCommand{
			Identity: staff[0], SessionID: prefix + "-browser-1",
			IdempotencyKey: prefix + "-pending-outbound", PracticeID: practiceID,
			LocationID: locationID, Destination: "+15555550123",
		}); !errors.Is(err, humancalling.ErrOccupied) {
		t.Fatalf("outbound start with pending inbound Staff leg error = %v", err)
	}
	processAllCommands(t, calling)
	dial := provider.last(humancalling.CommandDialStaff)
	staffState, _ := dial.Payload["client_state"].(string)
	staffFact := humancalling.ProviderFact{
		EventID: prefix + "-staff-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(2 * time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: prefix + "-staff-control", CallLegID: prefix + "-staff-leg",
		CallSessionID: prefix + "-staff-session", ClientState: staffState,
	}
	if err := calling.ApplyProviderFact(context.Background(), staffFact); err != nil {
		t.Fatal(err)
	}
	staffFact.EventID = prefix + "-staff-answered"
	staffFact.Type = humancalling.FactCallAnswered
	answerResult := make(chan error, 1)
	outboundResult := make(chan error, 1)
	go func() {
		answerResult <- calling.ApplyProviderFact(context.Background(), staffFact)
	}()
	go func() {
		_, err := calling.StartOutboundCall(context.Background(),
			humancalling.StartOutboundCallCommand{
				Identity: staff[0], SessionID: prefix + "-browser-1",
				IdempotencyKey: prefix + "-outbound", PracticeID: practiceID,
				LocationID: locationID, Destination: "+15555550123",
			})
		outboundResult <- err
	}()
	if err := <-answerResult; err != nil {
		t.Fatalf("concurrent inbound answer: %v", err)
	}
	outboundErr := <-outboundResult
	if outboundErr != nil && !errors.Is(outboundErr, humancalling.ErrOccupied) {
		t.Fatalf("concurrent outbound start: %v", outboundErr)
	}
	var occupyingLegs, outboundCalls int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM human_calling_call_legs
			 WHERE role = 'STAFF' AND staff_subject = $1
				AND state IN ('PENDING', 'DIALING', 'RINGING', 'BRIDGE_PENDING', 'BRIDGED')),
			(SELECT count(*) FROM human_calling_calls WHERE direction = 'OUTBOUND')
	`, staff[0].Subject).Scan(&occupyingLegs, &outboundCalls); err != nil {
		t.Fatal(err)
	}
	if occupyingLegs != 1 || (outboundErr == nil) != (outboundCalls == 1) {
		t.Fatalf("lease race left %d occupying legs and %d outbound Calls; outbound err=%v",
			occupyingLegs, outboundCalls, outboundErr)
	}
}

func TestBridgeWinnerConvergesUncertainLosingDialAndHangup(t *testing.T) {
	now := time.Date(2026, time.August, 5, 14, 50, 0, 0, time.UTC)
	prefix := "uncertain-loser"
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{
		{CallControlID: prefix + "-winner-control", CallLegID: prefix + "-winner-leg"},
		{CallControlID: prefix + "-loser-control", CallLegID: prefix + "-loser-leg"},
	}}
	pool, calling, _, _ := prepareInboundFanout(t, now, prefix, provider, 2)
	processAllCommands(t, calling)
	dials := provider.all(humancalling.CommandDialStaff)
	winnerState, _ := dials[0].Payload["client_state"].(string)
	winner := humancalling.ProviderFact{
		EventID: prefix + "-winner-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now, ConnectionID: "staff-call-control-connection",
		CallControlID: prefix + "-winner-control", CallLegID: prefix + "-winner-leg",
		CallSessionID: prefix + "-winner-session", ClientState: winnerState,
	}
	if err := calling.ApplyProviderFact(context.Background(), winner); err != nil {
		t.Fatal(err)
	}
	winner.EventID = prefix + "-winner-answered"
	winner.Type = humancalling.FactCallAnswered
	if err := calling.ApplyProviderFact(context.Background(), winner); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	bridge := provider.last(humancalling.CommandBridge)
	bridgeState, _ := bridge.Payload["client_state"].(string)
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs
		SET state = 'DIALING', provider_call_control_id = NULL,
			provider_call_leg_id = NULL, provider_call_session_id = NULL
		WHERE id = $1
	`, dials[1].CallLegID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET state = 'AMBIGUOUS', last_error_code = 'SYNTHETIC_TIMEOUT'
		WHERE call_leg_id = $1 AND action = 'DIAL_STAFF'
	`, dials[1].CallLegID); err != nil {
		t.Fatal(err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: prefix + "-winner-bridged", Type: humancalling.FactCallBridged,
		OccurredAt: now.Add(time.Second), CallControlID: winner.CallControlID,
		CallLegID: winner.CallLegID, CallSessionID: winner.CallSessionID,
		ClientState: bridgeState,
	}); err != nil {
		t.Fatal(err)
	}
	var loserState, dialState string
	if err := pool.QueryRow(context.Background(), `
		SELECT leg.state, command.state
		FROM human_calling_call_legs leg
		JOIN human_calling_provider_commands command ON command.call_leg_id = leg.id
		WHERE leg.id = $1 AND command.action = 'DIAL_STAFF'
	`, dials[1].CallLegID).Scan(&loserState, &dialState); err != nil {
		t.Fatal(err)
	}
	if loserState != "ENDING" || dialState != "AMBIGUOUS" {
		t.Fatalf("uncertain loser after Bridge = leg:%s Dial:%s", loserState, dialState)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1
	`, dials[1].CallLegID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET created_at = $2, updated_at = $2
		WHERE call_leg_id = $1 AND action = 'DIAL_STAFF'
	`, dials[1].CallLegID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.observations = append(provider.observations, humancalling.ProviderCallObservation{
		Active: true, CallControlID: prefix + "-late-control",
		CallLegID: prefix + "-late-leg", CallSessionID: prefix + "-late-session",
	})
	provider.mu.Unlock()
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("reconcile uncertain losing Dial = %d, %v", reconciled, err)
	}
	var hangupID, hangupTarget string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text, target_id FROM human_calling_provider_commands
		WHERE call_leg_id = $1 AND action = 'HANGUP_LEG' AND state = 'PENDING'
	`, dials[1].CallLegID).Scan(&hangupID, &hangupTarget); err != nil {
		t.Fatal(err)
	}
	if hangupTarget != prefix+"-late-control" {
		t.Fatalf("reconciled loser Hangup target = %s", hangupTarget)
	}
	processAllCommands(t, calling)
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1
	`, dials[1].CallLegID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET created_at = $2, updated_at = $2
		WHERE id = $1
	`, hangupID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.observations = append(provider.observations, humancalling.ProviderCallObservation{
		Active: true, CallControlID: prefix + "-late-control",
		CallLegID: prefix + "-late-leg", CallSessionID: prefix + "-late-session",
	})
	provider.mu.Unlock()
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("reconcile accepted Hangup without webhook = %d, %v", reconciled, err)
	}
	var failedHangups, pendingHangups int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FILTER (WHERE state = 'FAILED'),
			count(*) FILTER (WHERE state = 'PENDING')
		FROM human_calling_provider_commands
		WHERE call_leg_id = $1 AND action = 'HANGUP_LEG'
	`, dials[1].CallLegID).Scan(&failedHangups, &pendingHangups); err != nil {
		t.Fatal(err)
	}
	if failedHangups != 1 || pendingHangups != 1 {
		t.Fatalf("active Hangup continuation = %d failed, %d pending",
			failedHangups, pendingHangups)
	}
}

func TestLosingInboundStaffHangupDoesNotTerminateConnectedCall(t *testing.T) {
	now := time.Date(2026, time.August, 17, 15, 13, 0, 0, time.UTC)
	prefix := "late-answer-loser-termination"
	dialResults := []humancalling.ProviderResult{
		{CallControlID: prefix + "-winner-control", CallLegID: prefix + "-winner-leg"},
		{CallControlID: prefix + "-loser-control", CallLegID: prefix + "-loser-leg"},
	}
	provider := &recordingProvider{dialResults: append(
		[]humancalling.ProviderResult(nil), dialResults...,
	)}
	pool, calling, _, _ := prepareInboundFanout(t, now, prefix, provider, 2)
	processAllCommands(t, calling)
	dials := provider.all(humancalling.CommandDialStaff)
	if len(dials) != 2 {
		t.Fatalf("staff Dials = %d, want 2", len(dials))
	}

	staffFacts := make([]humancalling.ProviderFact, 2)
	for index, dial := range dials {
		clientState, _ := dial.Payload["client_state"].(string)
		staffFacts[index] = humancalling.ProviderFact{
			EventID: prefix + fmt.Sprintf("-%d-initiated", index),
			Type:    humancalling.FactCallInitiated, OccurredAt: now.Add(time.Second),
			ConnectionID:  "staff-call-control-connection",
			CallControlID: dialResults[index].CallControlID,
			CallLegID:     dialResults[index].CallLegID,
			CallSessionID: prefix + fmt.Sprintf("-%d-session", index),
			ClientState:   clientState,
		}
		if err := calling.ApplyProviderFact(context.Background(), staffFacts[index]); err != nil {
			t.Fatalf("project Staff %d initiation: %v", index, err)
		}
		staffFacts[index].EventID = prefix + fmt.Sprintf("-%d-answered", index)
		staffFacts[index].Type = humancalling.FactCallAnswered
		staffFacts[index].OccurredAt = now.Add(time.Duration(2+index) * time.Second)
		if err := calling.ApplyProviderFact(context.Background(), staffFacts[index]); err != nil {
			t.Fatalf("project Staff %d answer: %v", index, err)
		}
	}

	processAllCommands(t, calling)
	bridge := provider.last(humancalling.CommandBridge)
	bridgeState, _ := bridge.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: prefix + "-winner-bridged", Type: humancalling.FactCallBridged,
		OccurredAt:    now.Add(4 * time.Second),
		CallControlID: staffFacts[0].CallControlID,
		CallLegID:     staffFacts[0].CallLegID,
		CallSessionID: staffFacts[0].CallSessionID,
		ClientState:   bridgeState,
	}); err != nil {
		t.Fatalf("project winning Bridge: %v", err)
	}
	processAllCommands(t, calling)

	loser := staffFacts[1]
	loser.EventID = prefix + "-loser-hangup"
	loser.Type = humancalling.FactCallHangup
	loser.OccurredAt = now.Add(5 * time.Second)
	loser.HangupCause = "NORMAL_CLEARING"
	loser.TerminationSource = "CALL_CONTROL"
	if err := calling.ApplyProviderFact(context.Background(), loser); err != nil {
		t.Fatalf("project losing Staff Hangup: %v", err)
	}

	var loserState string
	var providerTermination *string
	if err := pool.QueryRow(context.Background(), `
		SELECT loser.state, call.provider_termination
		FROM human_calling_calls call
		JOIN human_calling_call_legs loser ON loser.call_id = call.id
		WHERE loser.provider_call_leg_id = $1
	`, loser.CallLegID).Scan(&loserState, &providerTermination); err != nil {
		t.Fatal(err)
	}
	if loserState != "ENDED" || providerTermination != nil {
		t.Fatalf("losing Staff Hangup = state:%s provider termination:%v",
			loserState, providerTermination)
	}
}

func TestBridgeWinnerCommitsCleanupWhenSendingLosingDialReturns(t *testing.T) {
	now := time.Date(2026, time.August, 5, 14, 55, 0, 0, time.UTC)
	prefix := "sending-loser"
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{
		{CallControlID: prefix + "-winner-control", CallLegID: prefix + "-winner-leg"},
		{CallControlID: prefix + "-initial-loser-control", CallLegID: prefix + "-initial-loser-leg"},
	}}
	pool, calling, _, _ := prepareInboundFanout(t, now, prefix, provider, 2)
	processAllCommands(t, calling)
	dials := provider.all(humancalling.CommandDialStaff)
	winnerState, _ := dials[0].Payload["client_state"].(string)
	winner := humancalling.ProviderFact{
		EventID: prefix + "-winner-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now, ConnectionID: "staff-call-control-connection",
		CallControlID: prefix + "-winner-control", CallLegID: prefix + "-winner-leg",
		CallSessionID: prefix + "-winner-session", ClientState: winnerState,
	}
	if err := calling.ApplyProviderFact(context.Background(), winner); err != nil {
		t.Fatal(err)
	}
	winner.EventID = prefix + "-winner-answered"
	winner.Type = humancalling.FactCallAnswered
	if err := calling.ApplyProviderFact(context.Background(), winner); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	bridge := provider.last(humancalling.CommandBridge)
	bridgeState, _ := bridge.Payload["client_state"].(string)
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs
		SET state = 'PENDING', provider_call_control_id = NULL,
			provider_call_leg_id = NULL, provider_call_session_id = NULL
		WHERE id = $1
	`, dials[1].CallLegID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET state = 'PENDING', next_attempt_at = $2
		WHERE call_leg_id = $1 AND action = 'DIAL_STAFF'
	`, dials[1].CallLegID, now); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.dialResults = append(provider.dialResults, humancalling.ProviderResult{
		CallControlID: prefix + "-late-control", CallLegID: prefix + "-late-leg",
	})
	provider.blockAction = humancalling.CommandDialStaff
	provider.blockStarted = make(chan struct{})
	provider.blockRelease = make(chan struct{})
	started, release := provider.blockStarted, provider.blockRelease
	provider.mu.Unlock()
	processed := make(chan error, 1)
	go func() {
		ok, err := calling.ProcessNextCommand(context.Background())
		if !ok && err == nil {
			err = errors.New("no command processed")
		}
		processed <- err
	}()
	<-started
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: prefix + "-winner-bridged", Type: humancalling.FactCallBridged,
		OccurredAt: now.Add(time.Second), CallControlID: winner.CallControlID,
		CallLegID: winner.CallLegID, CallSessionID: winner.CallSessionID,
		ClientState: bridgeState,
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-processed; err != nil {
		t.Fatal(err)
	}
	var legState, controlID, hangupTarget string
	if err := pool.QueryRow(context.Background(), `
		SELECT leg.state, leg.provider_call_control_id, command.target_id
		FROM human_calling_call_legs leg
		JOIN human_calling_provider_commands command
			ON command.call_leg_id = leg.id AND command.action = 'HANGUP_LEG'
		WHERE leg.id = $1 AND command.state = 'PENDING'
	`, dials[1].CallLegID).Scan(&legState, &controlID, &hangupTarget); err != nil {
		t.Fatal(err)
	}
	if legState != "ENDING" || controlID != prefix+"-late-control" ||
		hangupTarget != prefix+"-late-control" {
		t.Fatalf("sending loser cleanup = state:%s control:%s target:%s",
			legState, controlID, hangupTarget)
	}
}

func TestAbsentHangupCompletionAndProviderFactUseConsistentLockOrder(t *testing.T) {
	now := time.Date(2026, time.August, 17, 19, 0, 0, 0, time.UTC)
	prefix := "absent-hangup-lock-order"
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: prefix + "-staff-control",
		CallLegID:     prefix + "-staff-leg",
	}}}
	pool, calling, caller, _ := prepareInboundFanout(t, now, prefix, provider, 1)
	processAllCommands(t, calling)
	dial := provider.last(humancalling.CommandDialStaff)
	clientState, _ := dial.Payload["client_state"].(string)

	caller.EventID = prefix + "-caller-hangup"
	caller.Type = humancalling.FactCallHangup
	caller.OccurredAt = now.Add(time.Second)
	caller.HangupCause = "normal_clearing"
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatalf("end abandoned caller: %v", err)
	}

	const barrierKey int64 = 817190001
	barrier := holdPostgresAdvisoryLock(t, pool, barrierKey)
	defer barrier.close()

	const triggerName = "test_block_absent_hangup_fact"
	const functionName = "test_wait_for_absent_hangup_fact"
	installPostgresTestTrigger(t, pool, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $function$
		BEGIN
			PERFORM pg_advisory_xact_lock(TG_ARGV[0]::bigint);
			RETURN NEW;
		END
		$function$;
		CREATE TRIGGER %s
		BEFORE UPDATE ON human_calling_call_legs
		FOR EACH ROW WHEN (NEW.id = '%s'::uuid)
		EXECUTE FUNCTION %s('%d')
	`, functionName, triggerName, dial.CallLegID, functionName, barrierKey), fmt.Sprintf(`
		DROP TRIGGER IF EXISTS %s ON human_calling_call_legs;
		DROP FUNCTION IF EXISTS %s()
	`, triggerName, functionName))

	provider.mu.Lock()
	provider.blockAction = humancalling.CommandHangupLeg
	provider.blockStarted = make(chan struct{})
	provider.blockRelease = make(chan struct{})
	provider.blockError = fmt.Errorf(
		"%w: synthetic absent Hangup target",
		humancalling.ErrProviderTargetAbsent,
	)
	started, release := provider.blockStarted, provider.blockRelease
	provider.mu.Unlock()

	commandResult := make(chan error, 1)
	go func() {
		processed, err := calling.ProcessNextCommand(context.Background())
		if !processed && err == nil {
			err = errors.New("no Hangup command processed")
		}
		commandResult <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Hangup provider execution did not start")
	}

	factResult := make(chan error, 1)
	go func() {
		factResult <- calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
			EventID:       prefix + "-staff-hangup",
			Type:          humancalling.FactCallHangup,
			OccurredAt:    now.Add(2 * time.Second),
			CallControlID: prefix + "-staff-control",
			CallLegID:     prefix + "-staff-leg",
			ClientState:   clientState,
			HangupCause:   "normal_clearing",
		})
	}()
	factPID := waitForPostgresLockWaiter(
		t, barrier.connection, "advisory", barrier.pid,
	)
	close(release)
	waitForPostgresLockWaiter(t, barrier.connection, "transactionid", factPID)
	barrier.release()

	select {
	case err := <-factResult:
		if err != nil {
			t.Fatalf("project concurrent provider Hangup: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider Hangup projection did not finish")
	}
	select {
	case err := <-commandResult:
		if err != nil {
			t.Fatalf("converge absent Hangup target: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("absent Hangup command did not finish")
	}

	var legState, commandState string
	if err := pool.QueryRow(context.Background(), `
		SELECT leg.state, command.state
		FROM human_calling_call_legs leg
		JOIN human_calling_provider_commands command ON command.call_leg_id = leg.id
		WHERE leg.id = $1 AND command.action = 'HANGUP_LEG'
	`, dial.CallLegID).Scan(&legState, &commandState); err != nil {
		t.Fatal(err)
	}
	if legState != "ENDED" || commandState != "RECONCILED" ||
		provider.count(humancalling.CommandHangupLeg) != 1 {
		t.Fatalf("absent Hangup convergence = leg:%s command:%s executions:%d",
			legState, commandState, provider.count(humancalling.CommandHangupLeg))
	}
}

func TestCommandFailureAndAbsentReconciliationUseConsistentLockOrder(t *testing.T) {
	now := time.Date(2026, time.August, 17, 19, 30, 0, 0, time.UTC)
	prefix := "command-reconciliation-lock-order"
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: prefix + "-staff-control",
		CallLegID:     prefix + "-staff-leg",
	}}}
	pool, calling, _, _ := prepareInboundFanout(t, now, prefix, provider, 1)

	var commandID, callLegID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text, call_leg_id::text
		FROM human_calling_provider_commands
		WHERE action = 'DIAL_STAFF' AND state = 'PENDING'
	`).Scan(&commandID, &callLegID); err != nil {
		t.Fatalf("read pending Staff Dial: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET state = 'RECONCILED', updated_at = $2
		WHERE state = 'PENDING' AND id <> $1
	`, commandID, now); err != nil {
		t.Fatalf("isolate pending Staff Dial: %v", err)
	}

	provider.mu.Lock()
	provider.blockAction = humancalling.CommandDialStaff
	provider.blockStarted = make(chan struct{})
	provider.blockRelease = make(chan struct{})
	provider.blockError = fmt.Errorf(
		"%w: synthetic rejected Staff Dial",
		humancalling.ErrDefinitiveProviderFailure,
	)
	provider.observations = append(
		provider.observations,
		humancalling.ProviderCallObservation{},
	)
	started, release := provider.blockStarted, provider.blockRelease
	provider.mu.Unlock()

	commandResult := make(chan error, 1)
	go func() {
		processed, err := calling.ProcessNextCommand(context.Background())
		if !processed && err == nil {
			err = errors.New("no Staff Dial command processed")
		}
		commandResult <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Staff Dial provider execution did not start")
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET created_at = $2, updated_at = $2
		WHERE id = $1 AND state = 'SENDING'
	`, commandID, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("age in-flight Staff Dial: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1
	`, callLegID, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("age in-flight Staff CallLeg: %v", err)
	}

	const barrierKey int64 = 817193001
	barrier := holdPostgresAdvisoryLock(t, pool, barrierKey)
	defer barrier.close()

	const triggerName = "test_block_absent_reconciliation"
	const functionName = "test_wait_for_absent_reconciliation"
	installPostgresTestTrigger(t, pool, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $function$
		BEGIN
			PERFORM pg_advisory_xact_lock(TG_ARGV[0]::bigint);
			RETURN NEW;
		END
		$function$;
		CREATE TRIGGER %s
		BEFORE UPDATE ON human_calling_provider_commands
		FOR EACH ROW WHEN (NEW.id = '%s'::uuid AND NEW.state = 'FAILED')
		EXECUTE FUNCTION %s('%d')
	`, functionName, triggerName, commandID, functionName, barrierKey), fmt.Sprintf(`
		DROP TRIGGER IF EXISTS %s ON human_calling_provider_commands;
		DROP FUNCTION IF EXISTS %s()
	`, triggerName, functionName))

	reconciliationResult := make(chan error, 1)
	go func() {
		reconciled, err := calling.ReconcileStaleCalls(context.Background())
		if reconciled != 1 && err == nil {
			err = fmt.Errorf("reconciled %d stale Calls, want 1", reconciled)
		}
		reconciliationResult <- err
	}()
	reconciliationPID := waitForPostgresLockWaiter(
		t, barrier.connection, "advisory", barrier.pid,
	)
	close(release)
	waitForPostgresLockWaiter(
		t, barrier.connection, "transactionid", reconciliationPID,
	)
	barrier.release()

	select {
	case err := <-reconciliationResult:
		if err != nil {
			t.Fatalf("reconcile absent concurrent Staff Dial: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("absent Staff Dial reconciliation did not finish")
	}
	select {
	case err := <-commandResult:
		if err != nil {
			t.Fatalf("finish concurrent rejected Staff Dial: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rejected Staff Dial command did not finish")
	}

	var legState, commandState string
	if err := pool.QueryRow(context.Background(), `
		SELECT leg.state, command.state
		FROM human_calling_call_legs leg
		JOIN human_calling_provider_commands command ON command.call_leg_id = leg.id
		WHERE command.id = $1
	`, commandID).Scan(&legState, &commandState); err != nil {
		t.Fatal(err)
	}
	if legState != "FAILED" || commandState != "FAILED" ||
		provider.count(humancalling.CommandDialStaff) != 1 {
		t.Fatalf("rejected Staff Dial convergence = leg:%s command:%s executions:%d",
			legState, commandState, provider.count(humancalling.CommandDialStaff))
	}
}

func TestClosedHandoffAdmissionFailsBeforeDatabaseMutation(t *testing.T) {
	calling := humancalling.New(nil, nil, nil, humancalling.Config{
		HandoffAdmissionClosed: true,
	}, nil)
	if _, err := calling.CreateHandoff(
		context.Background(), humancalling.CreateHandoffCommand{},
	); !errors.Is(err, humancalling.ErrHandoffAdmissionClosed) {
		t.Fatalf("closed handoff admission error = %v", err)
	}
}

func TestPostgresWaiterLookupIsScopedToItsBlocker(t *testing.T) {
	pool := testdb.Open(t)
	const firstKey int64 = 817190101
	const secondKey int64 = 817190102
	first := holdPostgresAdvisoryLock(t, pool, firstKey)
	defer first.close()
	second := holdPostgresAdvisoryLock(t, pool, secondKey)
	defer second.close()

	waiterResult := make(chan error, 1)
	go func() {
		waiter, err := pool.Acquire(context.Background())
		if err == nil {
			defer waiter.Release()
			_, err = waiter.Exec(context.Background(), `SELECT pg_advisory_lock($1)`, secondKey)
			if err == nil {
				_, err = waiter.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, secondKey)
			}
		}
		waiterResult <- err
	}()
	waitForPostgresLockWaiter(t, first.connection, "advisory", second.pid)

	if pid, found, err := findPostgresLockWaiter(
		context.Background(), first.connection, "advisory", first.pid,
	); err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatalf("unrelated advisory waiter %d attributed to blocker %d", pid, first.pid)
	}
	second.release()
	select {
	case err := <-waiterResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unrelated advisory waiter did not finish")
	}
}

func TestClosedHandoffAdmissionRejectsPreviouslyIssuedRefer(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 5, 14, 30, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(
		t, accessModule, now, "closed-refer-admission", 1,
	)
	config := humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:    "staff-call-control-connection",
	}
	open := humancalling.New(pool, accessModule, &recordingProvider{}, config, func() time.Time {
		return now
	})
	_, err := open.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject: "abita-closed-refer-admission", PracticeID: authorization.Practice.ID,
		},
		LocationID: authorization.Locations[0].ID, SourceCallID: "closed-refer-source",
		IdempotencyKey: "closed-refer-handoff",
		Contact: humancalling.ContactContext{
			Phone: "+15555550100", PhoneSource: "Abita",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	config.HandoffAdmissionClosed = true
	closed := humancalling.New(pool, accessModule, &recordingProvider{}, config, func() time.Time {
		return now
	})
	err = closed.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "closed-refer-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now, ConnectionID: config.CallControlID,
		CallControlID: "closed-refer-control", CallLegID: "closed-refer-leg",
		CallSessionID: "closed-refer-session", To: "+14843989071",
	})
	if !errors.Is(err, humancalling.ErrHandoffAdmissionClosed) {
		t.Fatalf("closed delayed REFER error = %v", err)
	}
	var calls, consumed int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM human_calling_calls),
			(SELECT count(*) FROM human_calling_handoffs WHERE consumed_at IS NOT NULL)
	`).Scan(&calls, &consumed); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || consumed != 0 {
		t.Fatalf("closed delayed REFER mutated calls=%d consumed=%d", calls, consumed)
	}
}

func TestBridgeFailureAfterRingCompletionStartsVoicemailOnce(t *testing.T) {
	now := time.Date(2026, time.August, 5, 15, 0, 0, 0, time.UTC)
	provider := &recordingProvider{
		dialResults: []humancalling.ProviderResult{{
			CallControlID: "failed-bridge-staff-control",
			CallLegID:     "failed-bridge-staff-leg",
		}},
		actionErrors: map[humancalling.CommandAction][]error{
			humancalling.CommandBridge: {
				fmt.Errorf("%w: synthetic rejected Bridge", humancalling.ErrDefinitiveProviderFailure),
			},
		},
	}
	_, calling, caller, _ := prepareInboundFanout(
		t, now, "call-leg-bridge-failure", provider, 1,
	)
	processAllCommands(t, calling)
	dial := provider.last(humancalling.CommandDialStaff)
	clientState, _ := dial.Payload["client_state"].(string)
	staff := humancalling.ProviderFact{
		EventID: "failed-bridge-staff-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt:    now,
		ConnectionID:  "staff-call-control-connection",
		CallControlID: "failed-bridge-staff-control",
		CallLegID:     "failed-bridge-staff-leg", CallSessionID: "failed-bridge-session",
		ClientState: clientState,
	}
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	staff.EventID = "failed-bridge-staff-answered"
	staff.Type = humancalling.FactCallAnswered
	staff.OccurredAt = now
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	ring := provider.last(humancalling.CommandStartRingWindow)
	ringState, _ := ring.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "failed-bridge-ring-completed", Type: humancalling.FactPlaybackEnded,
		OccurredAt: now, CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: ringState, PlaybackStatus: "completed",
	}); err != nil {
		t.Fatal(err)
	}
	processed, err := calling.ProcessNextCommand(context.Background())
	if !processed || err != nil {
		t.Fatalf("rejected Bridge processing = %t, %v", processed, err)
	}
	processAllCommands(t, calling)
	if provider.count(humancalling.CommandBridge) != 1 ||
		provider.count(humancalling.CommandSpeakVoicemail) != 1 {
		t.Fatalf("Bridge recovery commands = %#v", provider.commands)
	}
}

func TestPlaybackStartedReconcilesStartRingWindow(t *testing.T) {
	now := time.Date(2026, time.August, 5, 15, 20, 0, 0, time.UTC)
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: "started-staff-control",
		CallLegID:     "started-staff-leg",
	}}}
	pool, calling, caller, _ := prepareInboundFanout(
		t, now, "call-leg-playback-started", provider, 1,
	)
	processAllCommands(t, calling)
	ring := provider.last(humancalling.CommandStartRingWindow)
	ringState, _ := ring.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "ring-window-started", Type: humancalling.FactPlaybackStarted,
		OccurredAt: now.Add(2 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: ringState,
	}); err != nil {
		t.Fatal(err)
	}
	var commandState string
	var terminalOutcome *string
	if err := pool.QueryRow(context.Background(), `
		SELECT state FROM human_calling_provider_commands
		WHERE action = 'START_RING_WINDOW'
	`).Scan(&commandState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT terminal_outcome FROM human_calling_calls
	`).Scan(&terminalOutcome); err != nil {
		t.Fatal(err)
	}
	if commandState != "RECONCILED" || terminalOutcome != nil {
		t.Fatalf("started ring window = state:%s terminal:%v", commandState, terminalOutcome)
	}
}

func TestActiveCallMakesUnobservedSentStartRingWindowAmbiguousOnce(t *testing.T) {
	now := time.Date(2026, time.August, 5, 15, 22, 0, 0, time.UTC)
	var metrics bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimeWorker,
		"test-revision",
		slog.New(slog.NewJSONHandler(&metrics, nil)),
	)
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: "ambiguous-start-staff-control",
		CallLegID:     "ambiguous-start-staff-leg",
	}}}
	pool, calling, caller, _ := prepareInboundFanout(
		t, now, "call-leg-ambiguous-start", provider, 1, observer,
	)
	processAllCommands(t, calling)
	ring := provider.last(humancalling.CommandStartRingWindow)
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1
	`, ring.CallLegID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET created_at = $2, updated_at = $2
		WHERE id = $1
	`, ring.ID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.observations = append(provider.observations, humancalling.ProviderCallObservation{
		Active: true, CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
	})
	provider.mu.Unlock()
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("reconcile ambiguous Start ring window = %d, %v", reconciled, err)
	}
	var commandState string
	var terminalOutcome *string
	if err := pool.QueryRow(context.Background(), `
		SELECT state FROM human_calling_provider_commands WHERE id = $1
	`, ring.ID).Scan(&commandState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT terminal_outcome FROM human_calling_calls
	`).Scan(&terminalOutcome); err != nil {
		t.Fatal(err)
	}
	if commandState != "AMBIGUOUS" || terminalOutcome != nil {
		t.Fatalf("active absent playback = state:%s terminal:%v", commandState, terminalOutcome)
	}
	if count := strings.Count(metrics.String(), `"outcome":"ambiguous"`); count != 1 {
		t.Fatalf("ambiguous provider command metrics = %d, want 1: %s", count, metrics.String())
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1
	`, ring.CallLegID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.observations = append(provider.observations, humancalling.ProviderCallObservation{
		Active: true, CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
	})
	provider.mu.Unlock()
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("repeat ambiguous Start ring window reconciliation = %d, %v", reconciled, err)
	}
	if count := strings.Count(metrics.String(), `"outcome":"ambiguous"`); count != 1 {
		t.Fatalf("repeat reconciliation emitted %d ambiguous metrics, want 1", count)
	}
	ringState, _ := ring.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "ambiguous-ring-window-started", Type: humancalling.FactPlaybackStarted,
		OccurredAt: now.Add(time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: ringState,
	}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT state FROM human_calling_provider_commands WHERE id = $1
	`, ring.ID).Scan(&commandState); err != nil {
		t.Fatal(err)
	}
	if commandState != "RECONCILED" {
		t.Fatalf("late positive playback state = %s", commandState)
	}
}

func TestActiveStopRingWindowAbsentTargetRecordsDegradedAudioUntilPlaybackEnds(t *testing.T) {
	now := time.Date(2026, time.August, 5, 15, 25, 0, 0, time.UTC)
	provider := &recordingProvider{
		dialResults: []humancalling.ProviderResult{{
			CallControlID: "stop-failure-staff-control",
			CallLegID:     "stop-failure-staff-leg",
		}},
		actionErrors: map[humancalling.CommandAction][]error{
			humancalling.CommandStopRingWindow: {
				fmt.Errorf("%w: synthetic active Stop target absent",
					humancalling.ErrProviderTargetAbsent),
			},
		},
	}
	pool, calling, caller, _ := prepareInboundFanout(
		t, now, "call-leg-stop-failure", provider, 1,
	)
	processAllCommands(t, calling)
	dial := provider.last(humancalling.CommandDialStaff)
	staffState, _ := dial.Payload["client_state"].(string)
	staff := humancalling.ProviderFact{
		EventID: "stop-failure-staff-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(2 * time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: "stop-failure-staff-control",
		CallLegID:     "stop-failure-staff-leg", CallSessionID: "stop-failure-session",
		ClientState: staffState,
	}
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	staff.EventID = "stop-failure-staff-answered"
	staff.Type = humancalling.FactCallAnswered
	staff.OccurredAt = now.Add(3 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	processed, err := calling.ProcessNextCommand(context.Background())
	if !processed || err != nil {
		t.Fatalf("Bridge processing = %t, %v", processed, err)
	}
	bridge := provider.last(humancalling.CommandBridge)
	bridgeState, _ := bridge.Payload["client_state"].(string)
	staff.EventID = "stop-failure-staff-bridged"
	staff.Type = humancalling.FactCallBridged
	staff.OccurredAt = now.Add(4 * time.Second)
	staff.ClientState = bridgeState
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	processed, err = calling.ProcessNextCommand(context.Background())
	if !processed || err != nil {
		t.Fatalf("Stop processing = %t, %v", processed, err)
	}
	var degraded int
	var stopState string
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_timeline
		WHERE kind = 'caller_audio.degraded'
	`).Scan(&degraded); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT state FROM human_calling_provider_commands
		WHERE action = 'STOP_RING_WINDOW'
	`).Scan(&stopState); err != nil {
		t.Fatal(err)
	}
	if degraded != 1 || stopState != "FAILED" ||
		provider.count(humancalling.CommandStopRingWindow) != 1 {
		t.Fatalf("degraded caller audio = timeline:%d state:%s effects:%d",
			degraded, stopState,
			provider.count(humancalling.CommandStopRingWindow))
	}
	ring := provider.last(humancalling.CommandStartRingWindow)
	ringState, _ := ring.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "stop-failure-playback-ended", Type: humancalling.FactPlaybackEnded,
		OccurredAt: now.Add(5 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: ringState, PlaybackStatus: "cancelled",
	}); err != nil {
		t.Fatal(err)
	}
	var converged int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_timeline
		WHERE kind = 'caller_audio.converged'
	`).Scan(&converged); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT state FROM human_calling_provider_commands
		WHERE action = 'STOP_RING_WINDOW'
	`).Scan(&stopState); err != nil {
		t.Fatal(err)
	}
	if converged != 1 || stopState != "RECONCILED" ||
		provider.count(humancalling.CommandStopRingWindow) != 1 {
		t.Fatalf("converged caller audio = timeline:%d state:%s effects:%d",
			converged, stopState,
			provider.count(humancalling.CommandStopRingWindow))
	}
}

func TestActiveUnobservedStopRingWindowConvergesWhenCallEnds(t *testing.T) {
	now := time.Date(2026, time.August, 18, 21, 0, 0, 0, time.UTC)
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: "unobserved-stop-staff-control",
		CallLegID:     "unobserved-stop-staff-leg",
	}}}
	pool, calling, caller, _ := prepareInboundFanout(
		t, now, "active-unobserved-stop", provider, 1,
	)
	processAllCommands(t, calling)
	dial := provider.last(humancalling.CommandDialStaff)
	staffState, _ := dial.Payload["client_state"].(string)
	staff := humancalling.ProviderFact{
		EventID: "unobserved-stop-staff-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now.Add(2 * time.Second), ConnectionID: "staff-call-control-connection",
		CallControlID: "unobserved-stop-staff-control",
		CallLegID:     "unobserved-stop-staff-leg", CallSessionID: "unobserved-stop-session",
		ClientState: staffState,
	}
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	staff.EventID = "unobserved-stop-staff-answered"
	staff.Type = humancalling.FactCallAnswered
	staff.OccurredAt = now.Add(3 * time.Second)
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	processed, err := calling.ProcessNextCommand(context.Background())
	if err != nil || !processed {
		t.Fatalf("process unobserved Stop Bridge = %t, %v", processed, err)
	}
	bridge := provider.last(humancalling.CommandBridge)
	bridgeState, _ := bridge.Payload["client_state"].(string)
	staff.EventID = "unobserved-stop-staff-bridged"
	staff.Type = humancalling.FactCallBridged
	staff.OccurredAt = now.Add(4 * time.Second)
	staff.ClientState = bridgeState
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	processed, err = calling.ProcessNextCommand(context.Background())
	if err != nil || !processed {
		t.Fatalf("process accepted unobserved Stop = %t, %v", processed, err)
	}
	stop := provider.last(humancalling.CommandStopRingWindow)
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET state = 'RECONCILED', updated_at = $2
		WHERE call_id = (
			SELECT call_id FROM human_calling_provider_commands WHERE id = $1
		) AND action = 'START_RING_WINDOW'
	`, stop.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET created_at = $2, updated_at = $2
		WHERE id = $1
	`, stop.ID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1
	`, stop.CallLegID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.observations = append(provider.observations, humancalling.ProviderCallObservation{
		Active: true, CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
	})
	provider.mu.Unlock()

	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("reconcile active unobserved Stop = %d, %v", reconciled, err)
	}
	var commandState string
	var degradedAudio int
	if err := pool.QueryRow(context.Background(), `
		SELECT command.state,
			(SELECT count(*) FROM human_calling_timeline
			 WHERE provider_command_id = command.id AND kind = 'caller_audio.degraded')
		FROM human_calling_provider_commands command WHERE command.id = $1
	`, stop.ID).Scan(&commandState, &degradedAudio); err != nil {
		t.Fatal(err)
	}
	if commandState != "AMBIGUOUS" || degradedAudio != 1 ||
		provider.count(humancalling.CommandStopRingWindow) != 1 {
		t.Fatalf("active unobserved Stop = state:%s degraded:%d effects:%d",
			commandState, degradedAudio,
			provider.count(humancalling.CommandStopRingWindow))
	}

	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "unobserved-stop-caller-hangup", Type: humancalling.FactCallHangup,
		OccurredAt: now.Add(5 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		HangupCause: "normal_clearing", TerminationSource: "caller",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1
	`, stop.CallLegID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("terminalize unobserved Stop = %d, %v", reconciled, err)
	}
	var convergedAudio, terminalized int
	if err := pool.QueryRow(context.Background(), `
		SELECT command.state,
			(SELECT count(*) FROM human_calling_timeline
			 WHERE provider_command_id = command.id AND kind = 'caller_audio.converged'),
			(SELECT count(*) FROM human_calling_timeline
			 WHERE provider_command_id = command.id AND kind = 'ring_window.terminalized')
		FROM human_calling_provider_commands command WHERE command.id = $1
	`, stop.ID).Scan(&commandState, &convergedAudio, &terminalized); err != nil {
		t.Fatal(err)
	}
	if commandState != "RECONCILED" || convergedAudio != 1 || terminalized != 1 ||
		provider.count(humancalling.CommandStopRingWindow) != 1 {
		t.Fatalf("terminal unobserved Stop = state:%s converged:%d terminalized:%d effects:%d",
			commandState, convergedAudio, terminalized,
			provider.count(humancalling.CommandStopRingWindow))
	}
}

func TestRingWindowFailureDoesNotBlockStaffDial(t *testing.T) {
	now := time.Date(2026, time.August, 5, 15, 30, 0, 0, time.UTC)
	provider := &recordingProvider{
		dialResults: []humancalling.ProviderResult{{
			CallControlID: "routing-failure-staff-control",
			CallLegID:     "routing-failure-staff-leg",
		}},
		actionErrors: map[humancalling.CommandAction][]error{
			humancalling.CommandStartRingWindow: {
				fmt.Errorf("%w: synthetic ring window rejection", humancalling.ErrDefinitiveProviderFailure),
			},
		},
	}
	pool, calling, _, _ := prepareInboundFanout(
		t, now, "call-leg-routing-failure", provider, 1,
	)
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !processed {
			break
		}
	}
	var terminal *string
	var staffState, ringState, dialState string
	var degraded int
	if err := pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome, staff.state,
			ring.state, dial.state,
			(
				SELECT count(*) FROM human_calling_timeline timeline
				WHERE timeline.call_id = call.id
					AND timeline.kind = 'caller_audio.degraded'
			)
		FROM human_calling_calls call
		JOIN human_calling_call_legs staff
			ON staff.call_id = call.id AND staff.role = 'STAFF'
		JOIN human_calling_provider_commands ring
			ON ring.call_id = call.id AND ring.action = 'START_RING_WINDOW'
		JOIN human_calling_provider_commands dial
			ON dial.call_id = call.id AND dial.action = 'DIAL_STAFF'
	`).Scan(&terminal, &staffState, &ringState, &dialState, &degraded); err != nil {
		t.Fatal(err)
	}
	if terminal != nil || staffState != "DIALING" || ringState != "FAILED" ||
		dialState != "SENT" || degraded != 1 ||
		provider.count(humancalling.CommandDialStaff) != 1 {
		t.Fatalf("ring-window degradation = terminal:%v Staff:%s ring:%s dial:%s timeline:%d commands:%#v",
			terminal, staffState, ringState, dialState, degraded, provider.commands)
	}
}

func TestStaleCallReconciliationQuarantinesContradictoryProviderObservation(t *testing.T) {
	now := time.Date(2026, time.August, 15, 13, 0, 0, 0, time.UTC)
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: "reconciliation-conflict-staff-control",
		CallLegID:     "reconciliation-conflict-staff-leg",
	}}}
	pool, calling, _, _ := prepareInboundFanout(
		t, now, "reconciliation-conflict", provider, 1,
	)
	processAllCommands(t, calling)
	var callID, staffLegID, commandID, providerControlID, providerLegID, clientState string
	if err := pool.QueryRow(context.Background(), `
			SELECT call.id::text, leg.id::text, command.id::text,
				leg.provider_call_control_id, leg.provider_call_leg_id,
				command.payload->>'client_state'
			FROM human_calling_calls call
			JOIN human_calling_call_legs leg
				ON leg.call_id = call.id AND leg.role = 'STAFF'
			JOIN human_calling_provider_commands command
				ON command.call_leg_id = leg.id AND command.action = 'DIAL_STAFF'
		`).Scan(
		&callID, &staffLegID, &commandID, &providerControlID, &providerLegID, &clientState,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
			UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1
		`, staffLegID, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.observations = append(provider.observations, humancalling.ProviderCallObservation{
		Active:        true,
		CallControlID: providerControlID,
		CallLegID:     providerLegID,
		CallSessionID: "reconciliation-conflict-session",
		Events: []humancalling.ProviderFact{{
			EventID:       "reconciliation-conflict-answer",
			Type:          humancalling.FactCallInitiated,
			OccurredAt:    now,
			CallControlID: providerControlID,
			CallLegID:     "contradictory-provider-leg",
			CallSessionID: "reconciliation-conflict-session",
			ClientState:   clientState,
		}},
	})
	provider.mu.Unlock()

	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 1 {
		t.Fatalf("quarantine contradictory stale observation = %d, %v", reconciled, err)
	}
	var terminalOutcome, legState, legCode, commandState, commandCode string
	if err := pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome, leg.state, COALESCE(leg.error_code, ''),
			command.state, COALESCE(command.last_error_code, '')
		FROM human_calling_calls call
		JOIN human_calling_call_legs leg ON leg.id = $2 AND leg.call_id = call.id
		JOIN human_calling_provider_commands command ON command.id = $3
		WHERE call.id = $1
	`, callID, staffLegID, commandID).Scan(
		&terminalOutcome, &legState, &legCode, &commandState, &commandCode,
	); err != nil {
		t.Fatal(err)
	}
	if terminalOutcome != "ROUTING_FAILED" || legState != "FAILED" ||
		legCode != "PROVIDER_OBSERVATION_CONFLICT" || commandState != "FAILED" ||
		commandCode != "PROVIDER_OBSERVATION_CONFLICT" {
		t.Fatalf("stale observation quarantine = Call:%s leg:%s/%s command:%s/%s",
			terminalOutcome, legState, legCode, commandState, commandCode)
	}
	if reconciled, err := calling.ReconcileStaleCalls(context.Background()); err != nil || reconciled != 0 {
		t.Fatalf("repeat contradictory stale observation = %d, %v", reconciled, err)
	}
}

func TestCancelledRingWindowWithProvisionalBridgeFailsRouting(t *testing.T) {
	now := time.Date(2026, time.August, 5, 16, 0, 0, 0, time.UTC)
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: "cancelled-ring-staff-control",
		CallLegID:     "cancelled-ring-staff-leg",
	}}}
	pool, calling, caller, _ := prepareInboundFanout(
		t, now, "call-leg-cancelled-ring", provider, 1,
	)
	processAllCommands(t, calling)
	dial := provider.last(humancalling.CommandDialStaff)
	staffState, _ := dial.Payload["client_state"].(string)
	staff := humancalling.ProviderFact{
		EventID: "cancelled-ring-staff-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now, ConnectionID: "staff-call-control-connection",
		CallControlID: "cancelled-ring-staff-control",
		CallLegID:     "cancelled-ring-staff-leg", CallSessionID: "cancelled-ring-session",
		ClientState: staffState,
	}
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	staff.EventID = "cancelled-ring-staff-answered"
	staff.Type = humancalling.FactCallAnswered
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	ring := provider.last(humancalling.CommandStartRingWindow)
	ringState, _ := ring.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "cancelled-ring-ended", Type: humancalling.FactPlaybackEnded,
		OccurredAt: now, CallControlID: caller.CallControlID, CallLegID: caller.CallLegID,
		CallSessionID: caller.CallSessionID, ClientState: ringState,
		PlaybackStatus: "cancelled",
	}); err != nil {
		t.Fatal(err)
	}
	var terminal string
	if err := pool.QueryRow(context.Background(), `
		SELECT terminal_outcome FROM human_calling_calls
	`).Scan(&terminal); err != nil {
		t.Fatal(err)
	}
	if terminal != "ROUTING_FAILED" || provider.count(humancalling.CommandSpeakVoicemail) != 0 {
		t.Fatalf("cancelled provisional Bridge outcome=%s commands=%#v", terminal, provider.commands)
	}
}

func TestLateStaffAnswerCannotBridgeAfterVoicemailStarts(t *testing.T) {
	now := time.Date(2026, time.August, 5, 16, 30, 0, 0, time.UTC)
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: "late-answer-staff-control", CallLegID: "late-answer-staff-leg",
	}}}
	_, calling, caller, _ := prepareInboundFanout(
		t, now, "call-leg-late-answer", provider, 1,
	)
	processAllCommands(t, calling)
	dial := provider.last(humancalling.CommandDialStaff)
	staffState, _ := dial.Payload["client_state"].(string)
	staff := humancalling.ProviderFact{
		EventID: "late-answer-staff-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now, ConnectionID: "staff-call-control-connection",
		CallControlID: "late-answer-staff-control", CallLegID: "late-answer-staff-leg",
		CallSessionID: "late-answer-session", ClientState: staffState,
	}
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	ring := provider.last(humancalling.CommandStartRingWindow)
	ringState, _ := ring.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "late-answer-ring-completed", Type: humancalling.FactPlaybackEnded,
		OccurredAt: now, CallControlID: caller.CallControlID, CallLegID: caller.CallLegID,
		CallSessionID: caller.CallSessionID, ClientState: ringState,
		PlaybackStatus: "completed",
	}); err != nil {
		t.Fatal(err)
	}
	staff.EventID = "late-answer-staff-answered"
	staff.Type = humancalling.FactCallAnswered
	if err := calling.ApplyProviderFact(context.Background(), staff); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	if provider.count(humancalling.CommandBridge) != 0 ||
		provider.count(humancalling.CommandSpeakVoicemail) != 1 {
		t.Fatalf("late answer commands = %#v", provider.commands)
	}
}

func TestVoicemailEvidenceRequiresExactCallerAndUpgradesRecoveryTask(t *testing.T) {
	now := time.Date(2026, time.August, 5, 17, 0, 0, 0, time.UTC)
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: "voicemail-staff-control", CallLegID: "voicemail-staff-leg",
	}}}
	pool, calling, caller, staff := prepareInboundFanout(
		t, now, "call-leg-voicemail-evidence", provider, 1,
	)
	processAllCommands(t, calling)
	ring := provider.last(humancalling.CommandStartRingWindow)
	ringState, _ := ring.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "voicemail-ring-completed", Type: humancalling.FactPlaybackEnded,
		OccurredAt: now.Add(20 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: ringState, PlaybackStatus: "completed",
	}); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	greetingState, err := calling.ReadCallingState(context.Background(), staff[0])
	if err != nil {
		t.Fatalf("read live voicemail greeting state: %v", err)
	}
	if greetingState.Voicemail == nil ||
		greetingState.Voicemail.State != "VOICEMAIL_GREETING" {
		t.Fatalf("live voicemail greeting state = %#v", greetingState.Voicemail)
	}
	greetingCall, err := calling.ReadCall(
		context.Background(), staff[0], greetingState.Voicemail.CallID,
	)
	if err != nil || string(greetingCall.State) != "VOICEMAIL_GREETING" ||
		greetingCall.Voicemail.TaskID != "" {
		t.Fatalf("live voicemail greeting Call = %#v, err = %v", greetingCall, err)
	}
	speak := provider.last(humancalling.CommandSpeakVoicemail)
	speakState, _ := speak.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "voicemail-speak-wrong-caller", Type: humancalling.FactSpeakEnded,
		OccurredAt: now.Add(21 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: "wrong-caller-leg", CallSessionID: caller.CallSessionID,
		ClientState: speakState, PlaybackStatus: "completed",
	}); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("mismatched voicemail greeting identity error = %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "voicemail-speak-completed", Type: humancalling.FactSpeakEnded,
		OccurredAt: now.Add(21 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: speakState, PlaybackStatus: "completed",
	}); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	recordingState, err := calling.ReadCallingState(context.Background(), staff[0])
	if err != nil {
		t.Fatalf("read live voicemail recording state: %v", err)
	}
	if recordingState.Voicemail == nil ||
		recordingState.Voicemail.State != "VOICEMAIL_RECORDING" {
		t.Fatalf("live voicemail recording state = %#v", recordingState.Voicemail)
	}
	record := provider.last(humancalling.CommandStartVoicemailRecording)
	recordState, _ := record.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "voicemail-recording-wrong-caller", Type: humancalling.FactRecordingError,
		OccurredAt: now.Add(22 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: "wrong-caller-session",
		ClientState: recordState,
	}); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("mismatched voicemail recording identity error = %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "voicemail-recording-error", Type: humancalling.FactRecordingError,
		OccurredAt: now.Add(22 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: recordState,
	}); err != nil {
		t.Fatal(err)
	}
	provider.recording = humancalling.ProviderRecording{
		ID: "recording-after-error", CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		StartedAt: now.Add(15 * time.Second), EndedAt: now.Add(23 * time.Second),
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "voicemail-recording-saved-after-error", Type: humancalling.FactRecordingSaved,
		OccurredAt: now.Add(23 * time.Second),
		CallLegID:  caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState:        recordState,
		RecordingStartedAt: now.Add(15 * time.Second),
		RecordingEndedAt:   now.Add(23 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	var callOutcome, voicemailOutcome, audioState, taskTitle, taskOrigin, taskOutcome string
	var acknowledgementCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome, voicemail.outcome, voicemail.audio_state,
			task.title, task.origin, task.recovery_outcome,
			(SELECT count(*) FROM work_task_acknowledgements WHERE task_id = task.id)
		FROM human_calling_calls call
		JOIN human_calling_voicemails voicemail ON voicemail.call_id = call.id
		JOIN work_tasks task ON task.id = voicemail.task_id
	`).Scan(
		&callOutcome, &voicemailOutcome, &audioState,
		&taskTitle, &taskOrigin, &taskOutcome, &acknowledgementCount,
	); err != nil {
		t.Fatal(err)
	}
	if callOutcome != "VOICEMAIL" || voicemailOutcome != "VOICEMAIL" ||
		audioState != "READY" || taskTitle != "Review voicemail" ||
		taskOrigin != "VOICEMAIL_RECOVERY" || taskOutcome != "VOICEMAIL" ||
		acknowledgementCount != 0 {
		t.Fatalf("upgraded voicemail recovery = %s %s %s %s %s %s acknowledgements:%d",
			callOutcome, voicemailOutcome, audioState, taskTitle, taskOrigin, taskOutcome,
			acknowledgementCount)
	}
	savedState, err := calling.ReadCallingState(context.Background(), staff[0])
	if err != nil {
		t.Fatalf("read saved voicemail state: %v", err)
	}
	if savedState.Voicemail == nil || savedState.Voicemail.State != "VOICEMAIL" {
		t.Fatalf("saved voicemail state = %#v", savedState.Voicemail)
	}
}

func TestFailedVoicemailGreetingCreatesMissedCallWithoutRecording(t *testing.T) {
	now := time.Date(2026, time.August, 5, 17, 15, 0, 0, time.UTC)
	provider := &recordingProvider{dialResults: []humancalling.ProviderResult{{
		CallControlID: "failed-greeting-staff-control", CallLegID: "failed-greeting-staff-leg",
	}}}
	pool, calling, caller, _ := prepareInboundFanout(
		t, now, "failed-voicemail-greeting", provider, 1,
	)
	processAllCommands(t, calling)
	ring := provider.last(humancalling.CommandStartRingWindow)
	ringState, _ := ring.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "failed-greeting-ring-completed", Type: humancalling.FactPlaybackEnded,
		OccurredAt: now.Add(20 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: ringState, PlaybackStatus: "completed",
	}); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	speak := provider.last(humancalling.CommandSpeakVoicemail)
	speakState, _ := speak.Payload["client_state"].(string)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "failed-greeting-ended", Type: humancalling.FactSpeakEnded,
		OccurredAt: now.Add(21 * time.Second), CallControlID: caller.CallControlID,
		CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		ClientState: speakState, PlaybackStatus: "call_hangup",
	}); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	var terminal, audioState, taskOrigin string
	if err := pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome, voicemail.audio_state, task.origin
		FROM human_calling_calls call
		JOIN human_calling_voicemails voicemail ON voicemail.call_id = call.id
		JOIN work_tasks task ON task.id = voicemail.task_id
	`).Scan(&terminal, &audioState, &taskOrigin); err != nil {
		t.Fatal(err)
	}
	if terminal != "MISSED" || audioState != "UNAVAILABLE" ||
		taskOrigin != "MISSED_CALL_RECOVERY" ||
		provider.count(humancalling.CommandStartVoicemailRecording) != 0 {
		t.Fatalf("failed greeting outcome = %s %s %s commands=%#v",
			terminal, audioState, taskOrigin, provider.commands)
	}
}

func TestExpiredConnectedRecordingIsDeletedFromTelnyxAndRetainsAuditMetadata(t *testing.T) {
	pool := testdb.Open(t)
	createdAt := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	currentTime := createdAt
	var metrics bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimeWorker,
		"recording-retention-test",
		slog.New(slog.NewJSONHandler(&metrics, nil)),
	)
	accessModule := access.New(pool, func() time.Time { return currentTime })
	if _, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "recording-retention-test",
		PlatformOperators: []string{"operator@example.test"},
		Practices: []access.PracticeProvision{{
			Key:                                 "recording-retention",
			Name:                                "Recording Retention",
			ConnectedCallRecordingEnabled:       true,
			ConnectedCallRecordingRetentionDays: 30,
			Locations:                           []access.LocationProvision{{Key: "office", Name: "Office"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var practiceID, locationID string
	if err := pool.QueryRow(context.Background(), `
		SELECT practice.id::text, location.id::text
		FROM access_practices practice
		JOIN access_locations location ON location.practice_id = practice.id
		WHERE practice.provisioning_key = 'recording-retention'
	`).Scan(&practiceID, &locationID); err != nil {
		t.Fatal(err)
	}
	const callID = "10000000-0000-4000-8000-000000000001"
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_calls (
			id, practice_id, location_id, direction, entry_point,
			terminal_outcome, ended_at, version, created_at, updated_at
		) VALUES ($1, $2, $3, 'OUTBOUND', 'STANDALONE', 'RESOLVED', $4, 1, $4, $4)
	`, callID, practiceID, locationID, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_call_recordings (
			call_id, practice_id, location_id, audio_state,
			provider_recording_id, retention_days, recording_started_at,
			recording_ended_at, content_expires_at, duration_millis,
			created_at, updated_at
		) VALUES ($1, $2, $3, 'READY', 'recording-to-delete',
			30, $4::timestamptz - interval '1 minute', $4,
			$4::timestamptz + interval '30 days', 60000, $4, $4)
	`, callID, practiceID, locationID, createdAt); err != nil {
		t.Fatal(err)
	}
	provider := &recordingProvider{deletionErr: humancalling.ErrAmbiguousEffect}
	calling := humancalling.New(
		pool,
		accessModule,
		provider,
		humancalling.Config{Observer: observer},
		func() time.Time { return currentTime },
	)
	currentTime = createdAt.Add(31 * 24 * time.Hour)
	operator := access.Identity{
		Subject:       "recording-retention-operator",
		Email:         "operator@example.test",
		EmailVerified: true,
	}
	expiredCall, err := calling.ReadCall(context.Background(), operator, callID)
	if err != nil || expiredCall.Recording.AudioState != humancalling.RecordingExpired {
		t.Fatalf("expired recording projection = %#v, %v", expiredCall.Recording, err)
	}
	if _, err := calling.IssueCallRecordingPlayback(
		context.Background(), operator, callID,
	); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("expired recording playback error = %v", err)
	}
	if err := calling.ReportReceiptQueue(context.Background()); err != nil {
		t.Fatalf("report recording retention queue: %v", err)
	}
	for _, fragment := range []string{
		`"metric":"acuity_call_center_recording_queue"`,
		`"retention_depth":1`,
		`"oldest_retention_age_seconds":86400`,
	} {
		if !strings.Contains(metrics.String(), fragment) {
			t.Fatalf("recording retention queue metric omitted %s: %s",
				fragment, metrics.String())
		}
	}
	processed, err := calling.ProcessNextRecordingRetention(context.Background())
	if !errors.Is(err, humancalling.ErrAmbiguousEffect) || !processed {
		t.Fatalf("failed recording retention = %t, %v", processed, err)
	}
	if !strings.Contains(metrics.String(), `"operation":"retention"`) ||
		!strings.Contains(metrics.String(), `"outcome":"retry"`) {
		t.Fatalf("recording retention retry metric missing: %s", metrics.String())
	}
	var nextDeletionAttempt time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT next_deletion_attempt_at
		FROM human_calling_call_recordings WHERE call_id = $1
	`, callID).Scan(&nextDeletionAttempt); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.deletionErr = nil
	provider.mu.Unlock()
	currentTime = nextDeletionAttempt
	processed, err = calling.ProcessNextRecordingRetention(context.Background())
	if err != nil || !processed {
		t.Fatalf("retry recording retention = %t, %v", processed, err)
	}
	if !strings.Contains(metrics.String(), `"outcome":"succeeded"`) {
		t.Fatalf("recording retention success metric missing: %s", metrics.String())
	}
	provider.mu.Lock()
	deleted := append([]string(nil), provider.deletedRecordings...)
	provider.mu.Unlock()
	if len(deleted) != 2 || deleted[0] != "recording-to-delete" ||
		deleted[1] != "recording-to-delete" {
		t.Fatalf("deleted provider recordings = %#v", deleted)
	}
	var state, providerRecordingID string
	var deletedAt *time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT audio_state, provider_recording_id, content_deleted_at
		FROM human_calling_call_recordings WHERE call_id = $1
	`, callID).Scan(&state, &providerRecordingID, &deletedAt); err != nil {
		t.Fatal(err)
	}
	if state != "DELETED" || providerRecordingID != "recording-to-delete" ||
		deletedAt == nil || !deletedAt.Equal(currentTime) {
		t.Fatalf("retained recording audit = %q %q %v", state, providerRecordingID, deletedAt)
	}
}

func TestStaleConnectedRecordingReconcilesLostTerminalWebhooks(t *testing.T) {
	pool := testdb.Open(t)
	endedAt := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	currentTime := endedAt.Add(3 * time.Minute)
	var metrics bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimeWorker,
		"recording-reconciliation-test",
		slog.New(slog.NewJSONHandler(&metrics, nil)),
	)
	accessModule := access.New(pool, func() time.Time { return currentTime })
	if _, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "recording-reconciliation-test",
		PlatformOperators: []string{"recording-operator@example.test"},
		Practices: []access.PracticeProvision{{
			Key:                                 "recording-reconciliation",
			Name:                                "Recording Reconciliation",
			ConnectedCallRecordingEnabled:       true,
			ConnectedCallRecordingRetentionDays: 90,
			Locations:                           []access.LocationProvision{{Key: "office", Name: "Office"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var practiceID, locationID string
	if err := pool.QueryRow(context.Background(), `
		SELECT practice.id::text, location.id::text
		FROM access_practices practice
		JOIN access_locations location ON location.practice_id = practice.id
		WHERE practice.provisioning_key = 'recording-reconciliation'
	`).Scan(&practiceID, &locationID); err != nil {
		t.Fatal(err)
	}
	const callID = "20000000-0000-4000-8000-000000000001"
	const callLegID = "20000000-0000-4000-8000-000000000002"
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_calls (
			id, practice_id, location_id, direction, entry_point,
			terminal_outcome, ended_at, version, created_at, updated_at
		) VALUES ($1, $2, $3, 'OUTBOUND', 'STANDALONE', 'RESOLVED', $4, 1, $5, $4)
	`, callID, practiceID, locationID, endedAt, endedAt.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_call_legs (
			id, call_id, role, sequence, state, provider_connection_id,
			provider_call_control_id, provider_call_leg_id, provider_call_session_id,
			answered_at, bridge_pending_at, bridged_at, ending_at, ended_at,
			created_at, updated_at
		) VALUES ($1, $2, 'DESTINATION', 1, 'ENDED', 'recording-connection',
			'recording-control', 'recording-leg', 'recording-session',
			$3, $3, $3, $4, $4, $3, $4)
	`, callLegID, callID, endedAt.Add(-time.Minute), endedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_provider_commands (
			call_id, call_leg_id, action, target_id, payload, state,
			sent_at, created_at, updated_at
		) VALUES ($1, $2, 'BRIDGE', 'recording-control', '{}', 'RECONCILED', $3, $3, $3)
	`, callID, callLegID, endedAt.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_call_recordings (
			call_id, practice_id, location_id, audio_state, retention_days,
			created_at, updated_at
		) VALUES ($1, $2, $3, 'PROCESSING', 90, $4, $4)
	`, callID, practiceID, locationID, endedAt.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	provider := &recordingProvider{
		recording: humancalling.ProviderRecording{
			ID: "reconciled-recording", CallControlID: "recording-control",
			CallLegID: "recording-leg", CallSessionID: "recording-session",
			StartedAt: endedAt.Add(-time.Minute), EndedAt: endedAt,
		},
		recordingErr: humancalling.ErrProviderRecordingFailed,
	}
	calling := humancalling.New(
		pool,
		accessModule,
		provider,
		humancalling.Config{Observer: observer},
		func() time.Time { return currentTime },
	)
	if err := calling.ReportReceiptQueue(context.Background()); err != nil {
		t.Fatalf("report recording reconciliation queue: %v", err)
	}
	for _, fragment := range []string{
		`"metric":"acuity_call_center_recording_queue"`,
		`"reconciliation_depth":1`,
		`"oldest_reconciliation_age_seconds":180`,
	} {
		if !strings.Contains(metrics.String(), fragment) {
			t.Fatalf("recording reconciliation queue metric omitted %s: %s",
				fragment, metrics.String())
		}
	}
	processed, err := calling.ProcessNextRecordingReconciliation(context.Background())
	if err != nil || !processed {
		t.Fatalf("failed recording reconciliation = %t, %v", processed, err)
	}
	failedCall, err := calling.ReadCall(context.Background(), access.Identity{
		Subject:       "recording-operator",
		Email:         "recording-operator@example.test",
		EmailVerified: true,
	}, callID)
	if err != nil || failedCall.Recording.AudioState != humancalling.RecordingUnavailable {
		t.Fatalf("failed recording projection = %#v, %v", failedCall.Recording, err)
	}
	if !strings.Contains(metrics.String(),
		`"metric":"acuity_call_center_recording_maintenance"`) ||
		!strings.Contains(metrics.String(), `"outcome":"unavailable"`) {
		t.Fatalf("recording failure metric missing: %s", metrics.String())
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_recordings SET
			audio_state = 'PROCESSING', last_error_code = NULL,
			reconciliation_attempts = 0, reconciliation_claimed_at = NULL,
			next_reconciliation_attempt_at = NULL,
			reconciliation_error_code = NULL, updated_at = $2
		WHERE call_id = $1
	`, callID, currentTime); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.recordingErr = humancalling.ErrAmbiguousEffect
	provider.mu.Unlock()

	processed, err = calling.ProcessNextRecordingReconciliation(context.Background())
	if !processed || !errors.Is(err, humancalling.ErrAmbiguousEffect) {
		t.Fatalf("first recording reconciliation = %t, %v", processed, err)
	}
	var attempts int
	var claimedAt *time.Time
	var nextAttempt time.Time
	var reconciliationError string
	if err := pool.QueryRow(context.Background(), `
		SELECT reconciliation_attempts, reconciliation_claimed_at,
			next_reconciliation_attempt_at, reconciliation_error_code
		FROM human_calling_call_recordings WHERE call_id = $1
	`, callID).Scan(&attempts, &claimedAt, &nextAttempt, &reconciliationError); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || claimedAt != nil || !nextAttempt.After(currentTime) ||
		reconciliationError == "" {
		t.Fatalf("recording reconciliation retry = %d %v %v %q",
			attempts, claimedAt, nextAttempt, reconciliationError)
	}
	if !strings.Contains(metrics.String(), `"outcome":"retry"`) {
		t.Fatalf("recording retry metric missing: %s", metrics.String())
	}
	if processed, err := calling.ProcessNextRecordingReconciliation(context.Background()); err != nil || processed {
		t.Fatalf("early recording reconciliation retry = %t, %v", processed, err)
	}
	for expectedAttempts := 2; expectedAttempts <= 10; expectedAttempts++ {
		currentTime = nextAttempt
		processed, err = calling.ProcessNextRecordingReconciliation(context.Background())
		if !processed {
			t.Fatalf("recording reconciliation attempt %d was not processed", expectedAttempts)
		}
		if expectedAttempts < 10 && !errors.Is(err, humancalling.ErrAmbiguousEffect) {
			t.Fatalf("recording reconciliation attempt %d error = %v",
				expectedAttempts, err)
		}
		if expectedAttempts == 10 && err != nil {
			t.Fatalf("terminal recording reconciliation attempt error = %v", err)
		}
		var state string
		var lastError string
		if err := pool.QueryRow(context.Background(), `
			SELECT audio_state, reconciliation_attempts,
				COALESCE(next_reconciliation_attempt_at, $2),
				COALESCE(last_error_code, '')
			FROM human_calling_call_recordings WHERE call_id = $1
		`, callID, currentTime).Scan(
			&state, &attempts, &nextAttempt, &lastError,
		); err != nil {
			t.Fatal(err)
		}
		if attempts != expectedAttempts {
			t.Fatalf("recording reconciliation attempts = %d, want %d",
				attempts, expectedAttempts)
		}
		if expectedAttempts < 10 && state != "PROCESSING" {
			t.Fatalf("recording state after retry %d = %q", expectedAttempts, state)
		}
		if expectedAttempts == 10 && (state != "UNAVAILABLE" ||
			lastError != "RECONCILIATION_RETRY_EXHAUSTED") {
			t.Fatalf("recording after retry exhaustion = state:%q error:%q",
				state, lastError)
		}
	}
	exhaustedCall, err := calling.ReadCall(context.Background(), access.Identity{
		Subject:       "recording-operator",
		Email:         "recording-operator@example.test",
		EmailVerified: true,
	}, callID)
	if err != nil || exhaustedCall.Recording.AudioState != humancalling.RecordingUnavailable {
		t.Fatalf("exhausted recording projection = %#v, %v", exhaustedCall.Recording, err)
	}
	if !strings.Contains(metrics.String(), `"outcome":"exhausted"`) ||
		!strings.Contains(metrics.String(), `"attempt":10`) {
		t.Fatalf("recording exhaustion metric missing: %s", metrics.String())
	}
	bridgeClientState := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"v":2,"call":%q,"call_leg":%q,"role":"DESTINATION","kind":"bridge"}`,
		callID, callLegID,
	)))
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:            "late-recording-saved-after-reconciliation-exhaustion",
		Type:               humancalling.FactRecordingSaved,
		OccurredAt:         currentTime,
		CallControlID:      "recording-control",
		CallLegID:          "recording-leg",
		CallSessionID:      "recording-session",
		ClientState:        bridgeClientState,
		RecordingID:        "reconciled-recording",
		RecordingStartedAt: endedAt.Add(-time.Minute),
		RecordingEndedAt:   endedAt,
	}); err != nil {
		t.Fatalf("apply late saved recording webhook: %v", err)
	}
	var state, providerRecordingID string
	var contentExpiresAt time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT audio_state, provider_recording_id, content_expires_at
		FROM human_calling_call_recordings WHERE call_id = $1
	`, callID).Scan(&state, &providerRecordingID, &contentExpiresAt); err != nil {
		t.Fatal(err)
	}
	if state != "READY" || providerRecordingID != "reconciled-recording" ||
		!contentExpiresAt.Equal(endedAt.Add(90*24*time.Hour)) {
		t.Fatalf("reconciled recording = %q %q %v", state, providerRecordingID, contentExpiresAt)
	}
}

type recordingProvider struct {
	mu                sync.Mutex
	commands          []humancalling.ProviderCommand
	dialResults       []humancalling.ProviderResult
	actionErrors      map[humancalling.CommandAction][]error
	observations      []humancalling.ProviderCallObservation
	recording         humancalling.ProviderRecording
	recordingErr      error
	deletedRecordings []string
	deletionErr       error
	blockAction       humancalling.CommandAction
	blockStarted      chan struct{}
	blockRelease      chan struct{}
	blockError        error
}

func (provider *recordingProvider) DeleteRecording(
	_ context.Context,
	recordingID string,
) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.deletedRecordings = append(provider.deletedRecordings, recordingID)
	return provider.deletionErr
}

type credentialFailureProvider struct {
	executeErr  error
	lookupErr   error
	lookupCalls int
}

func (provider *credentialFailureProvider) Execute(
	context.Context,
	humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	return humancalling.ProviderResult{}, provider.executeErr
}

func (provider *credentialFailureProvider) FindCredentialByName(
	context.Context,
	string,
) (humancalling.ProviderResult, bool, error) {
	provider.lookupCalls++
	return humancalling.ProviderResult{}, false, provider.lookupErr
}

type commandOnlyProvider struct{}

func (commandOnlyProvider) Execute(
	context.Context,
	humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	return humancalling.ProviderResult{}, nil
}

type blockingDialProvider struct {
	mu             sync.Mutex
	started        chan struct{}
	release        chan struct{}
	commands       []humancalling.ProviderCommand
	dialExecutions []blockingDialExecution
}

type blockingDialExecution struct {
	command humancalling.ProviderCommand
	result  humancalling.ProviderResult
}

func (provider *blockingDialProvider) Execute(
	_ context.Context,
	command humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	provider.mu.Lock()
	provider.commands = append(provider.commands, command)
	if command.Action != humancalling.CommandDialStaff {
		provider.mu.Unlock()
		return humancalling.ProviderResult{}, nil
	}
	suffix := fmt.Sprint(len(provider.dialExecutions) + 1)
	result := humancalling.ProviderResult{
		CallControlID: "concurrent-dial-control-" + suffix,
		CallLegID:     "concurrent-dial-leg-" + suffix,
	}
	provider.dialExecutions = append(provider.dialExecutions, blockingDialExecution{
		command: command,
		result:  result,
	})
	provider.mu.Unlock()
	provider.started <- struct{}{}
	<-provider.release
	return result, nil
}

func (provider *blockingDialProvider) count() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return len(provider.dialExecutions)
}

func (provider *blockingDialProvider) dials() []blockingDialExecution {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]blockingDialExecution(nil), provider.dialExecutions...)
}

func (provider *blockingDialProvider) all(
	action humancalling.CommandAction,
) []humancalling.ProviderCommand {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	commands := []humancalling.ProviderCommand{}
	for _, command := range provider.commands {
		if command.Action == action {
			commands = append(commands, command)
		}
	}
	return commands
}

func (provider *recordingProvider) ResolveRecording(
	_ context.Context,
	callLegID string,
	callSessionID string,
) (humancalling.ProviderRecording, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.recordingErr != nil {
		return humancalling.ProviderRecording{}, provider.recordingErr
	}
	if provider.recording.CallLegID != callLegID ||
		provider.recording.CallSessionID != callSessionID {
		return humancalling.ProviderRecording{}, humancalling.ErrAmbiguousEffect
	}
	return provider.recording, nil
}

func (provider *recordingProvider) ObserveCall(
	_ context.Context,
	_ string,
	callControlID string,
	callLegID string,
	_ string,
	_ time.Time,
) (humancalling.ProviderCallObservation, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.observations) > 0 {
		observation := provider.observations[0]
		provider.observations = provider.observations[1:]
		return observation, nil
	}
	return humancalling.ProviderCallObservation{
		Active: true, CallControlID: callControlID, CallLegID: callLegID,
	}, nil
}

func (provider *recordingProvider) Execute(
	_ context.Context,
	command humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	provider.mu.Lock()
	provider.commands = append(provider.commands, command)
	if queued := provider.actionErrors[command.Action]; len(queued) > 0 {
		err := queued[0]
		provider.actionErrors[command.Action] = queued[1:]
		provider.mu.Unlock()
		return humancalling.ProviderResult{}, err
	}
	if provider.blockAction == command.Action && provider.blockStarted != nil &&
		provider.blockRelease != nil {
		started, release := provider.blockStarted, provider.blockRelease
		blockError := provider.blockError
		provider.blockAction = ""
		provider.mu.Unlock()
		close(started)
		<-release
		if blockError != nil {
			return humancalling.ProviderResult{}, blockError
		}
		provider.mu.Lock()
	}
	defer provider.mu.Unlock()
	switch command.Action {
	case humancalling.CommandCreateCredential:
		name, _ := command.Payload["name"].(string)
		return humancalling.ProviderResult{
			CredentialID: "credential-" + name,
			SIPUsername:  "sip-" + name,
		}, nil
	case humancalling.CommandDialStaff, humancalling.CommandDialOutboundStaff,
		humancalling.CommandDialOutboundDestination:
		if len(provider.dialResults) > 0 {
			result := provider.dialResults[0]
			provider.dialResults = provider.dialResults[1:]
			return result, nil
		}
	}
	return humancalling.ProviderResult{}, nil
}

func prepareInboundFanout(
	t *testing.T,
	now time.Time,
	prefix string,
	provider *recordingProvider,
	staffCount int,
	observers ...observability.Observer,
) (*pgxpool.Pool, *humancalling.Module, humancalling.ProviderFact, []access.Identity) {
	t.Helper()
	pool := testdb.Open(t)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, staff := provisionConcurrentStaff(t, accessModule, now, prefix, staffCount)
	var observer observability.Observer
	if len(observers) > 0 {
		observer = observers[0]
	}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain:       "synthetic.sip.telnyx.com",
		StaffSIPDomain:         "sip.telnyx.com",
		RingWindowDuration:     20 * time.Second,
		HandoffTokenKey:        []byte("0123456789abcdef0123456789abcdef"),
		CallControlID:          "staff-call-control-connection",
		CredentialConnectionID: "staff-credential-connection",
		FromNumber:             "+14843336938",
		RingbackURL:            "https://media.synthetic.test/ringback.wav",
		Observer:               observer,
	}, func() time.Time { return now })
	prepareCredentials(t, calling)
	readyConcurrentStaff(t, calling, staff, prefix+"-browser")
	_, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject: "abita-" + prefix, PracticeID: authorization.Practice.ID,
		},
		LocationID: authorization.Locations[0].ID, SourceCallID: prefix + "-source",
		IdempotencyKey: prefix + "-handoff",
		Contact:        humancalling.ContactContext{Phone: "+15555550100"},
	})
	if err != nil {
		t.Fatal(err)
	}
	caller := humancalling.ProviderFact{
		EventID: prefix + "-caller-initiated", Type: humancalling.FactCallInitiated,
		OccurredAt: now, ConnectionID: "staff-call-control-connection",
		CallControlID: prefix + "-caller-control", CallLegID: prefix + "-caller-leg",
		CallSessionID: prefix + "-caller-session", From: "+15555550100",
		To: "+14843989071",
	}
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	caller.EventID = prefix + "-caller-answered"
	caller.Type = humancalling.FactCallAnswered
	caller.OccurredAt = now.Add(time.Second)
	if err := calling.ApplyProviderFact(context.Background(), caller); err != nil {
		t.Fatal(err)
	}
	return pool, calling, caller, staff
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

func (provider *recordingProvider) last(
	action humancalling.CommandAction,
) humancalling.ProviderCommand {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	for index := len(provider.commands) - 1; index >= 0; index-- {
		if provider.commands[index].Action == action {
			return provider.commands[index]
		}
	}
	return humancalling.ProviderCommand{}
}

func (provider *recordingProvider) all(
	action humancalling.CommandAction,
) []humancalling.ProviderCommand {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	result := []humancalling.ProviderCommand{}
	for _, command := range provider.commands {
		if command.Action == action {
			result = append(result, command)
		}
	}
	return result
}

func prepareCredentials(t *testing.T, calling *humancalling.Module) {
	t.Helper()
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("reconcile managed credentials: %v", err)
	}
	processAllCommands(t, calling)
}

func provisionConcurrentStaff(
	t *testing.T,
	accessModule *access.Module,
	now time.Time,
	prefix string,
	count int,
) (access.Authorization, []access.Identity) {
	t.Helper()
	accessGrants := make([]access.AccessGrantProvision, count)
	identities := make([]access.Identity, count)
	for index := range count {
		email := fmt.Sprintf("%s-staff-%d@synthetic.test", prefix, index+1)
		accessGrants[index] = access.AccessGrantProvision{
			Key: fmt.Sprintf("%s-staff-%d", prefix, index+1), Email: email,
			Role: access.RoleStaff, LocationScope: access.LocationScopeAll,
		}
		identities[index] = access.Identity{
			Subject: fmt.Sprintf("%s-staff-%d", prefix, index+1),
			Email:   email, EmailVerified: true,
		}
	}
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test", RequestedBy: prefix + "-test",
		Practices: []access.PracticeProvision{{
			Key: prefix + "-practice", Name: prefix + " practice",
			Locations: []access.LocationProvision{{
				Key: prefix + "-location", Name: prefix + " location",
			}},
			AccessGrants: accessGrants,
		}},
	})
	if err != nil {
		t.Fatalf("provision %s staff: %v", prefix, err)
	}
	var authorization access.Authorization
	for _, identity := range identities {
		authorization = testaccess.Activate(t, accessModule, identity)
	}
	return authorization, identities
}

func readyConcurrentStaff(
	t *testing.T,
	calling *humancalling.Module,
	identities []access.Identity,
	sessionPrefix string,
) {
	t.Helper()
	for index, identity := range identities {
		sessionID := fmt.Sprintf("%s-%d", sessionPrefix, index+1)
		if _, err := calling.AcquireSoftphone(
			context.Background(), identity, sessionID, false,
		); err != nil {
			t.Fatalf("acquire %s softphone %d: %v", sessionPrefix, index+1, err)
		}
		if _, err := calling.SetReadiness(context.Background(),
			humancalling.ReadinessCommand{
				Identity: identity, SessionID: sessionID, Registered: true,
				MicrophoneReady: true, AudioReady: true, SessionHealthy: true,
				Available: true,
			}); err != nil {
			t.Fatalf("ready %s softphone %d: %v", sessionPrefix, index+1, err)
		}
	}
}

func processAllCommands(t *testing.T, calling *humancalling.Module) {
	t.Helper()
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process provider command: %v", err)
		}
		if !processed {
			return
		}
	}
}

type postgresAdvisoryBarrier struct {
	t          *testing.T
	connection *pgxpool.Conn
	key        int64
	pid        int32
	locked     bool
}

func holdPostgresAdvisoryLock(
	t *testing.T,
	pool *pgxpool.Pool,
	key int64,
) *postgresAdvisoryBarrier {
	t.Helper()
	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire PostgreSQL advisory barrier connection: %v", err)
	}
	barrier := &postgresAdvisoryBarrier{
		t: t, connection: connection, key: key,
	}
	if err := connection.QueryRow(
		context.Background(), `SELECT pg_backend_pid()`,
	).Scan(&barrier.pid); err != nil {
		connection.Release()
		t.Fatalf("read PostgreSQL advisory barrier backend PID: %v", err)
	}
	if _, err := connection.Exec(
		context.Background(), `SELECT pg_advisory_lock($1)`, key,
	); err != nil {
		connection.Release()
		t.Fatalf("lock PostgreSQL advisory barrier: %v", err)
	}
	barrier.locked = true
	return barrier
}

func (barrier *postgresAdvisoryBarrier) release() {
	barrier.t.Helper()
	if !barrier.locked {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := barrier.connection.Exec(
		ctx, `SELECT pg_advisory_unlock($1)`, barrier.key,
	); err != nil {
		barrier.t.Fatalf("release PostgreSQL advisory barrier: %v", err)
	}
	barrier.locked = false
}

func (barrier *postgresAdvisoryBarrier) close() {
	if barrier.locked {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if _, err := barrier.connection.Exec(
			ctx, `SELECT pg_advisory_unlock($1)`, barrier.key,
		); err != nil {
			barrier.t.Errorf("clean up PostgreSQL advisory barrier: %v", err)
		}
		cancel()
		barrier.locked = false
	}
	barrier.connection.Release()
}

func installPostgresTestTrigger(
	t *testing.T,
	pool *pgxpool.Pool,
	createSQL string,
	dropSQL string,
) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), createSQL); err != nil {
		t.Fatalf("install PostgreSQL test trigger: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, dropSQL); err != nil {
			t.Errorf("clean up PostgreSQL test trigger: %v", err)
		}
	})
}

func waitForPostgresLockWaiter(
	t *testing.T,
	connection *pgxpool.Conn,
	waitEvent string,
	blockerPID int32,
) int32 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pid, waiting, err := findPostgresLockWaiter(
			context.Background(), connection, waitEvent, blockerPID,
		)
		if err != nil {
			t.Fatalf("inspect PostgreSQL %s lock waiters: %v", waitEvent, err)
		}
		if waiting {
			return pid
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("PostgreSQL did not expose a %s lock waiter blocked by PID %d",
		waitEvent, blockerPID)
	return 0
}

func findPostgresLockWaiter(
	ctx context.Context,
	connection *pgxpool.Conn,
	waitEvent string,
	blockerPID int32,
) (int32, bool, error) {
	var pid int32
	err := connection.QueryRow(ctx, `
		SELECT pid
		FROM pg_stat_activity
		WHERE datname = current_database()
			AND wait_event_type = 'Lock'
			AND wait_event = $1
			AND $2 = ANY(pg_blocking_pids(pid))
		ORDER BY pid
		LIMIT 1
	`, waitEvent, blockerPID).Scan(&pid)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return pid, err == nil, err
}
