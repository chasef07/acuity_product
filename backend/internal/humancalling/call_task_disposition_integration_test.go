package humancalling_test

import (
	"context"
	"errors"
	"slices"
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
	other := wrapUpFixture{outboundEndFixture: current.fixture, now: current.now}.phoneTask(t, "Insurance question")
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
	var untouched bool
	if err := current.fixture.pool.QueryRow(ctx, `
		SELECT state = 'OPEN' AND version = 1 FROM work_tasks WHERE id = $1
	`, other).Scan(&untouched); err != nil || !untouched {
		t.Fatalf("expiry changed another Task for the phone: %v, %v", untouched, err)
	}
}

func TestTaskCallCompletionCannotClearNewerEvidence(t *testing.T) {
	ctx := context.Background()
	current := startConnectedTaskCall(t, "task-call-newer-evidence")
	if updated := attachMissedCall(t, current.fixture, current.work, current.now.Add(5*time.Second)); updated.ID != current.task.ID || updated.Version <= current.task.Version {
		t.Fatalf("newer missed Call did not extend the Task: %#v", updated)
	}
	if _, err := current.fixture.calling.RecordDisposition(ctx, current.fixture.identity,
		current.fixture.sessionID, current.call.ID, humancalling.DispositionCompleteTask, nil); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("stale Call completion error = %v, want conflict", err)
	}
	if task := current.readTask(t); task.State != work.TaskOpen {
		t.Fatalf("stale Call completion cleared newer evidence: %#v", task)
	}
	result, err := current.fixture.calling.RecordDisposition(ctx, current.fixture.identity,
		current.fixture.sessionID, current.call.ID, humancalling.DispositionKeepOpen, nil)
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
		current.fixture.sessionID, current.call.ID, humancalling.DispositionCompleteTask, nil)
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
		current.fixture.sessionID, current.call.ID, humancalling.DispositionCompleteTask, nil)
	if err != nil {
		t.Fatalf("record disposition: %v", err)
	}
	replay, err := current.fixture.calling.RecordDisposition(ctx, current.fixture.identity,
		current.fixture.sessionID, current.call.ID, humancalling.DispositionCompleteTask, nil)
	if err != nil || replay.TaskID != first.TaskID || replay.Call.Version != first.Call.Version {
		t.Fatalf("same-outcome replay = %#v, %v; want %#v", replay, err, first)
	}
	if _, err := current.fixture.calling.RecordDisposition(ctx, current.fixture.identity,
		current.fixture.sessionID, current.call.ID, humancalling.DispositionKeepOpen, nil); !errors.Is(err, humancalling.ErrConflict) {
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
		result, err := fixture.calling.RecordDisposition(ctx, fixture.identity, fixture.sessionID, callID, humancalling.DispositionCreateTask, nil)
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
	replay, err := fixture.calling.RecordDisposition(ctx, fixture.identity, fixture.sessionID, second.ID, humancalling.DispositionCreateTask, nil)
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

const wrapUpPhone = "+15555550123"

type wrapUpFixture struct {
	outboundEndFixture
	work *work.Module
	now  time.Time
}

func newWrapUpFixture(t *testing.T, prefix string) wrapUpFixture {
	t.Helper()
	now := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	fixture := newOutboundEndFixture(t, prefix, now, &recordingProvider{})
	return wrapUpFixture{
		outboundEndFixture: fixture,
		work: work.New(fixture.pool,
			access.New(fixture.pool, func() time.Time { return now }),
			func() time.Time { return now }),
		now: now,
	}
}

func (fixture wrapUpFixture) endedCall(t *testing.T, key string) humancalling.Call {
	t.Helper()
	call := fixture.startCall(t, key)
	endSyntheticCall(t, fixture.outboundEndFixture, call.ID, fixture.now)
	return call
}

func (fixture wrapUpFixture) openTask(t *testing.T, practiceID, locationID, phone, title string) string {
	t.Helper()
	var id string
	if err := fixture.pool.QueryRow(context.Background(), `
		INSERT INTO work_tasks (
			practice_id, location_id, phone, title, state, origin, urgency, category,
			created_by_kind, created_by_subject, created_at, updated_at,
			source_call_id, source_message, ai_idempotency_key, ai_input_fingerprint
		) VALUES (
			$1, $2, $3, $4::text, 'OPEN', 'ABITA_AI', 'normal', 'medication',
			'SERVICE', 'abita-synthetic', $5, $5, $4::text, 'Synthetic request.', $4::text,
			sha256(convert_to($4::text, 'UTF8'))
		)
		RETURNING id::text
	`, practiceID, locationID, phone, title, fixture.now.Add(-time.Hour)).Scan(&id); err != nil {
		t.Fatalf("insert synthetic Task %q: %v", title, err)
	}
	return id
}

func (fixture wrapUpFixture) phoneTask(t *testing.T, title string) string {
	t.Helper()
	return fixture.openTask(t, fixture.authorization.Practice.ID,
		fixture.authorization.Locations[0].ID, wrapUpPhone, title)
}

type storedClosure struct {
	state, reason, closingCall, actorEmail string
	completions                            int
}

func (fixture wrapUpFixture) stored(t *testing.T, taskID string) storedClosure {
	t.Helper()
	var stored storedClosure
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT task.state, COALESCE(task.close_reason, ''),
			COALESCE(task.closed_via_call_id::text, ''), COALESCE(task.completed_by_email, ''),
			(SELECT count(*) FROM work_task_activities activity
				WHERE activity.task_id = task.id AND activity.kind = 'TASK_COMPLETED')
		FROM work_tasks task WHERE task.id = $1
	`, taskID).Scan(&stored.state, &stored.reason, &stored.closingCall,
		&stored.actorEmail, &stored.completions); err != nil {
		t.Fatalf("read stored Task: %v", err)
	}
	return stored
}

func (fixture wrapUpFixture) dispose(
	callID string,
	disposition humancalling.Disposition,
	closures []work.CallTaskClosure,
) (humancalling.DispositionResult, error) {
	return fixture.calling.RecordDisposition(context.Background(), fixture.identity,
		fixture.sessionID, callID, disposition, closures)
}

func sameTasks(got []string, want ...string) bool {
	if got == nil || len(got) != len(want) {
		return false
	}
	for _, id := range want {
		if !slices.Contains(got, id) {
			return false
		}
	}
	return true
}

func TestDispositionWithoutClosuresLeavesPhoneTasksOpen(t *testing.T) {
	fixture := newWrapUpFixture(t, "wrap-up-none")
	first := fixture.phoneTask(t, "Refill request")
	second := fixture.phoneTask(t, "Insurance question")
	for name, closures := range map[string][]work.CallTaskClosure{
		"absent": nil,
		"empty":  {},
	} {
		call := fixture.endedCall(t, "wrap-up-none-"+name)
		result, err := fixture.dispose(call.ID, humancalling.DispositionResolved, closures)
		if err != nil || result.Call.State != humancalling.CallResolved || result.TaskID != "" ||
			!sameTasks(result.Closures.Closed) || !sameTasks(result.Closures.AlreadyClosed) {
			t.Fatalf("%s closures disposition = %#v, %v", name, result, err)
		}
		for _, taskID := range []string{first, second} {
			if stored := fixture.stored(t, taskID); stored.state != "OPEN" || stored.completions != 0 {
				t.Fatalf("%s closures changed a Task: %#v", name, stored)
			}
		}
	}
}

func TestDispositionClosesExactlyTheListedTasks(t *testing.T) {
	fixture := newWrapUpFixture(t, "wrap-up-two")
	done := fixture.phoneTask(t, "Refill request")
	notNeeded := fixture.phoneTask(t, "Insurance question")
	untouched := fixture.phoneTask(t, "Billing question")
	call := fixture.endedCall(t, "wrap-up-two-call")
	closures := []work.CallTaskClosure{
		{TaskID: done, Reason: work.CloseReasonDone, ExpectedVersion: 1},
		{TaskID: notNeeded, Reason: work.CloseReasonNotNeeded, ExpectedVersion: 1},
	}
	check := func(step string, result humancalling.DispositionResult, err error) {
		t.Helper()
		if err != nil || result.Call.State != humancalling.CallResolved ||
			!sameTasks(result.Closures.Closed, done, notNeeded) ||
			!sameTasks(result.Closures.AlreadyClosed) || !sameTasks(result.Closures.Changed) ||
			result.Outcome != humancalling.DispositionResolved {
			t.Fatalf("%s = %#v, %v", step, result, err)
		}
		for taskID, reason := range map[string]string{done: "DONE", notNeeded: "NOT_NEEDED"} {
			want := storedClosure{
				state: "COMPLETED", reason: reason, closingCall: call.ID,
				actorEmail: fixture.identity.Email, completions: 1,
			}
			if stored := fixture.stored(t, taskID); stored != want {
				t.Fatalf("%s stored %#v, want %#v", step, stored, want)
			}
		}
		if stored := fixture.stored(t, untouched); stored.state != "OPEN" || stored.completions != 0 {
			t.Fatalf("%s closed an unlisted Task: %#v", step, stored)
		}
	}
	result, err := fixture.dispose(call.ID, humancalling.DispositionResolved, closures)
	check("disposition", result, err)
	replay, err := fixture.dispose(call.ID, humancalling.DispositionResolved, closures)
	check("replay", replay, err)
	if replay.Call.Version != result.Call.Version {
		t.Fatalf("replay moved the Call from version %d to %d", result.Call.Version, replay.Call.Version)
	}
}

func TestDispositionRejectsClosuresOutsideTheCall(t *testing.T) {
	ctx := context.Background()
	fixture := newWrapUpFixture(t, "wrap-up-reject")
	practiceID := fixture.authorization.Practice.ID
	locationID := fixture.authorization.Locations[0].ID
	listed := fixture.phoneTask(t, "Refill request")
	otherPhone := fixture.openTask(t, practiceID, locationID, "+15555550199", "Other phone")
	var otherLocationID, otherPracticeID, otherPracticeLocationID string
	if err := fixture.pool.QueryRow(ctx, `
		INSERT INTO access_locations (practice_id, provisioning_key, name)
		VALUES ($1, 'wrap-up-reject-hidden', 'Hidden office') RETURNING id::text
	`, practiceID).Scan(&otherLocationID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		WITH membership AS (
			UPDATE access_memberships SET location_scope = 'SELECTED'
			WHERE user_subject = $1 AND practice_id = $2 RETURNING id
		)
		INSERT INTO access_membership_locations (membership_id, location_id, practice_id)
		SELECT id, $3, $2 FROM membership
	`, fixture.identity.Subject, practiceID, locationID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(ctx, `
		WITH practice AS (
			INSERT INTO access_practices (provisioning_key, name)
			VALUES ('wrap-up-reject-other', 'Other practice') RETURNING id
		)
		INSERT INTO access_locations (practice_id, provisioning_key, name)
		SELECT id, 'wrap-up-reject-other', 'Other office' FROM practice
		RETURNING practice_id::text, id::text
	`).Scan(&otherPracticeID, &otherPracticeLocationID); err != nil {
		t.Fatal(err)
	}
	hidden := fixture.openTask(t, practiceID, otherLocationID, wrapUpPhone, "Hidden office")
	foreign := fixture.openTask(t, otherPracticeID, otherPracticeLocationID, wrapUpPhone, "Other practice")
	call := fixture.endedCall(t, "wrap-up-reject-call")
	valid := work.CallTaskClosure{TaskID: listed, Reason: work.CloseReasonDone, ExpectedVersion: 1}
	for name, test := range map[string]struct {
		closure work.CallTaskClosure
		want    error
	}{
		"another phone":       {work.CallTaskClosure{TaskID: otherPhone, Reason: work.CloseReasonDone, ExpectedVersion: 1}, humancalling.ErrInvalidInput},
		"unreadable Location": {work.CallTaskClosure{TaskID: hidden, Reason: work.CloseReasonDone, ExpectedVersion: 1}, humancalling.ErrInvalidInput},
		"another Practice":    {work.CallTaskClosure{TaskID: foreign, Reason: work.CloseReasonDone, ExpectedVersion: 1}, humancalling.ErrInvalidInput},
		"unknown Task":        {work.CallTaskClosure{TaskID: call.ID, Reason: work.CloseReasonDone, ExpectedVersion: 1}, humancalling.ErrInvalidInput},
		"unreachable reason":  {work.CallTaskClosure{TaskID: otherPhone, Reason: work.CloseReasonUnreachable, ExpectedVersion: 1}, humancalling.ErrInvalidInput},
		"expired reason":      {work.CallTaskClosure{TaskID: otherPhone, Reason: work.CloseReasonExpired, ExpectedVersion: 1}, humancalling.ErrInvalidInput},
		"listed twice":        {valid, humancalling.ErrInvalidInput},
	} {
		if _, err := fixture.dispose(call.ID, humancalling.DispositionResolved,
			[]work.CallTaskClosure{valid, test.closure}); !errors.Is(err, test.want) {
			t.Fatalf("%s error = %v, want %v", name, err, test.want)
		}
		for _, taskID := range []string{listed, otherPhone, hidden, foreign} {
			if stored := fixture.stored(t, taskID); stored.state != "OPEN" || stored.completions != 0 {
				t.Fatalf("%s wrote to a Task: %#v", name, stored)
			}
		}
		var recorded bool
		if err := fixture.pool.QueryRow(ctx, `
			SELECT disposition_outcome IS NOT NULL FROM human_calling_calls WHERE id = $1
		`, call.ID).Scan(&recorded); err != nil || recorded {
			t.Fatalf("%s recorded the outcome: %v, %v", name, recorded, err)
		}
	}
}

func TestDispositionReportsATaskSomeoneElseClosed(t *testing.T) {
	ctx := context.Background()
	fixture := newWrapUpFixture(t, "wrap-up-raced")
	raced := fixture.phoneTask(t, "Refill request")
	open := fixture.phoneTask(t, "Insurance question")
	call := fixture.endedCall(t, "wrap-up-raced-call")
	if _, err := fixture.work.CompleteTask(ctx, work.CompleteTaskCommand{
		Identity: fixture.identity, TaskID: raced, ExpectedVersion: 1,
		Reason: work.CloseReasonUnreachable,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.dispose(call.ID, humancalling.DispositionResolved, []work.CallTaskClosure{
		{TaskID: raced, Reason: work.CloseReasonDone, ExpectedVersion: 1},
		{TaskID: open, Reason: work.CloseReasonDone, ExpectedVersion: 1},
	})
	if err != nil || !sameTasks(result.Closures.Closed, open) ||
		!sameTasks(result.Closures.AlreadyClosed, raced) {
		t.Fatalf("raced closure = %#v, %v", result, err)
	}
	if stored := fixture.stored(t, raced); stored.reason != "UNREACHABLE" ||
		stored.closingCall != "" || stored.completions != 1 {
		t.Fatalf("already closed Task was rewritten: %#v", stored)
	}
	if stored := fixture.stored(t, open); stored.state != "COMPLETED" || stored.closingCall != call.ID {
		t.Fatalf("open Task beside a closed one = %#v", stored)
	}
}

func TestTaskCallClosureClosesTheCallTaskOnceWithItsReason(t *testing.T) {
	current := startConnectedTaskCall(t, "wrap-up-own-task")
	fixture := wrapUpFixture{outboundEndFixture: current.fixture, work: current.work, now: current.now}
	other := fixture.phoneTask(t, "Insurance question")
	own := work.CallTaskClosure{TaskID: current.task.ID, Reason: work.CloseReasonNotNeeded, ExpectedVersion: current.task.Version}
	if _, err := fixture.dispose(current.call.ID, humancalling.DispositionKeepOpen,
		[]work.CallTaskClosure{own}); !errors.Is(err, humancalling.ErrInvalidInput) {
		t.Fatalf("kept-open Call Task listed for closure error = %v, want invalid input", err)
	}
	if stored := fixture.stored(t, current.task.ID); stored.state != "OPEN" {
		t.Fatalf("rejected disposition closed the Call Task: %#v", stored)
	}
	result, err := fixture.dispose(current.call.ID, humancalling.DispositionCompleteTask,
		[]work.CallTaskClosure{own, {TaskID: other, Reason: work.CloseReasonDone, ExpectedVersion: 1}})
	if err != nil || result.TaskID != current.task.ID ||
		!sameTasks(result.Closures.Closed, current.task.ID, other) ||
		!sameTasks(result.Closures.AlreadyClosed) {
		t.Fatalf("Call Task closure = %#v, %v", result, err)
	}
	want := storedClosure{
		state: "COMPLETED", reason: "NOT_NEEDED", closingCall: current.call.ID,
		actorEmail: fixture.identity.Email, completions: 1,
	}
	if stored := fixture.stored(t, current.task.ID); stored != want {
		t.Fatalf("Call Task stored %#v, want %#v", stored, want)
	}
}

func TestListedCallTaskStillCannotClearNewerEvidence(t *testing.T) {
	current := startConnectedTaskCall(t, "wrap-up-own-evidence")
	fixture := wrapUpFixture{outboundEndFixture: current.fixture, work: current.work, now: current.now}
	seen := attachMissedCall(t, current.fixture, current.work, current.now.Add(5*time.Second))
	if _, err := fixture.dispose(current.call.ID, humancalling.DispositionCompleteTask,
		[]work.CallTaskClosure{{TaskID: current.task.ID, Reason: work.CloseReasonDone, ExpectedVersion: seen.Version}},
	); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("listed stale Call Task error = %v, want conflict", err)
	}
	if stored := fixture.stored(t, current.task.ID); stored.state != "OPEN" {
		t.Fatalf("listed Call Task cleared newer evidence: %#v", stored)
	}
}

func TestClosingTheOldFollowUpStillCreatesTheNewOne(t *testing.T) {
	fixture := newWrapUpFixture(t, "wrap-up-follow-up")
	first := fixture.endedCall(t, "wrap-up-follow-up-first")
	created, err := fixture.dispose(first.ID, humancalling.DispositionCreateTask, nil)
	if err != nil || created.TaskID == "" {
		t.Fatalf("create follow-up Task = %#v, %v", created, err)
	}
	second := fixture.endedCall(t, "wrap-up-follow-up-second")
	result, err := fixture.dispose(second.ID, humancalling.DispositionCreateTask,
		[]work.CallTaskClosure{{TaskID: created.TaskID, Reason: work.CloseReasonDone, ExpectedVersion: 1}})
	if err != nil || result.TaskID == "" || result.TaskID == created.TaskID ||
		!sameTasks(result.Closures.Closed, created.TaskID) {
		t.Fatalf("follow-up after closing the old one = %#v, %v", result, err)
	}
	if stored := fixture.stored(t, result.TaskID); stored.state != "OPEN" {
		t.Fatalf("new follow-up Task = %#v", stored)
	}
	if stored := fixture.stored(t, created.TaskID); stored.state != "COMPLETED" || stored.closingCall != second.ID {
		t.Fatalf("old follow-up Task = %#v", stored)
	}
}
