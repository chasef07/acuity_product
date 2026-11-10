package humancalling_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

type connectedTaskCall struct {
	fixture outboundEndFixture
	work    *work.Module
	task    work.Task
	call    humancalling.Call
	now     time.Time
}

func startConnectedTaskCall(t *testing.T, prefix string) connectedTaskCall {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	fixture := newOutboundEndFixture(t, prefix, now, &recordingProvider{})
	workModule := work.New(fixture.pool,
		access.New(fixture.pool, func() time.Time { return now }),
		func() time.Time { return now })
	task := attachMissedCall(t, fixture, workModule, now.Add(-time.Minute))
	call, err := fixture.calling.StartOutboundCall(ctx, humancalling.StartOutboundCallCommand{
		Identity: fixture.identity, SessionID: fixture.sessionID,
		IdempotencyKey: prefix + "-call", TaskID: task.ID,
	})
	if err != nil {
		t.Fatalf("start Task Call: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET state = 'ENDED', answered_at = $2, bridge_pending_at = $2, bridged_at = $2,
			ending_at = $2, ended_at = $2, updated_at = $2
		WHERE call_id = $1
	`, call.ID, now); err != nil {
		t.Fatalf("connect synthetic Task Call: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE human_calling_calls
		SET terminal_outcome = 'ENDED', ended_at = $2, disposition_deadline = $2, updated_at = $2
		WHERE id = $1
	`, call.ID, now); err != nil {
		t.Fatalf("end synthetic Task Call: %v", err)
	}
	return connectedTaskCall{fixture: fixture, work: workModule, task: task, call: call, now: now}
}

func attachMissedCall(t *testing.T, fixture outboundEndFixture, workModule *work.Module, at time.Time) work.Task {
	t.Helper()
	ctx := context.Background()
	var sourceID string
	if err := fixture.pool.QueryRow(ctx, `
		INSERT INTO human_calling_calls (
			practice_id, location_id, direction, entry_point, terminal_outcome,
			caller_phone, ended_at, created_at, updated_at
		) VALUES ($1, $2, 'INBOUND', 'STANDALONE', 'VOICEMAIL', '+15555550123', $3, $3, $3)
		RETURNING id::text
	`, fixture.authorization.Practice.ID, fixture.authorization.Locations[0].ID, at).Scan(&sourceID); err != nil {
		t.Fatalf("insert synthetic missed Call: %v", err)
	}
	tx, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	task, err := workModule.EnsureRecoveryTask(ctx, tx, work.EnsureRecoveryTaskCommand{
		CallID: sourceID, PracticeID: fixture.authorization.Practice.ID,
		LocationID: fixture.authorization.Locations[0].ID, Phone: "+15555550123",
		Outcome: work.RecoveryOutcomeMissedCall, OccurredAt: at,
	})
	if err != nil {
		t.Fatalf("attach synthetic missed Call: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return task
}

func (current connectedTaskCall) readTask(t *testing.T) work.Task {
	t.Helper()
	task, err := current.work.ReadTask(context.Background(), current.fixture.identity, current.task.ID)
	if err != nil {
		t.Fatalf("read Task: %v", err)
	}
	return task
}

func TestExpiredTaskCallDispositionLeavesTaskOpen(t *testing.T) {
	ctx := context.Background()
	current := startConnectedTaskCall(t, "task-call-expiry")
	if expired, err := current.fixture.calling.ExpireDispositions(ctx); err != nil || expired != 1 {
		t.Fatalf("expire Task Call disposition = %d, %v", expired, err)
	}
	call, err := current.fixture.calling.ReadCall(ctx, current.fixture.identity, current.call.ID)
	if err != nil || call.State != humancalling.CallResolved || call.DispositionDeadline != nil {
		t.Fatalf("expired Task Call = %#v, %v", call, err)
	}
	task := current.readTask(t)
	if task.State != work.TaskOpen || task.CompletedBy != nil || task.Version != current.task.Version {
		t.Fatalf("disposition expiry changed the Task: %#v", task)
	}
	var completions int
	if err := current.fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM work_task_activities WHERE task_id = $1 AND kind = 'TASK_COMPLETED'
	`, task.ID).Scan(&completions); err != nil || completions != 0 {
		t.Fatalf("expiry completion Activity = %d, %v", completions, err)
	}
}

func TestTaskCallCompletionCannotClearNewerEvidence(t *testing.T) {
	ctx := context.Background()
	current := startConnectedTaskCall(t, "task-call-newer-evidence")
	if updated := attachMissedCall(t, current.fixture, current.work, current.now.Add(5*time.Second)); updated.ID != current.task.ID || updated.Version <= current.task.Version {
		t.Fatalf("newer missed Call did not extend the Task: %#v", updated)
	}
	if _, err := current.fixture.calling.RecordDisposition(ctx, current.fixture.identity,
		current.fixture.sessionID, current.call.ID, humancalling.DispositionCompleteTask); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("stale Call completion error = %v, want conflict", err)
	}
	if task := current.readTask(t); task.State != work.TaskOpen {
		t.Fatalf("stale Call completion cleared newer evidence: %#v", task)
	}
	result, err := current.fixture.calling.RecordDisposition(ctx, current.fixture.identity,
		current.fixture.sessionID, current.call.ID, humancalling.DispositionKeepOpen)
	if err != nil || result.TaskID != current.task.ID {
		t.Fatalf("keep Task open after conflict = %#v, %v", result, err)
	}
	if task := current.readTask(t); task.State != work.TaskOpen {
		t.Fatalf("kept Task = %#v", task)
	}
}

func TestTaskCallCompletionRecordsStaffActor(t *testing.T) {
	ctx := context.Background()
	current := startConnectedTaskCall(t, "task-call-complete")
	result, err := current.fixture.calling.RecordDisposition(ctx, current.fixture.identity,
		current.fixture.sessionID, current.call.ID, humancalling.DispositionCompleteTask)
	if err != nil || result.TaskID != current.task.ID {
		t.Fatalf("complete Task from Call = %#v, %v", result, err)
	}
	task := current.readTask(t)
	if task.State != work.TaskCompleted || task.CompletedBy == nil ||
		task.CompletedBy.Kind != access.ActorHuman || task.CompletedBy.Email != current.fixture.identity.Email {
		t.Fatalf("Call completion = %#v", task)
	}
	var closingCall string
	if err := current.fixture.pool.QueryRow(ctx, `
		SELECT COALESCE(closed_via_call_id::text, '') FROM work_tasks WHERE id = $1
	`, task.ID).Scan(&closingCall); err != nil ||
		task.CloseReason != work.CloseReasonDone || closingCall != current.call.ID {
		t.Fatalf("Call completion stored reason %q and closing Call %q, %v; want DONE and %q",
			task.CloseReason, closingCall, err, current.call.ID)
	}
}

func TestDispositionReplayReturnsTheRecordedOutcome(t *testing.T) {
	ctx := context.Background()
	current := startConnectedTaskCall(t, "task-call-replay")
	first, err := current.fixture.calling.RecordDisposition(ctx, current.fixture.identity,
		current.fixture.sessionID, current.call.ID, humancalling.DispositionCompleteTask)
	if err != nil {
		t.Fatalf("record disposition: %v", err)
	}
	replay, err := current.fixture.calling.RecordDisposition(ctx, current.fixture.identity,
		current.fixture.sessionID, current.call.ID, humancalling.DispositionCompleteTask)
	if err != nil || replay.TaskID != first.TaskID || replay.Call.Version != first.Call.Version {
		t.Fatalf("same-outcome replay = %#v, %v; want %#v", replay, err, first)
	}
	if _, err := current.fixture.calling.RecordDisposition(ctx, current.fixture.identity,
		current.fixture.sessionID, current.call.ID, humancalling.DispositionKeepOpen); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("different-outcome replay error = %v, want conflict", err)
	}
	var recorded int
	if err := current.fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM human_calling_timeline
		WHERE call_id = $1 AND kind = 'call.dispositioned'
	`, current.call.ID).Scan(&recorded); err != nil || recorded != 1 {
		t.Fatalf("recorded dispositions = %d, %v", recorded, err)
	}
}

func endSyntheticCall(t *testing.T, fixture outboundEndFixture, callID string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET state = 'ENDED', answered_at = $2, bridge_pending_at = $2, bridged_at = $2,
			ending_at = $2, ended_at = $2, updated_at = $2
		WHERE call_id = $1
	`, callID, at); err != nil {
		t.Fatalf("connect synthetic Call: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE human_calling_calls
		SET terminal_outcome = 'ENDED', ended_at = $2, disposition_deadline = $2, updated_at = $2
		WHERE id = $1
	`, callID, at); err != nil {
		t.Fatalf("end synthetic Call: %v", err)
	}
}

func TestFollowUpDispositionReplayNeverCreatesAnotherTask(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	fixture := newOutboundEndFixture(t, "follow-up-replay", now, &recordingProvider{})
	workModule := work.New(fixture.pool,
		access.New(fixture.pool, func() time.Time { return now }),
		func() time.Time { return now })
	createFollowUp := func(callID string) humancalling.DispositionResult {
		t.Helper()
		result, err := fixture.calling.RecordDisposition(ctx, fixture.identity, fixture.sessionID, callID, humancalling.DispositionCreateTask)
		if err != nil || result.TaskID == "" {
			t.Fatalf("create follow-up Task = %#v, %v", result, err)
		}
		return result
	}
	first := fixture.startCall(t, "follow-up-replay-first")
	endSyntheticCall(t, fixture, first.ID, now)
	created := createFollowUp(first.ID)
	second := fixture.startCall(t, "follow-up-replay-second")
	endSyntheticCall(t, fixture, second.ID, now)
	reused := createFollowUp(second.ID)
	if reused.TaskID != created.TaskID {
		t.Fatalf("second Call Task = %q, want the open Task %q", reused.TaskID, created.TaskID)
	}
	task, err := workModule.ReadTask(ctx, fixture.identity, created.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workModule.CompleteTask(ctx, work.CompleteTaskCommand{
		Reason:   work.CloseReasonDone,
		Identity: fixture.identity, TaskID: task.ID, ExpectedVersion: task.Version,
	}); err != nil {
		t.Fatal(err)
	}
	replay, err := fixture.calling.RecordDisposition(ctx, fixture.identity, fixture.sessionID, second.ID, humancalling.DispositionCreateTask)
	if err != nil || replay.TaskID != created.TaskID {
		t.Fatalf("follow-up replay Task = %q, %v; want %q", replay.TaskID, err, created.TaskID)
	}
	var tasks int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM work_tasks WHERE practice_id = $1
	`, fixture.authorization.Practice.ID).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatalf("Tasks after replay = %d, %v; want 1", tasks, err)
	}
}
