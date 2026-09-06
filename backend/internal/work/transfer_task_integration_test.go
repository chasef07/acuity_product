package work_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestRecoveryReusesExplicitAITask(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	ai, _, err := module.CreateAITask(ctx, work.CreateAITaskCommand{
		Service: access.ServiceIdentity{Subject: "synthetic-agent", PracticeID: authorization.Practice.ID,
			LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}},
		OfficeKey: "spring-hill", OfficePhone: "+17275919997", SourceCallID: "synthetic-source",
		IdempotencyKey: "synthetic-task", Phone: "+15555550100", Summary: "Review appointment request",
		Message: "Synthetic caller asks staff to review appointment options.", Category: work.TaskCategoryAppointments, Urgency: work.TaskUrgencyNormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	callID := insertCall(t, pool, authorization, now)
	command := work.EnsureRecoveryTaskCommand{TaskID: ai.ID, CallID: callID, PracticeID: ai.PracticeID,
		LocationID: ai.LocationID, Phone: ai.Phone, Outcome: work.RecoveryOutcomeMissedCall, OccurredAt: now}
	ensure := func() work.Task {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		result, err := module.EnsureRecoveryTask(ctx, tx, command)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, outcome := range []work.RecoveryOutcome{work.RecoveryOutcomeMissedCall, work.RecoveryOutcomeVoicemail, work.RecoveryOutcomeMissedCall} {
		command.Outcome = outcome
		result := ensure()
		if result.ID != ai.ID || result.Title != ai.Title || result.Origin != ai.Origin || result.SourceMessage != ai.SourceMessage {
			t.Fatalf("recovery replaced original AI Task: got %#v, want %s", result, ai.ID)
		}
	}
	read, err := module.ReadTask(ctx, identity, ai.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Interactions) != 1 || read.Interactions[0].CallID != callID {
		t.Fatalf("missing transferred Call: %#v", read.Interactions)
	}
	completed, err := module.CompleteTask(ctx, work.CompleteTaskCommand{Identity: identity, TaskID: ai.ID, ExpectedVersion: read.Version})
	if err != nil {
		t.Fatal(err)
	}
	replay := ensure()
	if replay.State != work.TaskCompleted || replay.Version != completed.Version {
		t.Fatalf("replay changed completed work: %#v", replay)
	}
	var tasks, attachments int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM work_tasks), (SELECT count(*) FROM work_task_interactions)`).Scan(&tasks, &attachments); err != nil {
		t.Fatal(err)
	}
	if tasks != 1 || attachments != 1 {
		t.Fatalf("got %d Tasks and %d attachments; want 1 each", tasks, attachments)
	}
}

func TestRecoveryKeepsSeparateNeedsAndConcurrentReplays(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	create := func(key string) work.Task {
		t.Helper()
		task, _, err := module.CreateAITask(ctx, work.CreateAITaskCommand{
			Service: access.ServiceIdentity{Subject: "synthetic-agent", PracticeID: authorization.Practice.ID,
				LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}},
			OfficeKey: "spring-hill", OfficePhone: "+17275919997", SourceCallID: "same-call",
			IdempotencyKey: key, Phone: "+15555550100", Summary: "Review " + key,
			Message: "Synthetic request: " + key, Category: work.TaskCategoryAppointments, Urgency: work.TaskUrgencyNormal,
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	first, second := create("appointment-options"), create("records-request")
	callID := insertCall(t, pool, authorization, now)
	command := work.EnsureRecoveryTaskCommand{TaskID: first.ID, CallID: callID, PracticeID: first.PracticeID,
		LocationID: first.LocationID, Phone: first.Phone, Outcome: work.RecoveryOutcomeMissedCall, OccurredAt: now}
	ensure := func(command work.EnsureRecoveryTaskCommand) (work.Task, error) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return work.Task{}, err
		}
		defer tx.Rollback(ctx)
		result, err := module.EnsureRecoveryTask(ctx, tx, command)
		if err != nil {
			return work.Task{}, err
		}
		return result, tx.Commit(ctx)
	}
	results := make(chan error, 8)
	for i := range 8 {
		go func() {
			candidate := command
			if i%2 == 0 {
				candidate.Outcome = work.RecoveryOutcomeVoicemail
			}
			result, err := ensure(candidate)
			if err == nil && result.ID != first.ID {
				err = fmt.Errorf("attached to wrong Task: %s", result.ID)
			}
			results <- err
		}()
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	// An explicit reference cannot steal an already attached Call.
	wrong := command
	wrong.TaskID = second.ID
	if _, err := ensure(wrong); !errors.Is(err, work.ErrConflict) {
		t.Fatalf("reassignment: %v", err)
	}
	var tasks, attachments, activities int
	var attachmentActor string
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM work_tasks), (SELECT count(*) FROM work_task_interactions),
 (SELECT count(*) FROM work_task_activities WHERE kind = 'INTERACTION_ATTACHED')`).Scan(&tasks, &attachments, &activities); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT actor_subject FROM work_task_activities WHERE kind = 'INTERACTION_ATTACHED'`).Scan(&attachmentActor); err != nil {
		t.Fatal(err)
	}
	if attachmentActor != "human-calling" {
		t.Fatalf("attachment attributed to %s", attachmentActor)
	}
	if tasks != 2 || attachments != 1 || activities != 1 {
		t.Fatalf("Tasks=%d attachments=%d activities=%d", tasks, attachments, activities)
	}
	// Completion before the first recovery fact preserves completion and attaches evidence once.
	completed, err := module.CompleteTask(ctx, work.CompleteTaskCommand{Identity: identity, TaskID: second.ID, ExpectedVersion: second.Version})
	if err != nil {
		t.Fatal(err)
	}
	command.CallID = insertCall(t, pool, authorization, now.Add(time.Minute))
	command.TaskID = second.ID
	attached, err := ensure(command)
	if err != nil {
		t.Fatal(err)
	}
	if attached.State != work.TaskCompleted || attached.Version != completed.Version+1 {
		t.Fatalf("completed need changed: %#v", attached)
	}
	// A missing reference is a separate need, even with AI Tasks on the same phone.
	command.CallID = insertCall(t, pool, authorization, now.Add(2*time.Minute))
	command.TaskID = uuid.NewString()
	if _, err := ensure(command); !errors.Is(err, work.ErrConflict) {
		t.Fatalf("missing explicit Task: %v", err)
	}
	command.TaskID = ""
	separate, err := ensure(command)
	if err != nil {
		t.Fatal(err)
	}
	if separate.ID == first.ID || separate.ID == second.ID || separate.Origin != work.TaskOriginMissedCall {
		t.Fatalf("collapsed separate need: %#v", separate)
	}
}
