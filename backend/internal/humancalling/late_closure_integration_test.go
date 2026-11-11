package humancalling_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

func done(taskID string) work.CallTaskClosure {
	return work.CallTaskClosure{TaskID: taskID, Reason: work.CloseReasonDone, ExpectedVersion: 1}
}

func (fixture wrapUpFixture) storedOutcome(t *testing.T, callID string) (string, int) {
	t.Helper()
	var outcome string
	var recorded int
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT COALESCE(call.disposition_outcome, ''),
			(SELECT count(*) FROM human_calling_timeline timeline
				WHERE timeline.call_id = call.id AND timeline.kind = 'call.dispositioned')
		FROM human_calling_calls call WHERE call.id = $1
	`, callID).Scan(&outcome, &recorded); err != nil {
		t.Fatalf("read stored outcome: %v", err)
	}
	return outcome, recorded
}

func (fixture wrapUpFixture) expire(t *testing.T) {
	t.Helper()
	if expired, err := fixture.calling.ExpireDispositions(context.Background()); err != nil || expired != 1 {
		t.Fatalf("expire disposition = %d, %v", expired, err)
	}
}

func (fixture wrapUpFixture) bump(t *testing.T, taskID string) {
	t.Helper()
	if _, err := fixture.pool.Exec(context.Background(), `
		UPDATE work_tasks SET version = version + 1 WHERE id = $1
	`, taskID); err != nil {
		t.Fatalf("change Task: %v", err)
	}
}

func TestClosuresAfterTheCallWasResolvedAutomaticallyStillClose(t *testing.T) {
	fixture := newWrapUpFixture(t, "late-expired")
	refill := fixture.phoneTask(t, "Refill request")
	insurance := fixture.phoneTask(t, "Insurance question")
	untouched := fixture.phoneTask(t, "Billing question")
	call := fixture.endedCall(t, "late-expired-call")
	fixture.expire(t)
	result, err := fixture.dispose(call.ID, humancalling.DispositionCreateTask,
		[]work.CallTaskClosure{done(refill)})
	if err != nil || result.Outcome != humancalling.DispositionResolved || result.TaskID != "" ||
		result.Call.State != humancalling.CallResolved ||
		!sameTasks(result.Closures.Closed, refill) || !sameTasks(result.Closures.AlreadyClosed) ||
		!sameTasks(result.Closures.Changed) {
		t.Fatalf("closure after automatic resolve = %#v, %v", result, err)
	}
	want := storedClosure{
		state: "COMPLETED", reason: "DONE", closingCall: call.ID,
		actorEmail: fixture.identity.Email, completions: 1,
	}
	if stored := fixture.stored(t, refill); stored != want {
		t.Fatalf("late closure stored %#v, want %#v", stored, want)
	}
	second, err := fixture.dispose(call.ID, humancalling.DispositionResolved,
		[]work.CallTaskClosure{done(refill), done(insurance)})
	if err != nil || second.Outcome != humancalling.DispositionResolved ||
		!sameTasks(second.Closures.Closed, refill, insurance) ||
		!sameTasks(second.Closures.AlreadyClosed) || !sameTasks(second.Closures.Changed) {
		t.Fatalf("second closure request = %#v, %v", second, err)
	}
	if stored := fixture.stored(t, refill); stored != want {
		t.Fatalf("repeated closure rewrote the Task: %#v", stored)
	}
	if stored := fixture.stored(t, insurance); stored != want {
		t.Fatalf("second late closure stored %#v, want %#v", stored, want)
	}
	if stored := fixture.stored(t, untouched); stored.state != "OPEN" || stored.completions != 0 {
		t.Fatalf("late closure reached an unlisted Task: %#v", stored)
	}
	if outcome, recorded := fixture.storedOutcome(t, call.ID); outcome != "RESOLVED" || recorded != 1 {
		t.Fatalf("stored outcome = %q recorded %d times; want RESOLVED once", outcome, recorded)
	}
	var tasks int
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM work_tasks WHERE practice_id = $1
	`, fixture.authorization.Practice.ID).Scan(&tasks); err != nil || tasks != 3 {
		t.Fatalf("Tasks after late closures = %d, %v; want no follow-up Task", tasks, err)
	}
	if _, err := fixture.dispose(call.ID, humancalling.DispositionCreateTask, nil); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("different outcome without closures error = %v, want conflict", err)
	}
}

func TestLateClosuresAreForTheCallOwnerWithinTheWindow(t *testing.T) {
	ctx := context.Background()
	fixture := newWrapUpFixture(t, "late-guard")
	task := fixture.phoneTask(t, "Refill request")
	call := fixture.endedCall(t, "late-guard-call")
	if _, err := fixture.dispose(call.ID, humancalling.DispositionResolved, nil); err != nil {
		t.Fatal(err)
	}
	closures := []work.CallTaskClosure{done(task)}
	endedAgo := func(age time.Duration) {
		t.Helper()
		if _, err := fixture.pool.Exec(ctx, `
			UPDATE human_calling_calls SET ended_at = $2 WHERE id = $1
		`, call.ID, fixture.now.Add(-age)); err != nil {
			t.Fatalf("age synthetic Call: %v", err)
		}
	}
	rejected := func(step string, err error) {
		t.Helper()
		if !errors.Is(err, humancalling.ErrConflict) {
			t.Fatalf("%s error = %v, want conflict", step, err)
		}
		if stored := fixture.stored(t, task); stored.state != "OPEN" || stored.completions != 0 {
			t.Fatalf("%s closed the Task: %#v", step, stored)
		}
	}
	endedAgo(30*time.Minute + time.Second)
	_, err := fixture.dispose(call.ID, humancalling.DispositionResolved, closures)
	rejected("closure after the window", err)
	endedAgo(30 * time.Minute)
	_, err = fixture.calling.RecordDisposition(ctx, fixture.identity, "another-browser",
		call.ID, humancalling.DispositionResolved, closures)
	rejected("closure without the softphone lease", err)
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE human_calling_call_legs SET staff_subject = 'synthetic-other-staff'
		WHERE call_id = $1 AND role = 'STAFF'
	`, call.ID); err != nil {
		t.Fatalf("move synthetic Call to another staff member: %v", err)
	}
	_, err = fixture.dispose(call.ID, humancalling.DispositionResolved, closures)
	rejected("closure from a staff member who did not take the Call", err)
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE human_calling_call_legs SET staff_subject = $2
		WHERE call_id = $1 AND role = 'STAFF'
	`, call.ID, fixture.identity.Subject); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.dispose(call.ID, humancalling.DispositionResolved, closures)
	if err != nil || !sameTasks(result.Closures.Closed, task) {
		t.Fatalf("closure at the edge of the window = %#v, %v", result, err)
	}
}

func TestAChangedTaskStaysOpenAndIsReported(t *testing.T) {
	fixture := newWrapUpFixture(t, "wrap-up-changed")
	changed := fixture.phoneTask(t, "Refill request")
	steady := fixture.phoneTask(t, "Insurance question")
	later := fixture.phoneTask(t, "Billing question")
	call := fixture.endedCall(t, "wrap-up-changed-call")
	fixture.bump(t, changed)
	result, err := fixture.dispose(call.ID, humancalling.DispositionResolved,
		[]work.CallTaskClosure{done(changed), done(steady)})
	if err != nil || result.Outcome != humancalling.DispositionResolved ||
		!sameTasks(result.Closures.Closed, steady) || !sameTasks(result.Closures.AlreadyClosed) ||
		!sameTasks(result.Closures.Changed, changed) {
		t.Fatalf("disposition with a changed Task = %#v, %v", result, err)
	}
	if stored := fixture.stored(t, changed); stored.state != "OPEN" || stored.completions != 0 {
		t.Fatalf("changed Task was closed: %#v", stored)
	}
	if stored := fixture.stored(t, steady); stored.state != "COMPLETED" || stored.closingCall != call.ID {
		t.Fatalf("Task beside a changed one = %#v", stored)
	}
	fixture.bump(t, later)
	late, err := fixture.dispose(call.ID, humancalling.DispositionResolved,
		[]work.CallTaskClosure{done(later), {TaskID: changed, Reason: work.CloseReasonNotNeeded, ExpectedVersion: 2}})
	if err != nil || !sameTasks(late.Closures.Closed, changed) || !sameTasks(late.Closures.Changed, later) {
		t.Fatalf("late closure with a changed Task = %#v, %v", late, err)
	}
	if stored := fixture.stored(t, later); stored.state != "OPEN" {
		t.Fatalf("changed Task was closed late: %#v", stored)
	}
	if stored := fixture.stored(t, changed); stored.state != "COMPLETED" || stored.reason != "NOT_NEEDED" {
		t.Fatalf("Task closed at the version the staff member saw = %#v", stored)
	}
	if _, err := fixture.dispose(call.ID, humancalling.DispositionResolved,
		[]work.CallTaskClosure{{TaskID: later, Reason: work.CloseReasonDone}}); !errors.Is(err, humancalling.ErrInvalidInput) {
		t.Fatalf("closure without a version error = %v, want invalid input", err)
	}
}

func TestAChangedCallTaskIsKeptOpenAndRecordedAsKept(t *testing.T) {
	current := startConnectedTaskCall(t, "wrap-up-own-changed")
	fixture := wrapUpFixture{outboundEndFixture: current.fixture, work: current.work, now: current.now}
	other := fixture.phoneTask(t, "Insurance question")
	fixture.bump(t, current.task.ID)
	result, err := fixture.dispose(current.call.ID, humancalling.DispositionCompleteTask,
		[]work.CallTaskClosure{
			{TaskID: current.task.ID, Reason: work.CloseReasonDone, ExpectedVersion: current.task.Version},
			done(other),
		})
	if err != nil || result.Outcome != humancalling.DispositionKeepOpen ||
		!sameTasks(result.Closures.Closed, other) ||
		!sameTasks(result.Closures.Changed, current.task.ID) {
		t.Fatalf("disposition with a changed Call Task = %#v, %v", result, err)
	}
	if stored := fixture.stored(t, current.task.ID); stored.state != "OPEN" || stored.completions != 0 {
		t.Fatalf("changed Call Task was closed: %#v", stored)
	}
	if outcome, recorded := fixture.storedOutcome(t, current.call.ID); outcome != "KEEP_OPEN" || recorded != 1 {
		t.Fatalf("stored outcome = %q recorded %d times; want KEEP_OPEN once", outcome, recorded)
	}
}

func TestDispositionAcceptsFiftyClosuresAndRejectsMore(t *testing.T) {
	fixture := newWrapUpFixture(t, "wrap-up-cap")
	closures := make([]work.CallTaskClosure, 51)
	for index := range closures {
		closures[index] = done(fixture.phoneTask(t, fmt.Sprintf("Synthetic request %02d", index)))
	}
	call := fixture.endedCall(t, "wrap-up-cap-call")
	if _, err := fixture.dispose(call.ID, humancalling.DispositionResolved, closures); !errors.Is(err, humancalling.ErrInvalidInput) {
		t.Fatalf("51 closures error = %v, want invalid input", err)
	}
	if outcome, _ := fixture.storedOutcome(t, call.ID); outcome != "" {
		t.Fatalf("51 closures recorded the outcome %q", outcome)
	}
	if stored := fixture.stored(t, closures[0].TaskID); stored.state != "OPEN" {
		t.Fatalf("51 closures closed a Task: %#v", stored)
	}
	result, err := fixture.dispose(call.ID, humancalling.DispositionResolved, closures[:50])
	if err != nil || len(result.Closures.Closed) != 50 {
		t.Fatalf("50 closures = %d closed, %v", len(result.Closures.Closed), err)
	}
	if stored := fixture.stored(t, closures[50].TaskID); stored.state != "OPEN" {
		t.Fatalf("the unlisted Task was closed: %#v", stored)
	}
}

func TestClosuresNeedAnOutcomeTheCallAllows(t *testing.T) {
	ctx := context.Background()
	fixture := newWrapUpFixture(t, "wrap-up-allowed")
	task := fixture.phoneTask(t, "Refill request")
	closures := []work.CallTaskClosure{done(task)}
	rejected := func(step string, callID string, err error) {
		t.Helper()
		if !errors.Is(err, humancalling.ErrConflict) {
			t.Fatalf("%s error = %v, want conflict", step, err)
		}
		if stored := fixture.stored(t, task); stored.state != "OPEN" || stored.completions != 0 {
			t.Fatalf("%s closed the Task: %#v", step, stored)
		}
		if outcome, _ := fixture.storedOutcome(t, callID); outcome != "" {
			t.Fatalf("%s recorded the outcome %q", step, outcome)
		}
	}
	unconnected := fixture.startCall(t, "wrap-up-allowed-unconnected")
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE human_calling_call_legs SET state = 'ENDED', ending_at = $2, ended_at = $2, updated_at = $2
		WHERE call_id = $1
	`, unconnected.ID, fixture.now); err != nil {
		t.Fatalf("end unconnected CallLegs: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE human_calling_calls
		SET terminal_outcome = 'ENDED', ended_at = $2, disposition_deadline = $2, updated_at = $2
		WHERE id = $1
	`, unconnected.ID, fixture.now); err != nil {
		t.Fatalf("end unconnected Call: %v", err)
	}
	for _, outcome := range []humancalling.Disposition{
		humancalling.DispositionNoFollowUp, humancalling.DispositionCreateTask,
	} {
		_, err := fixture.dispose(unconnected.ID, outcome, closures)
		rejected("closure on a Call that never connected with "+string(outcome), unconnected.ID, err)
	}
	connected := fixture.endedCall(t, "wrap-up-allowed-connected")
	for _, outcome := range []humancalling.Disposition{
		humancalling.DispositionFollowUpRequired, humancalling.DispositionCompleteTask,
		humancalling.DispositionKeepOpen, humancalling.DispositionNoFollowUp,
	} {
		_, err := fixture.dispose(connected.ID, outcome, closures)
		rejected("closure with "+string(outcome), connected.ID, err)
	}
	_, err := fixture.calling.RecordDisposition(ctx, fixture.identity, "another-browser",
		connected.ID, humancalling.DispositionResolved, closures)
	rejected("closure without the softphone lease", connected.ID, err)
	result, err := fixture.dispose(connected.ID, humancalling.DispositionResolved, closures)
	if err != nil || !sameTasks(result.Closures.Closed, task) {
		t.Fatalf("allowed closure = %#v, %v", result, err)
	}
}
