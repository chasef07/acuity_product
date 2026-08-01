package humancalling_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestExpiredHandoffCreatesOneVoicemailTaskBeforeAudioCopy(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	provider := &recordingProvider{}
	audio := newVoicemailFixture()
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
		HandoffSIPDomain:    "synthetic.sip.telnyx.com",
		HandoffTokenKey:     []byte("0123456789abcdef0123456789abcdef"),
		VoicemailStore:      audio,
		RecordingDownloader: audio,
		PlaybackSigningKey:  []byte("abcdef0123456789abcdef0123456789"),
	}, func() time.Time { return now })
	const customGreeting = "Thank you for calling. Please leave a message after the tone."
	if err := calling.ProvisionLocationVoices(
		context.Background(),
		[]humancalling.LocationVoiceProvision{{
			PracticeKey:       "synthetic-practice",
			LocationKey:       "synthetic-location",
			Number:            "+14843336938",
			Enabled:           true,
			VoicemailGreeting: customGreeting,
		}},
	); err != nil {
		t.Fatalf("provision custom voicemail greeting: %v", err)
	}
	handoff, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-voicemail",
			PracticeID: authorization.Practice.ID,
		},
		LocationID:     authorization.Locations[0].ID,
		SourceCallID:   "voicemail-source",
		IdempotencyKey: "voicemail-handoff",
		Contact: humancalling.ContactContext{
			Phone:       "+15555550100",
			DisplayName: "Synthetic Caller",
			NameSource:  "Abita",
		},
	})
	if err != nil {
		t.Fatalf("create voicemail handoff: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "voicemail-inbound",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		CallControlID: "voicemail-caller-control",
		CallLegID:     "voicemail-caller-leg",
		CallSessionID: "voicemail-session",
		From:          "+15555550100",
		To:            "+14843336938",
	}); err != nil {
		t.Fatalf("admit voicemail caller: %v", err)
	}

	now = now.Add(21 * time.Second)
	if count, err := calling.ExpireOffers(context.Background()); err != nil || count != 1 {
		t.Fatalf("expire handoff offer: count=%d err=%v", count, err)
	}
	processCallingCommands(t, calling)
	greeting := provider.last(humancalling.CommandPlayVoicemailGreeting)
	if greeting.TargetID != "voicemail-caller-control" ||
		greeting.Payload["greeting"] != customGreeting {
		t.Fatalf("voicemail greeting command = %#v", greeting)
	}
	if provider.count(humancalling.CommandStartVoicemailRecording) != 0 {
		t.Fatal("recording started before the greeting completed")
	}

	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "voicemail-greeting-ended",
		Type:          humancalling.FactSpeakEnded,
		OccurredAt:    now.Add(time.Second),
		CallControlID: "voicemail-caller-control",
		CallLegID:     "voicemail-caller-leg",
		CallSessionID: "voicemail-session",
		ClientState:   stringPayload(greeting.Payload, "client_state"),
	}); err != nil {
		t.Fatalf("complete voicemail greeting: %v", err)
	}
	processCallingCommands(t, calling)
	recording := provider.last(humancalling.CommandStartVoicemailRecording)
	if payloadInteger(recording.Payload, "max_length") != 120 ||
		recording.Payload["transcription"] != false ||
		recording.Payload["recording_track"] != "inbound" {
		t.Fatalf("voicemail recording command = %#v", recording)
	}
	callID := handoffCallID(t, pool, handoff.ID)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "voicemail-caller-hangup",
		Type:          humancalling.FactCallHangup,
		OccurredAt:    now.Add(4 * time.Second),
		CallControlID: "voicemail-caller-control",
		CallLegID:     "voicemail-caller-leg",
		CallSessionID: "voicemail-session",
		HangupCause:   "normal_clearing",
	}); err != nil {
		t.Fatalf("apply voicemail caller hangup: %v", err)
	}
	var tasksBeforeArtifact int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM work_tasks WHERE call_id = $1
	`, callID).Scan(&tasksBeforeArtifact); err != nil {
		t.Fatalf("count Tasks before voicemail artifact: %v", err)
	}
	if tasksBeforeArtifact != 0 {
		t.Fatalf(
			"Tasks before delayed voicemail artifact = %d, want 0",
			tasksBeforeArtifact,
		)
	}

	startedAt := now.Add(2 * time.Second)
	endedAt := startedAt.Add(17 * time.Second)
	fact := humancalling.ProviderFact{
		EventID:            "voicemail-recording-saved",
		Type:               humancalling.FactRecordingSaved,
		OccurredAt:         endedAt,
		CallControlID:      "voicemail-caller-control",
		CallLegID:          "voicemail-caller-leg",
		CallSessionID:      "voicemail-session",
		ClientState:        stringPayload(recording.Payload, "client_state"),
		RecordingID:        "provider-recording-1",
		RecordingURL:       "https://provider.synthetic.test/recording-1.wav",
		RecordingStartedAt: startedAt,
		RecordingEndedAt:   endedAt,
	}
	if err := calling.ApplyProviderFact(context.Background(), fact); err != nil {
		t.Fatalf("apply voicemail recording: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), fact); err != nil {
		t.Fatalf("replay voicemail recording: %v", err)
	}

	call, err := calling.ReadCall(context.Background(), identity, callID)
	if err != nil {
		t.Fatalf("read voicemail Call: %v", err)
	}
	if call.State != humancalling.CallVoicemail ||
		call.Voicemail.Outcome != humancalling.RecoveryVoicemail ||
		call.Voicemail.AudioState != humancalling.VoicemailProcessing ||
		call.Voicemail.TaskID == "" ||
		call.Voicemail.DurationSeconds != 17 {
		t.Fatalf("voicemail Call = %#v", call)
	}
	taskModule := work.New(pool, accessModule, nil)
	task, err := taskModule.ReadTask(
		context.Background(),
		identity,
		call.Voicemail.TaskID,
	)
	if err != nil {
		t.Fatalf("read voicemail Task: %v", err)
	}
	if task.Title != "Review voicemail" ||
		task.Origin != work.TaskOriginVoicemail ||
		task.State != work.TaskOpen ||
		task.CallID != call.ID {
		t.Fatalf("voicemail Task = %#v", task)
	}

	if processed, err := calling.ProcessNextVoicemailCopy(context.Background()); err != nil || !processed {
		t.Fatalf("copy voicemail: processed=%t err=%v", processed, err)
	}
	call, err = calling.ReadCall(context.Background(), identity, call.ID)
	if err != nil {
		t.Fatalf("read copied voicemail Call: %v", err)
	}
	if call.Voicemail.AudioState != humancalling.VoicemailReady || audio.puts != 1 {
		t.Fatalf("copied voicemail = %#v, puts=%d", call.Voicemail, audio.puts)
	}
	completedTask, err := taskModule.CompleteTask(
		context.Background(),
		work.CompleteTaskCommand{
			Identity:        identity,
			TaskID:          task.ID,
			ExpectedVersion: task.Version,
		},
	)
	if err != nil || completedTask.State != work.TaskCompleted {
		t.Fatalf(
			"complete voicemail Task before playback: %#v, err=%v",
			completedTask,
			err,
		)
	}
	capability, err := calling.IssueVoicemailPlayback(
		context.Background(),
		identity,
		call.ID,
	)
	if err != nil {
		t.Fatalf("issue voicemail playback: %v", err)
	}
	content, err := calling.OpenVoicemailPlayback(
		context.Background(),
		identity,
		capability.Token,
	)
	if err != nil || string(content.Content) != "synthetic voicemail" {
		t.Fatalf("open voicemail playback: content=%q err=%v", content.Content, err)
	}

	now = now.Add(time.Minute)
	unavailableHandoff, err := calling.CreateHandoff(
		context.Background(),
		humancalling.CreateHandoffCommand{
			Service: humancalling.ServiceIdentity{
				Subject:    "abita-voicemail",
				PracticeID: authorization.Practice.ID,
			},
			LocationID:     authorization.Locations[0].ID,
			SourceCallID:   "unavailable-source",
			IdempotencyKey: "unavailable-handoff",
			Contact: humancalling.ContactContext{
				Phone: "+15555550101",
			},
		},
	)
	if err != nil {
		t.Fatalf("create unavailable voicemail handoff: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "unavailable-inbound",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		CallControlID: "unavailable-caller-control",
		CallLegID:     "unavailable-caller-leg",
		CallSessionID: "unavailable-session",
		From:          "+15555550101",
		To:            "+14843336938",
	}); err != nil {
		t.Fatalf("admit unavailable voicemail caller: %v", err)
	}
	now = now.Add(21 * time.Second)
	if _, err := calling.ExpireOffers(context.Background()); err != nil {
		t.Fatalf("expire unavailable voicemail offer: %v", err)
	}
	processCallingCommands(t, calling)
	unavailableGreeting := provider.last(
		humancalling.CommandPlayVoicemailGreeting,
	)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID: "unavailable-greeting-ended",
		// Accept playback completions emitted by commands from the previous revision.
		Type:          humancalling.FactPlaybackEnded,
		OccurredAt:    now,
		CallControlID: "unavailable-caller-control",
		CallLegID:     "unavailable-caller-leg",
		CallSessionID: "unavailable-session",
		ClientState: stringPayload(
			unavailableGreeting.Payload,
			"client_state",
		),
	}); err != nil {
		t.Fatalf("complete unavailable greeting: %v", err)
	}
	processCallingCommands(t, calling)
	unavailableRecording := provider.last(
		humancalling.CommandStartVoicemailRecording,
	)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:            "unavailable-recording-saved",
		Type:               humancalling.FactRecordingSaved,
		OccurredAt:         now.Add(5 * time.Second),
		CallControlID:      "unavailable-caller-control",
		CallLegID:          "unavailable-caller-leg",
		CallSessionID:      "unavailable-session",
		ClientState:        stringPayload(unavailableRecording.Payload, "client_state"),
		RecordingID:        "provider-recording-unavailable",
		RecordingURL:       "https://provider.synthetic.test/unavailable.wav",
		RecordingStartedAt: now,
		RecordingEndedAt:   now.Add(5 * time.Second),
	}); err != nil {
		t.Fatalf("save unavailable voicemail: %v", err)
	}
	audio.downloadErr = errors.New("synthetic copy failure")
	for attempt := 1; attempt <= 3; attempt++ {
		if processed, err := calling.ProcessNextVoicemailCopy(
			context.Background(),
		); err != nil || !processed {
			t.Fatalf(
				"fail voicemail copy attempt %d: processed=%t err=%v",
				attempt,
				processed,
				err,
			)
		}
		now = now.Add(time.Duration(attempt) * time.Minute)
	}
	unavailableCall, err := calling.ReadCall(
		context.Background(),
		identity,
		handoffCallID(t, pool, unavailableHandoff.ID),
	)
	if err != nil ||
		unavailableCall.Voicemail.AudioState != humancalling.VoicemailUnavailable {
		t.Fatalf("unavailable voicemail Call = %#v, err=%v", unavailableCall, err)
	}
	var unavailableTasks int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM work_tasks
		WHERE call_id = $1
	`, unavailableCall.ID).Scan(&unavailableTasks); err != nil {
		t.Fatalf("count unavailable recovery Tasks: %v", err)
	}
	if unavailableTasks != 1 {
		t.Fatalf("unavailable recovery Tasks = %d, want 1", unavailableTasks)
	}

	revokedCapability, err := calling.IssueVoicemailPlayback(
		context.Background(),
		identity,
		call.ID,
	)
	if err != nil {
		t.Fatalf("issue capability before revocation: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE access_memberships
		SET revoked_at = $2
		WHERE id = $1
	`, authorization.Membership.ID, now); err != nil {
		t.Fatalf("revoke voicemail Location access: %v", err)
	}
	if _, err := calling.OpenVoicemailPlayback(
		context.Background(),
		identity,
		revokedCapability.Token,
	); !errors.Is(err, humancalling.ErrDenied) {
		t.Fatalf("revoked playback error = %v, want denied", err)
	}
}

func TestExpiredHandoffWithoutRecordingCreatesOneMissedCallTask(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 30, 13, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	calling := humancalling.New(pool, accessModule, &recordingProvider{}, humancalling.Config{
		HandoffSIPDomain: "synthetic.sip.telnyx.com",
		HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
	}, func() time.Time { return now })
	handoff, err := calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "abita-missed",
			PracticeID: authorization.Practice.ID,
		},
		LocationID:     authorization.Locations[0].ID,
		SourceCallID:   "missed-source",
		IdempotencyKey: "missed-handoff",
		Contact:        humancalling.ContactContext{Phone: "+15555550100"},
	})
	if err != nil {
		t.Fatalf("create missed-call handoff: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "missed-inbound",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		CallControlID: "missed-control",
		CallLegID:     "missed-leg",
		CallSessionID: "missed-session",
		From:          "+15555550100",
		To:            "+14843336938",
	}); err != nil {
		t.Fatalf("admit missed caller: %v", err)
	}
	now = now.Add(21 * time.Second)
	if _, err := calling.ExpireOffers(context.Background()); err != nil {
		t.Fatalf("expire missed-call offer: %v", err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "missed-hangup",
		Type:          humancalling.FactCallHangup,
		OccurredAt:    now.Add(5 * time.Second),
		CallControlID: "missed-control",
		CallSessionID: "missed-session",
		HangupCause:   "normal_clearing",
	}); err != nil {
		t.Fatalf("apply missed-call hangup: %v", err)
	}
	call, err := calling.ReadCall(context.Background(), identity, handoffCallID(t, pool, handoff.ID))
	if err != nil {
		t.Fatalf("read missed Call: %v", err)
	}
	if call.State != humancalling.CallMissed ||
		call.Voicemail.Outcome != humancalling.RecoveryMissedCall ||
		call.Voicemail.AudioState != "" {
		t.Fatalf("missed Call = %#v", call)
	}
	task, err := work.New(pool, accessModule, nil).ReadTask(
		context.Background(),
		identity,
		call.Voicemail.TaskID,
	)
	if err != nil {
		t.Fatalf("read missed-call Task: %v", err)
	}
	if task.Title != "Return missed call" ||
		task.Origin != work.TaskOriginMissedCall ||
		task.State != work.TaskOpen {
		t.Fatalf("missed-call Task = %#v", task)
	}
}

func TestReorderedRecordingErrorDoesNotBeatSavedVoicemailArtifact(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 30, 13, 30, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	provider := &recordingProvider{}
	calling := humancalling.New(
		pool,
		accessModule,
		provider,
		humancalling.Config{
			HandoffSIPDomain: "synthetic.sip.telnyx.com",
			HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		},
		func() time.Time { return now },
	)
	callID, recording := prepareVoicemailRecording(
		t,
		calling,
		provider,
		pool,
		authorization,
		&now,
		"reordered",
	)
	clientState := stringPayload(recording.Payload, "client_state")
	recordingStartedAt := now
	recordingSavedAt := now.Add(2 * time.Second)
	if err := calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:       "reordered-mismatched-recording-error",
			Type:          humancalling.FactRecordingError,
			OccurredAt:    now,
			CallControlID: "reordered-caller-control",
			CallLegID:     "wrong-caller-leg",
			CallSessionID: "reordered-session",
			ClientState:   clientState,
		},
	); err != nil {
		t.Fatalf("apply mismatched recording error: %v", err)
	}
	if err := calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:            "reordered-mismatched-recording-saved",
			Type:               humancalling.FactRecordingSaved,
			OccurredAt:         recordingSavedAt,
			CallControlID:      "reordered-caller-control",
			CallLegID:          "reordered-caller-leg",
			CallSessionID:      "wrong-session",
			ClientState:        clientState,
			RecordingID:        "mismatched-recording",
			RecordingURL:       "https://provider.synthetic.test/mismatched.wav",
			RecordingStartedAt: recordingStartedAt,
			RecordingEndedAt:   recordingSavedAt,
		},
	); err != nil {
		t.Fatalf("apply mismatched saved artifact: %v", err)
	}
	var unmatchedState string
	if err := pool.QueryRow(context.Background(), `
		SELECT state
		FROM human_calling_calls
		WHERE id = $1
	`, callID).Scan(&unmatchedState); err != nil {
		t.Fatalf("read Call after mismatched voicemail facts: %v", err)
	}
	if unmatchedState != string(humancalling.CallUnanswered) {
		t.Fatalf("mismatched voicemail fact changed Call to %s", unmatchedState)
	}
	if err := calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:       "reordered-recording-error",
			Type:          humancalling.FactRecordingError,
			OccurredAt:    now.Add(3 * time.Second),
			CallControlID: "reordered-caller-control",
			CallLegID:     "reordered-caller-leg",
			CallSessionID: "reordered-session",
			ClientState:   clientState,
		},
	); err != nil {
		t.Fatalf("apply early recording error: %v", err)
	}
	var tasksAfterError int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM work_tasks WHERE call_id = $1
	`, callID).Scan(&tasksAfterError); err != nil {
		t.Fatalf("count recovery Tasks after early error: %v", err)
	}
	if tasksAfterError != 0 {
		t.Fatalf("recovery Tasks before reordering grace = %d", tasksAfterError)
	}
	now = now.Add(10 * time.Second)
	if err := calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:            "reordered-recording-saved",
			Type:               humancalling.FactRecordingSaved,
			OccurredAt:         recordingSavedAt,
			CallControlID:      "reordered-caller-control",
			CallLegID:          "reordered-caller-leg",
			CallSessionID:      "reordered-session",
			ClientState:        clientState,
			RecordingID:        "reordered-recording",
			RecordingURL:       "https://provider.synthetic.test/reordered.wav",
			RecordingStartedAt: recordingStartedAt,
			RecordingEndedAt:   recordingSavedAt,
		},
	); err != nil {
		t.Fatalf("apply reordered saved artifact: %v", err)
	}
	call, err := calling.ReadCall(context.Background(), identity, callID)
	if err != nil ||
		call.State != humancalling.CallVoicemail ||
		call.Voicemail.Outcome != humancalling.RecoveryVoicemail {
		t.Fatalf("reordered voicemail Call = %#v, err=%v", call, err)
	}
	now = now.Add(6 * time.Second)
	if expired, err := calling.ExpireVoicemailFailures(
		context.Background(),
	); err != nil || expired != 0 {
		t.Fatalf("superseded recording error expiry = %d, err=%v", expired, err)
	}
	var tasks int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM work_tasks WHERE call_id = $1
	`, callID).Scan(&tasks); err != nil {
		t.Fatalf("count reordered recovery Tasks: %v", err)
	}
	if tasks != 1 {
		t.Fatalf("reordered recovery Tasks = %d, want 1", tasks)
	}
	missedCallID, missedRecording := prepareVoicemailRecording(
		t,
		calling,
		provider,
		pool,
		authorization,
		&now,
		"recording-error",
	)
	if err := calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:       "recording-error-only",
			Type:          humancalling.FactRecordingError,
			OccurredAt:    now,
			CallControlID: "recording-error-caller-control",
			CallLegID:     "recording-error-caller-leg",
			CallSessionID: "recording-error-session",
			ClientState: stringPayload(
				missedRecording.Payload,
				"client_state",
			),
		},
	); err != nil {
		t.Fatalf("apply terminal recording error: %v", err)
	}
	now = now.Add(151 * time.Second)
	if expired, err := calling.ExpireVoicemailFailures(
		context.Background(),
	); err != nil || expired != 1 {
		t.Fatalf("expire terminal recording error = %d, err=%v", expired, err)
	}
	missedCall, err := calling.ReadCall(
		context.Background(),
		identity,
		missedCallID,
	)
	if err != nil ||
		missedCall.State != humancalling.CallMissed ||
		missedCall.Voicemail.Outcome != humancalling.RecoveryMissedCall {
		t.Fatalf("terminal recording error Call = %#v, err=%v", missedCall, err)
	}
	silentCallID, _ := prepareVoicemailRecording(
		t,
		calling,
		provider,
		pool,
		authorization,
		&now,
		"silent-recording",
	)
	now = now.Add(151 * time.Second)
	if expired, err := calling.ExpireVoicemailFailures(
		context.Background(),
	); err != nil || expired != 1 {
		t.Fatalf("expire silent recording callback = %d, err=%v", expired, err)
	}
	silentCall, err := calling.ReadCall(
		context.Background(),
		identity,
		silentCallID,
	)
	if err != nil ||
		silentCall.State != humancalling.CallMissed ||
		silentCall.Voicemail.Outcome != humancalling.RecoveryMissedCall ||
		silentCall.Voicemail.TaskID == "" {
		t.Fatalf("silent recording callback Call = %#v, err=%v", silentCall, err)
	}
}

func TestDefinitiveVoicemailRecordingRejectionCreatesMissedCallTask(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 30, 13, 45, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	provider := &recordingProvider{
		recordingError: fmt.Errorf(
			"%w: synthetic recording rejection",
			humancalling.ErrDefinitiveProviderFailure,
		),
	}
	calling := humancalling.New(
		pool,
		accessModule,
		provider,
		humancalling.Config{
			HandoffSIPDomain: "synthetic.sip.telnyx.com",
			HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
		},
		func() time.Time { return now },
	)
	callID, _ := prepareVoicemailRecording(
		t,
		calling,
		provider,
		pool,
		authorization,
		&now,
		"recording-rejected",
	)
	call, err := calling.ReadCall(context.Background(), identity, callID)
	if err != nil ||
		call.State != humancalling.CallMissed ||
		call.Voicemail.Outcome != humancalling.RecoveryMissedCall ||
		call.Voicemail.TaskID == "" {
		t.Fatalf("rejected recording Call = %#v, err=%v", call, err)
	}
	var tasks int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM work_tasks WHERE call_id = $1
	`, callID).Scan(&tasks); err != nil {
		t.Fatalf("count rejected-recording recovery Tasks: %v", err)
	}
	if tasks != 1 {
		t.Fatalf("rejected-recording recovery Tasks = %d, want 1", tasks)
	}
}

func TestTaskOutboundDerivesRouteAndWaitsForStaffMediaAnswer(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 30, 14, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	provisioned, err := accessModule.Provision(
		context.Background(),
		access.Provisioning{
			Environment: "test",
			RequestedBy: "slice-6-outbound-test",
			Practices: []access.PracticeProvision{{
				Key:  "outbound-practice",
				Name: "Outbound Practice",
				Locations: []access.LocationProvision{{
					Key:            "outbound-location",
					Name:           "Outbound Location",
					AbitaOfficeKey: "outbound-office",
				}},
				Invitations: []access.InvitationProvision{{
					Key:           "outbound-staff",
					Email:         "outbound@synthetic.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(time.Hour),
				}},
			}},
		},
	)
	if err != nil {
		t.Fatalf("provision outbound staff: %v", err)
	}
	identity := access.Identity{
		Subject:       "outbound-staff-subject",
		Email:         "outbound@synthetic.test",
		EmailVerified: true,
	}
	authorization, err := accessModule.AcceptInvitation(
		context.Background(),
		identity,
		provisioned.Invitations[0].Token,
	)
	if err != nil {
		t.Fatalf("accept outbound invitation: %v", err)
	}
	taskModule := work.New(pool, accessModule, func() time.Time { return now })
	task, _, err := taskModule.CreateAITask(
		context.Background(),
		work.CreateAITaskCommand{
			Service: access.ServiceIdentity{
				Subject:       "abita-outbound",
				PracticeID:    authorization.Practice.ID,
				LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{
					access.ServiceCapabilityCreateTask,
				},
			},
			OfficeKey:      "outbound-office",
			OfficePhone:    "+15555550199",
			SourceCallID:   "outbound-source-call",
			IdempotencyKey: "outbound-task",
			Phone:          "+15555550100",
			Summary:        "Call the patient",
			Message:        "Synthetic outbound Task",
			Category:       work.TaskCategoryAppointments,
			Urgency:        work.TaskUrgencyNormal,
		},
	)
	if err != nil {
		t.Fatalf("create outbound Task: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_location_voice_numbers (
			practice_id,
			location_id,
			phone,
			enabled
		)
		VALUES ($1, $2, '+15555550155', true)
	`, authorization.Practice.ID, authorization.Locations[0].ID); err != nil {
		t.Fatalf("configure outbound caller ID: %v", err)
	}
	provider := &recordingProvider{}
	calling := humancalling.New(
		pool,
		accessModule,
		provider,
		humancalling.Config{
			HandoffSIPDomain:  "synthetic.sip.telnyx.com",
			StaffSIPDomain:    "sip.telnyx.com",
			HandoffTokenKey:   []byte("0123456789abcdef0123456789abcdef"),
			CallControlID:     "synthetic-call-control",
			ConnectionTimeout: 15 * time.Second,
			LeaseDuration:     30 * time.Second,
			ReadinessGrace:    15 * time.Second,
		},
		func() time.Time { return now },
	)
	prepareCredentials(t, calling)
	const sessionID = "outbound-browser"
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		sessionID,
		false,
	); err != nil {
		t.Fatalf("acquire outbound softphone: %v", err)
	}
	if _, err := calling.SetReadiness(
		context.Background(),
		ready(identity, sessionID),
	); err != nil {
		t.Fatalf("ready outbound softphone: %v", err)
	}
	for index, destination := range []string{
		"+1911",
		"+442071838750",
		"+19005550100",
		"+12115550100",
		"+15559760100",
	} {
		if _, err := calling.StartOutboundCall(
			context.Background(),
			humancalling.StartOutboundCallCommand{
				Identity:       identity,
				SessionID:      sessionID,
				IdempotencyKey: "prohibited-destination-" + string(rune('a'+index)),
				PracticeID:     authorization.Practice.ID,
				LocationID:     authorization.Locations[0].ID,
				Destination:    destination,
			},
		); !errors.Is(err, humancalling.ErrInvalidInput) {
			t.Fatalf("prohibited destination %q error = %v", destination, err)
		}
	}
	if provider.count(humancalling.CommandDialStaff) != 0 ||
		provider.count(humancalling.CommandDialDestination) != 0 {
		t.Fatalf("provider contacted for prohibited destination: %#v", provider.commands)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_location_voice_numbers
		SET enabled = false
		WHERE practice_id = $1 AND location_id = $2
	`, authorization.Practice.ID, authorization.Locations[0].ID); err != nil {
		t.Fatalf("disable outbound caller ID: %v", err)
	}
	if _, err := calling.StartOutboundCall(
		context.Background(),
		humancalling.StartOutboundCallCommand{
			Identity:       identity,
			SessionID:      sessionID,
			IdempotencyKey: "disabled-caller-id",
			PracticeID:     authorization.Practice.ID,
			LocationID:     authorization.Locations[0].ID,
			Destination:    "+15555550100",
		},
	); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("disabled caller ID error = %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_location_voice_numbers
		SET enabled = true
		WHERE practice_id = $1 AND location_id = $2
	`, authorization.Practice.ID, authorization.Locations[0].ID); err != nil {
		t.Fatalf("restore outbound caller ID: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_location_voice_numbers (
			practice_id,
			location_id,
			phone,
			enabled
		)
		VALUES ($1, $2, '+15555550156', true)
	`, authorization.Practice.ID, authorization.Locations[0].ID); err != nil {
		t.Fatalf("make outbound caller ID ambiguous: %v", err)
	}
	if _, err := calling.StartOutboundCall(
		context.Background(),
		humancalling.StartOutboundCallCommand{
			Identity:       identity,
			SessionID:      sessionID,
			IdempotencyKey: "ambiguous-caller-id",
			PracticeID:     authorization.Practice.ID,
			LocationID:     authorization.Locations[0].ID,
			Destination:    "+15555550100",
		},
	); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("ambiguous caller ID error = %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		DELETE FROM human_calling_location_voice_numbers
		WHERE practice_id = $1
			AND location_id = $2
			AND phone = '+15555550156'
	`, authorization.Practice.ID, authorization.Locations[0].ID); err != nil {
		t.Fatalf("restore one outbound caller ID: %v", err)
	}
	if provider.count(humancalling.CommandDialStaff) != 0 ||
		provider.count(humancalling.CommandDialDestination) != 0 {
		t.Fatalf("provider contacted for caller ID configuration: %#v", provider.commands)
	}
	start := humancalling.StartOutboundCallCommand{
		Identity:       identity,
		SessionID:      sessionID,
		IdempotencyKey: "task-outbound-attempt-1",
		TaskID:         task.ID,
	}
	var concurrentCalls [2]humancalling.Call
	var concurrentErrors [2]error
	startGate := make(chan struct{})
	var starts sync.WaitGroup
	for index := range concurrentCalls {
		starts.Add(1)
		go func() {
			defer starts.Done()
			<-startGate
			concurrentCalls[index], concurrentErrors[index] =
				calling.StartOutboundCall(context.Background(), start)
		}()
	}
	close(startGate)
	starts.Wait()
	for index, err := range concurrentErrors {
		if err != nil {
			t.Fatalf("concurrent Task outbound Call %d: %v", index, err)
		}
	}
	call := concurrentCalls[0]
	replayed := concurrentCalls[1]
	if replayed.ID != call.ID {
		t.Fatalf("concurrent idempotent Calls = %s and %s", call.ID, replayed.ID)
	}
	if call.State != humancalling.CallPreparing ||
		call.Direction != humancalling.CallOutbound ||
		call.EntryPoint != humancalling.CallEntryTask ||
		call.TaskID != task.ID ||
		call.LocationID != task.LocationID ||
		call.Phone != task.Phone ||
		call.CallerID != "+15555550155" {
		t.Fatalf("prepared Task outbound Call = %#v", call)
	}
	var outboundRows, staffIntents int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			count(DISTINCT call.id),
			count(command.id) FILTER (WHERE command.action = 'DIAL_STAFF')
		FROM human_calling_calls call
		LEFT JOIN human_calling_provider_commands command
			ON command.call_id = call.id
		WHERE call.initiating_subject = $1
			AND call.outbound_idempotency_key = $2
	`, identity.Subject, start.IdempotencyKey).Scan(
		&outboundRows,
		&staffIntents,
	); err != nil {
		t.Fatalf("count concurrent outbound effects: %v", err)
	}
	if outboundRows != 1 || staffIntents != 1 {
		t.Fatalf(
			"concurrent outbound effects: Calls=%d DIAL_STAFF=%d",
			outboundRows,
			staffIntents,
		)
	}
	if provider.count(humancalling.CommandDialStaff) != 0 ||
		provider.count(humancalling.CommandDialDestination) != 0 {
		t.Fatalf("provider contacted before worker claim: %#v", provider.commands)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("prepare outbound media: processed=%t err=%v", processed, err)
	}
	if provider.count(humancalling.CommandDialStaff) != 1 ||
		provider.count(humancalling.CommandDialDestination) != 0 {
		t.Fatalf("outbound media preparation commands = %#v", provider.commands)
	}
	staffDial := provider.last(humancalling.CommandDialStaff)
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "outbound-staff-answered",
		Type:          humancalling.FactCallAnswered,
		OccurredAt:    now.Add(time.Second),
		CallControlID: "staff-control-1",
		CallLegID:     "staff-leg-1",
		CallSessionID: "outbound-session",
		ClientState:   stringPayload(staffDial.Payload, "client_state"),
	}); err != nil {
		t.Fatalf("apply outbound staff answer: %v", err)
	}
	answered, err := calling.ReadCall(context.Background(), identity, call.ID)
	if err != nil || answered.State != humancalling.CallPreparing {
		t.Fatalf("staff answer before browser readiness = %#v, err=%v", answered, err)
	}
	var destinationIntents int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM human_calling_provider_commands
		WHERE call_id = $1 AND action = 'DIAL_DESTINATION'
	`, call.ID).Scan(&destinationIntents); err != nil {
		t.Fatalf("count destination intents before browser readiness: %v", err)
	}
	if destinationIntents != 0 {
		t.Fatalf("destination intents before browser readiness = %d", destinationIntents)
	}
	if _, err := calling.ConfirmOutboundMedia(
		context.Background(),
		humancalling.ConfirmOutboundMediaCommand{
			Identity:          identity,
			SessionID:         sessionID,
			CallID:            call.ID,
			MediaToken:        answered.ExpectedMediaToken,
			ProviderSessionID: "unrelated-session",
		},
	); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("unrelated staff media session error = %v", err)
	}
	ringing, err := calling.ConfirmOutboundMedia(
		context.Background(),
		humancalling.ConfirmOutboundMediaCommand{
			Identity:          identity,
			SessionID:         sessionID,
			CallID:            call.ID,
			MediaToken:        answered.ExpectedMediaToken,
			ProviderSessionID: "outbound-session",
		},
	)
	if err != nil || ringing.State != humancalling.CallRinging {
		t.Fatalf("outbound Ringing Call = %#v, err=%v", ringing, err)
	}
	reconfirmed, err := calling.ConfirmOutboundMedia(
		context.Background(),
		humancalling.ConfirmOutboundMediaCommand{
			Identity:          identity,
			SessionID:         sessionID,
			CallID:            call.ID,
			MediaToken:        answered.ExpectedMediaToken,
			ProviderSessionID: "outbound-session",
		},
	)
	if err != nil || reconfirmed.State != humancalling.CallRinging {
		t.Fatalf("idempotent Ringing confirmation = %#v, err=%v", reconfirmed, err)
	}
	if provider.count(humancalling.CommandDialDestination) != 0 {
		t.Fatalf("destination command escaped durable worker: %#v", provider.commands)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("dial outbound destination: processed=%t err=%v", processed, err)
	}
	destinationDial := provider.last(humancalling.CommandDialDestination)
	if destinationDial.Payload["to"] != task.Phone ||
		destinationDial.Payload["from"] != "+15555550155" ||
		destinationDial.Payload["link_to"] != "staff-control-1" ||
		destinationDial.Payload["answering_machine_detection"] != "disabled" ||
		payloadInteger(destinationDial.Payload, "timeout_secs") != 30 {
		t.Fatalf("destination Dial route = %#v", destinationDial)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "outbound-destination-bridged",
		Type:          humancalling.FactCallBridged,
		OccurredAt:    now.Add(2 * time.Second),
		CallControlID: "destination-control-1",
		CallLegID:     "destination-leg-1",
		CallSessionID: "outbound-session",
		ClientState: stringPayload(
			destinationDial.Payload,
			"client_state",
		),
	}); err != nil {
		t.Fatalf("bridge outbound destination: %v", err)
	}
	connected, err := calling.ReadCall(
		context.Background(),
		identity,
		call.ID,
	)
	if err != nil ||
		connected.State != humancalling.CallConnected ||
		connected.WinnerSubject != identity.Subject ||
		connected.Recording.State != "" {
		t.Fatalf("connected outbound Call = %#v, err=%v", connected, err)
	}
	reconfirmed, err = calling.ConfirmOutboundMedia(
		context.Background(),
		humancalling.ConfirmOutboundMediaCommand{
			Identity:          identity,
			SessionID:         sessionID,
			CallID:            call.ID,
			MediaToken:        answered.ExpectedMediaToken,
			ProviderSessionID: "outbound-session",
		},
	)
	if err != nil || reconfirmed.State != humancalling.CallConnected {
		t.Fatalf("idempotent Connected confirmation = %#v, err=%v", reconfirmed, err)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "outbound-destination-hangup",
		Type:          humancalling.FactCallHangup,
		OccurredAt:    now.Add(20 * time.Second),
		CallControlID: "destination-control-1",
		CallLegID:     "destination-leg-1",
		CallSessionID: "outbound-session",
		ClientState: stringPayload(
			destinationDial.Payload,
			"client_state",
		),
		HangupCause: "normal_clearing",
	}); err != nil {
		t.Fatalf("terminate outbound destination: %v", err)
	}
	ended, err := calling.ReadCall(context.Background(), identity, call.ID)
	if err != nil || ended.State != humancalling.CallNeedsDisposition {
		t.Fatalf("ended outbound Call = %#v, err=%v", ended, err)
	}
	dispositioned, err := calling.RecordDisposition(
		context.Background(),
		identity,
		sessionID,
		call.ID,
		humancalling.DispositionCompleteTask,
	)
	if err != nil ||
		dispositioned.Call.State != humancalling.CallResolved ||
		dispositioned.TaskID != task.ID {
		t.Fatalf(
			"complete outbound Task disposition = %#v, err=%v",
			dispositioned,
			err,
		)
	}
	completedTask, err := taskModule.ReadTask(
		context.Background(),
		identity,
		task.ID,
	)
	if err != nil || completedTask.State != work.TaskCompleted {
		t.Fatalf("completed outbound Task = %#v, err=%v", completedTask, err)
	}

	now = now.Add(time.Minute)
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		sessionID,
		false,
	); err != nil {
		t.Fatalf("renew outbound softphone: %v", err)
	}
	if _, err := calling.SetReadiness(
		context.Background(),
		ready(identity, sessionID),
	); err != nil {
		t.Fatalf("renew outbound readiness: %v", err)
	}
	timeoutCall, err := calling.StartOutboundCall(
		context.Background(),
		humancalling.StartOutboundCallCommand{
			Identity:       identity,
			SessionID:      sessionID,
			IdempotencyKey: "standalone-outbound-timeout",
			PracticeID:     authorization.Practice.ID,
			LocationID:     authorization.Locations[0].ID,
			Destination:    task.Phone,
		},
	)
	if err != nil {
		t.Fatalf("start standalone timeout Call: %v", err)
	}
	if timeoutCall.EntryPoint != humancalling.CallEntryStandalone ||
		timeoutCall.TaskID != "" ||
		timeoutCall.Phone != task.Phone {
		t.Fatalf("standalone matching Task remained independent = %#v", timeoutCall)
	}
	if processed, err := calling.ProcessNextCommand(
		context.Background(),
	); err != nil || !processed {
		t.Fatalf("prepare timeout media: processed=%t err=%v", processed, err)
	}
	timeoutStaffDial := provider.last(humancalling.CommandDialStaff)
	if err := calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:       "timeout-staff-answered",
			Type:          humancalling.FactCallAnswered,
			OccurredAt:    now.Add(time.Second),
			CallControlID: "staff-control-1",
			CallLegID:     "staff-leg-1",
			CallSessionID: "timeout-session",
			ClientState: stringPayload(
				timeoutStaffDial.Payload,
				"client_state",
			),
		},
	); err != nil {
		t.Fatalf("answer timeout staff leg: %v", err)
	}
	timeoutAnswered, err := calling.ReadCall(
		context.Background(),
		identity,
		timeoutCall.ID,
	)
	if err != nil {
		t.Fatalf("read answered timeout staff leg: %v", err)
	}
	if _, err := calling.ConfirmOutboundMedia(
		context.Background(),
		humancalling.ConfirmOutboundMediaCommand{
			Identity:          identity,
			SessionID:         sessionID,
			CallID:            timeoutCall.ID,
			MediaToken:        timeoutAnswered.ExpectedMediaToken,
			ProviderSessionID: "timeout-session",
		},
	); err != nil {
		t.Fatalf("confirm timeout staff media: %v", err)
	}
	provider.destinationResults = []humancalling.ProviderResult{{
		CallControlID: "timeout-destination-control",
		CallLegID:     "timeout-destination-leg",
	}}
	if processed, err := calling.ProcessNextCommand(
		context.Background(),
	); err != nil || !processed {
		t.Fatalf("dial timeout destination: processed=%t err=%v", processed, err)
	}
	timeoutDestinationDial := provider.last(
		humancalling.CommandDialDestination,
	)
	now = now.Add(31 * time.Second)
	if expired, err := calling.ExpireConnections(
		context.Background(),
	); err != nil || expired != 1 {
		t.Fatalf("expire outbound ringing: expired=%d err=%v", expired, err)
	}
	unknown, err := calling.ReadCall(
		context.Background(),
		identity,
		timeoutCall.ID,
	)
	if err != nil ||
		unknown.State != humancalling.CallReconciling ||
		!unknown.MediaReady ||
		unknown.ProviderTermination != "STATUS_UNKNOWN" {
		t.Fatalf("outbound timeout reconciliation = %#v, err=%v", unknown, err)
	}
	if err := calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:       "timeout-late-staff-initiated",
			Type:          humancalling.FactCallInitiated,
			OccurredAt:    now,
			CallControlID: "staff-control-1",
			CallLegID:     "staff-leg-1",
			CallSessionID: "timeout-session",
			ClientState: stringPayload(
				timeoutStaffDial.Payload,
				"client_state",
			),
		},
	); err != nil {
		t.Fatalf("apply late timeout staff initiation: %v", err)
	}
	unknown, err = calling.ReadCall(context.Background(), identity, timeoutCall.ID)
	if err != nil ||
		unknown.State != humancalling.CallReconciling ||
		!unknown.MediaReady ||
		unknown.ProviderTermination != "STATUS_UNKNOWN" {
		t.Fatalf("late staff fact preserved timeout reconciliation = %#v, err=%v", unknown, err)
	}
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		sessionID,
		false,
	); err != nil {
		t.Fatalf("renew softphone after outbound timeout: %v", err)
	}
	reconfirmed, err = calling.ConfirmOutboundMedia(
		context.Background(),
		humancalling.ConfirmOutboundMediaCommand{
			Identity:          identity,
			SessionID:         sessionID,
			CallID:            timeoutCall.ID,
			MediaToken:        timeoutAnswered.ExpectedMediaToken,
			ProviderSessionID: "timeout-session",
		},
	)
	if err != nil || reconfirmed.State != humancalling.CallReconciling {
		t.Fatalf("idempotent reconciliation confirmation = %#v, err=%v", reconfirmed, err)
	}
	var timeoutDestinationIntents int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM human_calling_provider_commands
		WHERE call_id = $1 AND action = 'DIAL_DESTINATION'
	`, timeoutCall.ID).Scan(&timeoutDestinationIntents); err != nil {
		t.Fatalf("count timeout destination intents: %v", err)
	}
	if timeoutDestinationIntents != 1 {
		t.Fatalf("timeout destination intents after reconfirmation = %d", timeoutDestinationIntents)
	}
	var hangupIntents int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM human_calling_provider_commands
		WHERE call_id = $1
			AND action = 'HANGUP'
			AND state = 'PENDING'
	`, timeoutCall.ID).Scan(&hangupIntents); err != nil {
		t.Fatalf("read timeout Hangup intents: %v", err)
	}
	if hangupIntents != 2 {
		t.Fatalf("timeout Hangup intents = %d, want 2", hangupIntents)
	}
	if err := calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:       "timeout-provider-no-answer",
			Type:          humancalling.FactCallHangup,
			OccurredAt:    now,
			CallControlID: "timeout-destination-control",
			CallLegID:     "timeout-destination-leg",
			CallSessionID: "timeout-session",
			ClientState: stringPayload(
				timeoutDestinationDial.Payload,
				"client_state",
			),
			HangupCause: "no_answer",
		},
	); err != nil {
		t.Fatalf("apply provider no-answer receipt: %v", err)
	}
	providerBacked, err := calling.ReadCall(
		context.Background(),
		identity,
		timeoutCall.ID,
	)
	if err != nil ||
		providerBacked.State != humancalling.CallNeedsDisposition ||
		providerBacked.ProviderTermination != "NO_ANSWER" {
		t.Fatalf("provider-backed no-answer = %#v, err=%v", providerBacked, err)
	}
	if _, err := calling.AcquireSoftphone(
		context.Background(),
		identity,
		sessionID,
		false,
	); err != nil {
		t.Fatalf("renew lease for standalone disposition: %v", err)
	}
	if _, err := calling.SetReadiness(
		context.Background(),
		ready(identity, sessionID),
	); err != nil {
		t.Fatalf("renew readiness for standalone disposition: %v", err)
	}
	created, err := calling.RecordDisposition(
		context.Background(),
		identity,
		sessionID,
		timeoutCall.ID,
		humancalling.DispositionCreateTask,
	)
	if err != nil ||
		created.Call.State != humancalling.CallResolved ||
		created.TaskID == "" {
		t.Fatalf("standalone Create Task disposition = %#v, err=%v", created, err)
	}
	replayedDisposition, err := calling.RecordDisposition(
		context.Background(),
		identity,
		sessionID,
		timeoutCall.ID,
		humancalling.DispositionCreateTask,
	)
	if err != nil || replayedDisposition.TaskID != created.TaskID {
		t.Fatalf(
			"replayed standalone disposition = %#v, err=%v",
			replayedDisposition,
			err,
		)
	}
	var followUpTasks int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM work_tasks
		WHERE call_id = $1
	`, timeoutCall.ID).Scan(&followUpTasks); err != nil {
		t.Fatalf("count standalone follow-up Tasks: %v", err)
	}
	if followUpTasks != 1 {
		t.Fatalf("standalone follow-up Tasks = %d, want 1", followUpTasks)
	}
	for index, outcome := range []struct {
		cause string
		want  string
	}{
		{cause: "user_busy", want: "BUSY"},
		{cause: "call_rejected", want: "DECLINED"},
		{cause: "carrier_error", want: "FAILED"},
	} {
		proveProviderBackedOutcome(
			t,
			calling,
			provider,
			identity,
			sessionID,
			authorization.Practice.ID,
			authorization.Locations[0].ID,
			task.Phone,
			fmt.Sprintf("outcome-%d", index),
			outcome.cause,
			outcome.want,
			now,
		)
	}
	for _, test := range []struct {
		name      string
		dialError error
		wantState humancalling.CallState
	}{
		{
			name:      "preparing",
			wantState: humancalling.CallPreparing,
		},
		{
			name:      "reconciling",
			dialError: humancalling.ErrAmbiguousEffect,
			wantState: humancalling.CallReconciling,
		},
	} {
		processCallingCommands(t, calling)
		provider.dialError = test.dialError
		stalled, err := calling.StartOutboundCall(
			context.Background(),
			humancalling.StartOutboundCallCommand{
				Identity:       identity,
				SessionID:      sessionID,
				IdempotencyKey: "stalled-media-" + test.name,
				PracticeID:     authorization.Practice.ID,
				LocationID:     authorization.Locations[0].ID,
				Destination:    task.Phone,
			},
		)
		if err != nil {
			t.Fatalf("start stalled %s media Call: %v", test.name, err)
		}
		if processed, err := calling.ProcessNextCommand(
			context.Background(),
		); err != nil || !processed {
			t.Fatalf(
				"process stalled %s media Dial: processed=%t err=%v",
				test.name,
				processed,
				err,
			)
		}
		provider.dialError = nil
		stalled, err = calling.ReadCall(
			context.Background(),
			identity,
			stalled.ID,
		)
		if err != nil || stalled.State != test.wantState {
			t.Fatalf(
				"stalled %s media Call before expiry = %#v, err=%v",
				test.name,
				stalled,
				err,
			)
		}
		now = now.Add(16 * time.Second)
		if expired, err := calling.ExpireConnections(
			context.Background(),
		); err != nil || expired != 1 {
			t.Fatalf(
				"expire stalled %s media Call: expired=%d err=%v",
				test.name,
				expired,
				err,
			)
		}
		stalled, err = calling.ReadCall(
			context.Background(),
			identity,
			stalled.ID,
		)
		if err != nil ||
			stalled.State != humancalling.CallNeedsDisposition ||
			stalled.ProviderTermination != "MEDIA_READINESS_FAILED" {
			t.Fatalf(
				"expired %s media Call = %#v, err=%v",
				test.name,
				stalled,
				err,
			)
		}
		if _, err := calling.RecordDisposition(
			context.Background(),
			identity,
			sessionID,
			stalled.ID,
			humancalling.DispositionNoFollowUp,
		); err != nil {
			t.Fatalf("dispose stalled %s media Call: %v", test.name, err)
		}
		softphone, err := calling.AcquireSoftphone(
			context.Background(),
			identity,
			sessionID,
			false,
		)
		if err != nil || softphone.ActiveCallID != "" {
			t.Fatalf(
				"released %s media Call fence = %#v, err=%v",
				test.name,
				softphone,
				err,
			)
		}
		softphone, err = calling.SetReadiness(
			context.Background(),
			ready(identity, sessionID),
		)
		if err != nil || !softphone.Available {
			t.Fatalf(
				"restored %s media availability = %#v, err=%v",
				test.name,
				softphone,
				err,
			)
		}
	}
	provider.dialError = fmt.Errorf(
		"%w: synthetic staff media rejection",
		humancalling.ErrDefinitiveProviderFailure,
	)
	mediaFailure, err := calling.StartOutboundCall(
		context.Background(),
		humancalling.StartOutboundCallCommand{
			Identity:       identity,
			SessionID:      sessionID,
			IdempotencyKey: "standalone-media-failure",
			PracticeID:     authorization.Practice.ID,
			LocationID:     authorization.Locations[0].ID,
			Destination:    task.Phone,
		},
	)
	if err != nil {
		t.Fatalf("start standalone media failure Call: %v", err)
	}
	processCallingCommands(t, calling)
	provider.dialError = nil
	mediaFailure, err = calling.ReadCall(
		context.Background(),
		identity,
		mediaFailure.ID,
	)
	if err != nil ||
		mediaFailure.State != humancalling.CallNeedsDisposition ||
		mediaFailure.ProviderTermination != "MEDIA_READINESS_FAILED" {
		t.Fatalf("standalone media failure = %#v, err=%v", mediaFailure, err)
	}
	if _, err := calling.RecordDisposition(
		context.Background(),
		identity,
		sessionID,
		mediaFailure.ID,
		humancalling.DispositionNoFollowUp,
	); err != nil {
		t.Fatalf("dispose standalone media failure: %v", err)
	}
	retry, err := calling.StartOutboundCall(
		context.Background(),
		humancalling.StartOutboundCallCommand{
			Identity:       identity,
			SessionID:      sessionID,
			IdempotencyKey: "standalone-outbound-retry",
			PracticeID:     authorization.Practice.ID,
			LocationID:     authorization.Locations[0].ID,
			Destination:    task.Phone,
			RetryOfCallID:  timeoutCall.ID,
		},
	)
	if err != nil ||
		retry.ID == timeoutCall.ID ||
		retry.RetryOfCallID != timeoutCall.ID ||
		retry.State != humancalling.CallPreparing ||
		retry.EntryPoint != humancalling.CallEntryStandalone ||
		retry.TaskID != "" {
		t.Fatalf("explicit standalone retry = %#v, err=%v", retry, err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET
			state = 'SENDING',
			attempts = attempts + 1,
			updated_at = $2
		WHERE call_id = $1
			AND action = 'DIAL_STAFF'
			AND state = 'PENDING'
	`, retry.ID, now.Add(-61*time.Second)); err != nil {
		t.Fatalf("simulate interrupted outbound staff Dial: %v", err)
	}
	if err := calling.RecoverInterruptedCommands(context.Background()); err != nil {
		t.Fatalf("recover interrupted outbound staff Dial: %v", err)
	}
	unknownRetry, err := calling.ReadCall(
		context.Background(),
		identity,
		retry.ID,
	)
	if err != nil ||
		unknownRetry.State != humancalling.CallReconciling ||
		unknownRetry.ProviderTermination != "STATUS_UNKNOWN" {
		t.Fatalf("interrupted outbound Call = %#v, err=%v", unknownRetry, err)
	}
}

func proveProviderBackedOutcome(
	t *testing.T,
	calling *humancalling.Module,
	provider *recordingProvider,
	identity access.Identity,
	sessionID string,
	practiceID string,
	locationID string,
	destination string,
	suffix string,
	hangupCause string,
	want string,
	now time.Time,
) {
	t.Helper()
	staffControlID := suffix + "-staff-control"
	staffLegID := suffix + "-staff-leg"
	destinationControlID := suffix + "-destination-control"
	destinationLegID := suffix + "-destination-leg"
	callSessionID := suffix + "-session"
	provider.dialResults = append(
		provider.dialResults,
		humancalling.ProviderResult{
			CallControlID: staffControlID,
			CallLegID:     staffLegID,
		},
	)
	call, err := calling.StartOutboundCall(
		context.Background(),
		humancalling.StartOutboundCallCommand{
			Identity:       identity,
			SessionID:      sessionID,
			IdempotencyKey: suffix,
			PracticeID:     practiceID,
			LocationID:     locationID,
			Destination:    destination,
		},
	)
	if err != nil {
		t.Fatalf("start %s provider outcome Call: %v", want, err)
	}
	processCallingCommands(t, calling)
	staffDial := provider.last(humancalling.CommandDialStaff)
	if err := calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:       suffix + "-staff-answered",
			Type:          humancalling.FactCallAnswered,
			OccurredAt:    now,
			CallControlID: staffControlID,
			CallLegID:     staffLegID,
			CallSessionID: callSessionID,
			ClientState: stringPayload(
				staffDial.Payload,
				"client_state",
			),
		},
	); err != nil {
		t.Fatalf("answer %s staff leg: %v", want, err)
	}
	answered, err := calling.ReadCall(
		context.Background(),
		identity,
		call.ID,
	)
	if err != nil {
		t.Fatalf("read %s staff answer: %v", want, err)
	}
	if _, err := calling.ConfirmOutboundMedia(
		context.Background(),
		humancalling.ConfirmOutboundMediaCommand{
			Identity:          identity,
			SessionID:         sessionID,
			CallID:            call.ID,
			MediaToken:        answered.ExpectedMediaToken,
			ProviderSessionID: callSessionID,
		},
	); err != nil {
		t.Fatalf("confirm %s staff media: %v", want, err)
	}
	provider.destinationResults = append(
		provider.destinationResults,
		humancalling.ProviderResult{
			CallControlID: destinationControlID,
			CallLegID:     destinationLegID,
		},
	)
	processCallingCommands(t, calling)
	destinationDial := provider.last(humancalling.CommandDialDestination)
	if err := calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:       suffix + "-destination-hangup",
			Type:          humancalling.FactCallHangup,
			OccurredAt:    now,
			CallControlID: destinationControlID,
			CallLegID:     destinationLegID,
			CallSessionID: callSessionID,
			ClientState: stringPayload(
				destinationDial.Payload,
				"client_state",
			),
			HangupCause: hangupCause,
		},
	); err != nil {
		t.Fatalf("apply %s provider outcome: %v", want, err)
	}
	call, err = calling.ReadCall(context.Background(), identity, call.ID)
	if err != nil ||
		call.State != humancalling.CallNeedsDisposition ||
		call.ProviderTermination != want {
		t.Fatalf("provider-backed %s Call = %#v, err=%v", want, call, err)
	}
	if _, err := calling.RecordDisposition(
		context.Background(),
		identity,
		sessionID,
		call.ID,
		humancalling.DispositionNoFollowUp,
	); err != nil {
		t.Fatalf("dispose provider-backed %s Call: %v", want, err)
	}
}

func processCallingCommands(t *testing.T, calling *humancalling.Module) {
	t.Helper()
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process calling command: %v", err)
		}
		if !processed {
			return
		}
	}
}

func prepareVoicemailRecording(
	t *testing.T,
	calling *humancalling.Module,
	provider *recordingProvider,
	pool *pgxpool.Pool,
	authorization access.Authorization,
	now *time.Time,
	suffix string,
) (string, humancalling.ProviderCommand) {
	t.Helper()
	handoff, err := calling.CreateHandoff(
		context.Background(),
		humancalling.CreateHandoffCommand{
			Service: humancalling.ServiceIdentity{
				Subject:    "abita-" + suffix,
				PracticeID: authorization.Practice.ID,
			},
			LocationID:     authorization.Locations[0].ID,
			SourceCallID:   suffix + "-source",
			IdempotencyKey: suffix + "-handoff",
			Contact: humancalling.ContactContext{
				Phone: "+15555550100",
			},
		},
	)
	if err != nil {
		t.Fatalf("create %s voicemail handoff: %v", suffix, err)
	}
	controlID := suffix + "-caller-control"
	if err := calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:       suffix + "-inbound",
			Type:          humancalling.FactCallInitiated,
			OccurredAt:    *now,
			CallControlID: controlID,
			CallLegID:     suffix + "-caller-leg",
			CallSessionID: suffix + "-session",
			From:          "+15555550100",
			To:            "+14843336938",
		},
	); err != nil {
		t.Fatalf("admit %s voicemail caller: %v", suffix, err)
	}
	*now = (*now).Add(21 * time.Second)
	if _, err := calling.ExpireOffers(context.Background()); err != nil {
		t.Fatalf("expire %s voicemail offer: %v", suffix, err)
	}
	processCallingCommands(t, calling)
	greeting := provider.last(humancalling.CommandPlayVoicemailGreeting)
	recordingCommands := provider.count(
		humancalling.CommandStartVoicemailRecording,
	)
	if err := calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:       suffix + "-mismatched-greeting-ended",
			Type:          humancalling.FactSpeakEnded,
			OccurredAt:    *now,
			CallControlID: controlID,
			CallLegID:     "wrong-caller-leg",
			CallSessionID: suffix + "-session",
			ClientState: stringPayload(
				greeting.Payload,
				"client_state",
			),
		},
	); err != nil {
		t.Fatalf("apply mismatched %s greeting completion: %v", suffix, err)
	}
	processCallingCommands(t, calling)
	if provider.count(humancalling.CommandStartVoicemailRecording) !=
		recordingCommands {
		t.Fatalf("mismatched %s greeting started recording", suffix)
	}
	if err := calling.ApplyProviderFact(
		context.Background(),
		humancalling.ProviderFact{
			EventID:       suffix + "-greeting-ended",
			Type:          humancalling.FactSpeakEnded,
			OccurredAt:    *now,
			CallControlID: controlID,
			CallLegID:     suffix + "-caller-leg",
			CallSessionID: suffix + "-session",
			ClientState: stringPayload(
				greeting.Payload,
				"client_state",
			),
		},
	); err != nil {
		t.Fatalf("complete %s voicemail greeting: %v", suffix, err)
	}
	processed, err := calling.ProcessNextCommand(context.Background())
	if !processed {
		t.Fatalf("process %s voicemail recording command: no work", suffix)
	}
	if err != nil {
		t.Fatalf("process %s voicemail recording command: %v", suffix, err)
	}
	return handoffCallID(t, pool, handoff.ID), provider.last(
		humancalling.CommandStartVoicemailRecording,
	)
}

func handoffCallID(
	t *testing.T,
	pool *pgxpool.Pool,
	handoffID string,
) string {
	t.Helper()
	var callID string
	if err := pool.QueryRow(
		context.Background(),
		`SELECT id::text FROM human_calling_calls WHERE handoff_id = $1`,
		handoffID,
	).Scan(&callID); err != nil {
		t.Fatalf("read handoff Call: %v", err)
	}
	return callID
}

func stringPayload(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func payloadInteger(payload map[string]any, key string) int {
	switch value := payload[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
}

type voicemailFixture struct {
	mu          sync.Mutex
	objects     map[string][]byte
	puts        int
	downloadErr error
}

func newVoicemailFixture() *voicemailFixture {
	return &voicemailFixture{objects: map[string][]byte{}}
}

func (fixture *voicemailFixture) Download(
	_ context.Context,
	url string,
) ([]byte, string, error) {
	if fixture.downloadErr != nil {
		return nil, "", fixture.downloadErr
	}
	if url == "" {
		return nil, "", errors.New("missing provider recording URL")
	}
	return []byte("synthetic voicemail"), "audio/wav", nil
}

func (fixture *voicemailFixture) Put(
	_ context.Context,
	key string,
	value []byte,
) error {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.puts++
	fixture.objects[key] = append([]byte(nil), value...)
	return nil
}

func (fixture *voicemailFixture) Get(
	_ context.Context,
	key string,
) ([]byte, error) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	value, ok := fixture.objects[key]
	if !ok {
		return nil, errors.New("missing voicemail object")
	}
	return append([]byte(nil), value...), nil
}
