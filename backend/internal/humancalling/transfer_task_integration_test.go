package humancalling_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
)

func TestTransferredAITaskSurvivesMissedCallAndLateVoicemail(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	provider := &recordingProvider{}
	pool, calling, caller, staff := prepareInboundFanoutWithTask(t, now, "task-handoff", provider, 1, true)
	ctx := context.Background()
	var taskID, practiceID, locationID string
	if err := pool.QueryRow(ctx, `SELECT task_id::text, practice_id::text, location_id::text FROM human_calling_handoffs`).Scan(&taskID, &practiceID, &locationID); err != nil {
		t.Fatal(err)
	}
	handoff := humancalling.CreateHandoffCommand{
		Service:    access.ServiceIdentity{Subject: "abita-task-handoff", PracticeID: practiceID},
		LocationID: locationID, SourceCallID: "task-handoff-source", IdempotencyKey: "task-handoff-handoff",
		TaskID: taskID, Contact: humancalling.ContactContext{Phone: "+15555550100"},
	}
	if _, err := calling.CreateHandoff(ctx, handoff); err != nil {
		t.Fatalf("replay handoff: %v", err)
	}
	for _, field := range []string{"task", "source", "phone", "service", "location", "practice"} {
		invalid := handoff
		invalid.IdempotencyKey = "invalid-" + field
		switch field {
		case "task":
			invalid.TaskID = uuid.NewString()
		case "source":
			invalid.SourceCallID = "different-request"
		case "phone":
			invalid.Contact.Phone = "+15555550123"
		case "service":
			invalid.Service.Subject = "another-agent"
		case "location":
			invalid.LocationID = uuid.NewString()
		case "practice":
			invalid.Service.PracticeID = uuid.NewString()
		}
		if _, err := calling.CreateHandoff(ctx, invalid); !errors.Is(err, humancalling.ErrDenied) {
			t.Fatalf("%s mismatch: %v", field, err)
		}
	}
	processAllCommands(t, calling)
	ring := provider.last(humancalling.CommandStartRingWindow)
	ringState, _ := ring.Payload["client_state"].(string)
	fact := caller
	fact.EventID, fact.Type = "task-ring-ended", humancalling.FactPlaybackEnded
	fact.OccurredAt, fact.ClientState, fact.PlaybackStatus = now.Add(20*time.Second), ringState, "completed"
	if err := calling.ApplyProviderFact(ctx, fact); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	speak := provider.last(humancalling.CommandSpeakVoicemail)
	speakState, _ := speak.Payload["client_state"].(string)
	fact.EventID, fact.Type, fact.ClientState = "task-greeting-ended", humancalling.FactSpeakEnded, speakState
	fact.OccurredAt = now.Add(21 * time.Second)
	if err := calling.ApplyProviderFact(ctx, fact); err != nil {
		t.Fatal(err)
	}
	processAllCommands(t, calling)
	recording := provider.last(humancalling.CommandStartVoicemailRecording)
	recordState, _ := recording.Payload["client_state"].(string)
	fact.EventID, fact.Type, fact.ClientState = "task-recording-error", humancalling.FactRecordingError, recordState
	fact.OccurredAt = now.Add(22 * time.Second)
	if err := calling.ApplyProviderFact(ctx, fact); err != nil {
		t.Fatal(err)
	}
	// The staff finishes the original need before delayed recording evidence arrives.
	accessModule := access.New(pool, func() time.Time { return now.Add(23 * time.Second) })
	module := work.New(pool, accessModule, func() time.Time { return now.Add(23 * time.Second) })
	task, err := module.ReadTask(ctx, staff[0], taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Origin != work.TaskOriginAbitaAI || len(task.Interactions) != 1 {
		t.Fatalf("missing AI Task attachment: %#v", task)
	}
	completed, err := module.CompleteTask(ctx, work.CompleteTaskCommand{Identity: staff[0], TaskID: taskID, ExpectedVersion: task.Version})
	if err != nil {
		t.Fatal(err)
	}
	provider.recording = humancalling.ProviderRecording{
		ID: "synthetic-task-recording", CallControlID: caller.CallControlID, CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
		StartedAt: now.Add(21 * time.Second), EndedAt: now.Add(24 * time.Second),
	}
	fact.EventID, fact.Type = "task-recording-saved", humancalling.FactRecordingSaved
	fact.OccurredAt, fact.RecordingStartedAt, fact.RecordingEndedAt = now.Add(24*time.Second), provider.recording.StartedAt, provider.recording.EndedAt
	for range 2 {
		if err := calling.ApplyProviderFact(ctx, fact); err != nil {
			t.Fatal(err)
		}
	}
	var tasks, attachments, handoffs int
	var voicemailTask, outcome, audioState string
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM work_tasks), (SELECT count(*) FROM work_task_interactions),
 (SELECT count(*) FROM human_calling_handoffs), task_id::text, outcome, audio_state FROM human_calling_voicemails`).Scan(&tasks, &attachments, &handoffs, &voicemailTask, &outcome, &audioState); err != nil {
		t.Fatal(err)
	}
	if tasks != 1 || attachments != 1 || handoffs != 1 || voicemailTask != taskID || outcome != "VOICEMAIL" || audioState != "READY" {
		t.Fatalf("Tasks=%d attachments=%d handoffs=%d voicemailTask=%s outcome=%s audio=%s", tasks, attachments, handoffs, voicemailTask, outcome, audioState)
	}
	final, err := module.ReadTask(ctx, staff[0], taskID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != work.TaskCompleted || final.Version != completed.Version || final.Title != task.Title || final.SourceMessage != task.SourceMessage {
		t.Fatalf("late evidence changed original work: %#v", final)
	}
}
