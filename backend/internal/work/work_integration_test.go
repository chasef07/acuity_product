package work_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEnsureCallFollowUpCreatesOneDurableOpenTask(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	callID := insertCall(t, pool, authorization, now)
	module := work.New(pool, accessModule, func() time.Time { return now })

	command := work.EnsureCallFollowUpCommand{
		CallID:     callID,
		PracticeID: authorization.Practice.ID,
		LocationID: authorization.Locations[0].ID,
		Phone:      "+15555550100",
		Reason:     "  Confirm surgery instructions  ",
		Creator:    authorization.Actor,
	}
	first := ensureCallFollowUp(t, pool, module, command)
	replayed := ensureCallFollowUp(t, pool, module, command)

	if replayed.ID != first.ID {
		t.Fatalf("replayed Task ID = %q, want %q", replayed.ID, first.ID)
	}
	task, err := module.ReadTask(context.Background(), identity, first.ID)
	if err != nil {
		t.Fatalf("read Task: %v", err)
	}
	if task.State != work.TaskOpen ||
		task.Title != "Confirm surgery instructions" ||
		task.Phone != "+15555550100" ||
		task.CallID != callID ||
		task.Version != 1 ||
		task.CreatedBy.Subject != identity.Subject ||
		task.CreatedBy.Email != identity.Email {
		t.Fatalf("created Task = %#v", task)
	}

	var activityCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM work_task_activities
		WHERE task_id = $1 AND kind = 'TASK_CREATED'
	`, task.ID).Scan(&activityCount); err != nil {
		t.Fatalf("count Task Activities: %v", err)
	}
	if activityCount != 1 {
		t.Fatalf("Task creation Activity count = %d, want 1", activityCount)
	}
	assertTaskAcknowledgementIntentCount(t, pool, task.ID, 0)
}

func TestEnsureRecoveryTaskCombinesCompatibleCallEvidence(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 4, 9, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	locationID := authorization.Locations[0].ID
	phone := "+15555550100"
	missedCallID := insertCallAt(
		t,
		pool,
		authorization,
		locationID,
		phone,
		"Missed caller",
		now,
	)
	voicemailCallID := insertCallAt(
		t,
		pool,
		authorization,
		locationID,
		phone,
		"Voicemail caller",
		now.Add(time.Minute),
	)

	ensureRecovery := func(command work.EnsureRecoveryTaskCommand) work.Task {
		t.Helper()
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin recovery Task transaction: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		task, err := module.EnsureRecoveryTask(
			context.Background(),
			tx,
			command,
		)
		if err != nil {
			t.Fatalf("ensure recovery Task: %v", err)
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatalf("commit recovery Task: %v", err)
		}
		return task
	}
	missed := ensureRecovery(work.EnsureRecoveryTaskCommand{
		CallID:     missedCallID,
		PracticeID: authorization.Practice.ID,
		LocationID: locationID,
		Phone:      phone,
		Outcome:    work.RecoveryOutcomeMissedCall,
		OccurredAt: now,
	})
	if missed.Origin != work.TaskOriginMissedCall {
		t.Fatalf("missed-call recovery Task = %#v", missed)
	}
	assertTaskAcknowledgementIntentCount(t, pool, missed.ID, 0)
	voicemail := ensureRecovery(work.EnsureRecoveryTaskCommand{
		CallID:     voicemailCallID,
		PracticeID: authorization.Practice.ID,
		LocationID: locationID,
		Phone:      phone,
		Outcome:    work.RecoveryOutcomeVoicemail,
		OccurredAt: now.Add(time.Minute),
	})

	if voicemail.ID != missed.ID ||
		voicemail.Title != "Review voicemail" ||
		voicemail.Origin != work.TaskOriginVoicemail ||
		voicemail.RecoveryOutcome != work.RecoveryOutcomeVoicemail {
		t.Fatalf("combined recovery Task = %#v, first = %#v", voicemail, missed)
	}
	var taskCount, interactionCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM work_tasks WHERE id = $1),
			(SELECT count(*) FROM work_task_interactions WHERE task_id = $1)
	`, missed.ID).Scan(&taskCount, &interactionCount); err != nil {
		t.Fatalf("read combined recovery evidence: %v", err)
	}
	if taskCount != 1 || interactionCount != 2 {
		t.Fatalf(
			"combined recovery evidence = %d Tasks, %d Interactions; want 1, 2",
			taskCount,
			interactionCount,
		)
	}
	read, err := module.ReadTask(context.Background(), identity, missed.ID)
	if err != nil {
		t.Fatalf("read recovery Task interactions: %v", err)
	}
	if read.RelatedInteractionCount != 2 || len(read.Interactions) != 2 ||
		read.Interactions[0].CallID != missedCallID ||
		read.Interactions[1].CallID != voicemailCallID {
		t.Fatalf("recovery Task read model = %#v", read)
	}

	now = now.Add(2 * time.Minute)
	completed, err := module.CompleteTask(context.Background(), work.CompleteTaskCommand{
		Identity: identity, TaskID: read.ID, ExpectedVersion: read.Version,
	})
	if err != nil {
		t.Fatalf("complete combined recovery Task: %v", err)
	}
	replayed := ensureRecovery(work.EnsureRecoveryTaskCommand{
		CallID:     voicemailCallID,
		PracticeID: authorization.Practice.ID,
		LocationID: locationID,
		Phone:      phone,
		Outcome:    work.RecoveryOutcomeVoicemail,
		OccurredAt: now,
	})
	if replayed.ID != completed.ID ||
		replayed.State != work.TaskCompleted ||
		replayed.Version != completed.Version {
		t.Fatalf("completed combined recovery replay = %#v, want %#v", replayed, completed)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM work_tasks
				WHERE practice_id = $1 AND location_id = $2 AND phone = $3),
			(SELECT count(*) FROM work_task_interactions WHERE task_id = $4)
	`, authorization.Practice.ID, locationID, phone, completed.ID).Scan(
		&taskCount,
		&interactionCount,
	); err != nil {
		t.Fatalf("read completed replay evidence: %v", err)
	}
	if taskCount != 1 || interactionCount != 2 {
		t.Fatalf(
			"completed recovery replay = %d Tasks, %d Interactions; want 1, 2",
			taskCount,
			interactionCount,
		)
	}
}

func assertTaskAcknowledgementIntentCount(
	t *testing.T,
	pool *pgxpool.Pool,
	taskID string,
	want int,
) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM work_task_acknowledgements
		WHERE task_id = $1
			AND purpose = 'CALLER_TASK_RECEIVED'
			AND state = 'PENDING'
	`, taskID).Scan(&count); err != nil {
		t.Fatalf("read automatic Task acknowledgement intent: %v", err)
	}
	if count != want {
		t.Fatalf("automatic Task acknowledgement intents = %d, want %d", count, want)
	}
}

func TestEnsureRecoveryTaskOrdersSamePhoneVoicemailsAndReplays(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	locationID := authorization.Locations[0].ID
	phone := "+15555550100"
	firstCallID := insertCallAt(
		t, pool, authorization, locationID, phone, "First caller", now,
	)
	secondCallID := insertCallAt(
		t, pool, authorization, locationID, phone, "Second caller", now.Add(time.Minute),
	)

	ensureRecovery := func(callID string, occurredAt time.Time) work.Task {
		t.Helper()
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin recovery Task transaction: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		task, err := module.EnsureRecoveryTask(
			context.Background(),
			tx,
			work.EnsureRecoveryTaskCommand{
				CallID: callID, PracticeID: authorization.Practice.ID,
				LocationID: locationID, Phone: phone,
				Outcome: work.RecoveryOutcomeVoicemail, OccurredAt: occurredAt,
			},
		)
		if err != nil {
			t.Fatalf("ensure recovery Task: %v", err)
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatalf("commit recovery Task: %v", err)
		}
		return task
	}

	first := ensureRecovery(firstCallID, now)
	assertTaskAcknowledgementIntentCount(t, pool, first.ID, 0)
	second := ensureRecovery(secondCallID, now.Add(time.Minute))
	replayed := ensureRecovery(secondCallID, now.Add(time.Minute))
	if second.ID != first.ID || replayed.ID != first.ID {
		t.Fatalf("same-phone recovery Tasks = first:%q second:%q replay:%q",
			first.ID, second.ID, replayed.ID)
	}
	task, err := module.ReadTask(context.Background(), identity, first.ID)
	if err != nil {
		t.Fatalf("read same-phone voicemail Task: %v", err)
	}
	if task.Version != 2 || task.RelatedInteractionCount != 2 ||
		len(task.Interactions) != 2 ||
		task.Interactions[0].CallID != firstCallID ||
		task.Interactions[1].CallID != secondCallID {
		t.Fatalf("same-phone voicemail Task = %#v", task)
	}

	rows, err := pool.Query(context.Background(), `
		SELECT task_version, kind
		FROM work_task_activities
		WHERE task_id = $1
		ORDER BY task_version
	`, task.ID)
	if err != nil {
		t.Fatalf("read same-phone voicemail Activities: %v", err)
	}
	defer rows.Close()
	type activity struct {
		version int64
		kind    string
	}
	activities := []activity{}
	for rows.Next() {
		var item activity
		if err := rows.Scan(&item.version, &item.kind); err != nil {
			t.Fatalf("scan same-phone voicemail Activity: %v", err)
		}
		activities = append(activities, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate same-phone voicemail Activities: %v", err)
	}
	want := []activity{{version: 1, kind: "TASK_CREATED"}, {version: 2, kind: "INTERACTION_ATTACHED"}}
	if len(activities) != len(want) {
		t.Fatalf("same-phone voicemail Activities = %#v, want %#v", activities, want)
	}
	for index := range want {
		if activities[index] != want[index] {
			t.Fatalf("same-phone voicemail Activities = %#v, want %#v", activities, want)
		}
	}
}

func TestRecoveryTasksKeepDifferentSourcedCallersSeparate(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 13, 10, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	locationID := authorization.Locations[0].ID
	phone := "+15555550123"

	ensure := func(callerName string, occurredAt time.Time) work.Task {
		t.Helper()
		callID := insertCallAt(
			t, pool, authorization, locationID, phone, callerName, occurredAt,
		)
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin recovery Task: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		task, err := module.EnsureRecoveryTask(
			context.Background(),
			tx,
			work.EnsureRecoveryTaskCommand{
				CallID: callID, PracticeID: authorization.Practice.ID,
				LocationID: locationID, Phone: phone, CallerName: callerName,
				Outcome: work.RecoveryOutcomeVoicemail, OccurredAt: occurredAt,
			},
		)
		if err != nil {
			t.Fatalf("ensure recovery Task: %v", err)
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatalf("commit recovery Task: %v", err)
		}
		return task
	}

	alexFirst := ensure("Alex Patient", now)
	alexSecond := ensure("Alex Patient", now.Add(time.Minute))
	jordan := ensure("Jordan Patient", now.Add(2*time.Minute))
	if alexSecond.ID != alexFirst.ID || jordan.ID == alexFirst.ID {
		t.Fatalf(
			"caller-aware recovery Tasks = Alex %q/%q, Jordan %q",
			alexFirst.ID,
			alexSecond.ID,
			jordan.ID,
		)
	}
}

func TestResolveRecoveryTasksConvergesAcrossLocationsAndRespectsLaterWork(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 13, 11, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	phone := "+15555550124"

	ensure := func(locationID string, callAt time.Time) work.Task {
		t.Helper()
		callID := insertCallAt(
			t, pool, authorization, locationID, phone, "Shared caller", callAt,
		)
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin recovery Task: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		task, err := module.EnsureRecoveryTask(
			context.Background(),
			tx,
			work.EnsureRecoveryTaskCommand{
				CallID: callID, PracticeID: authorization.Practice.ID,
				LocationID: locationID, Phone: phone, CallerName: "Shared caller",
				Outcome: work.RecoveryOutcomeMissedCall, OccurredAt: callAt,
			},
		)
		if err != nil {
			t.Fatalf("ensure recovery Task: %v", err)
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatalf("commit recovery Task: %v", err)
		}
		return task
	}

	first := ensure(authorization.Locations[0].ID, now)
	second := ensure(authorization.Locations[1].ID, now.Add(time.Minute))
	resolutionAt := now.Add(2 * time.Minute)

	resolve := func(at time.Time, sourceID string) {
		t.Helper()
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin recovery resolution: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := module.ResolveRecoveryTasks(
			context.Background(),
			tx,
			work.ResolveRecoveryTasksCommand{
				PracticeID: authorization.Practice.ID,
				Phone:      phone,
				OccurredAt: at,
				Kind:       work.RecoveryResolutionInboundCall,
				SourceID:   sourceID,
			},
		); err != nil {
			t.Fatalf("resolve recovery Tasks: %v", err)
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatalf("commit recovery resolution: %v", err)
		}
	}

	resolve(resolutionAt, "connected-call")
	resolve(resolutionAt, "connected-call")
	for _, taskID := range []string{first.ID, second.ID} {
		resolved, err := module.ReadTask(context.Background(), identity, taskID)
		if err != nil || resolved.State != work.TaskCompleted ||
			resolved.CompletedBy == nil ||
			resolved.CompletedBy.Kind != access.ActorService {
			t.Fatalf("resolved recovery Task = %#v, %v", resolved, err)
		}
	}

	var activityCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM work_task_activities
		WHERE task_id = $1
			AND kind = 'TASK_AUTO_COMPLETED_INBOUND_CALL'
	`, first.ID).Scan(&activityCount); err != nil || activityCount != 1 {
		t.Fatalf("automatic completion Activities = %d, %v", activityCount, err)
	}

	now = resolutionAt.Add(time.Minute)
	reopened, err := module.ReopenTask(
		context.Background(),
		work.ReopenTaskCommand{
			Identity: identity, TaskID: first.ID, ExpectedVersion: 2,
		},
	)
	if err != nil {
		t.Fatalf("reopen automatically completed Task: %v", err)
	}
	resolve(resolutionAt, "connected-call")
	stillOpen, err := module.ReadTask(context.Background(), identity, first.ID)
	if err != nil || stillOpen.State != work.TaskOpen ||
		stillOpen.Version != reopened.Version {
		t.Fatalf("reopened recovery Task after stale replay = %#v, %v", stillOpen, err)
	}
	resolve(resolutionAt.Add(2*time.Minute), "new-connected-call")
	resolvedAgain, err := module.ReadTask(context.Background(), identity, first.ID)
	if err != nil || resolvedAgain.State != work.TaskCompleted ||
		resolvedAgain.Version != reopened.Version+1 {
		t.Fatalf("reopened recovery Task after new contact = %#v, %v", resolvedAgain, err)
	}
}

func TestRecoveryReconciliationProcessesOneQueuedKeyAndIsRestartable(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 16, 11, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	phone := "+15555550125"
	locationID := authorization.Locations[0].ID
	recoveryCallID := insertCallAt(
		t, pool, authorization, locationID, phone, "Recovery caller", now,
	)
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin recovery Task: %v", err)
	}
	task, err := module.EnsureRecoveryTask(
		context.Background(), tx, work.EnsureRecoveryTaskCommand{
			CallID: recoveryCallID, PracticeID: authorization.Practice.ID,
			LocationID: locationID, Phone: phone,
			Outcome: work.RecoveryOutcomeMissedCall, OccurredAt: now,
		},
	)
	if err != nil {
		t.Fatalf("ensure recovery Task: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit recovery Task: %v", err)
	}

	connectedAt := now.Add(10 * time.Minute)
	connectedCallID := insertCallAt(
		t, pool, authorization, locationID, phone, "Connected caller", connectedAt,
	)
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_calls
		SET direction = 'INBOUND'
		WHERE id = $1
	`, connectedCallID); err != nil {
		t.Fatalf("mark connected Call inbound: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO work_recovery_reconciliation_queue (
			practice_id, phone, enqueued_at
		) VALUES ($1, $2, $3)
	`, authorization.Practice.ID, phone, now); err != nil {
		t.Fatalf("queue recovery reconciliation: %v", err)
	}

	processed, err := module.ProcessNextRecoveryReconciliation(context.Background())
	if err != nil || !processed {
		t.Fatalf("process recovery reconciliation = %v, %v", processed, err)
	}
	resolved, err := module.ReadTask(context.Background(), identity, task.ID)
	if err != nil || resolved.State != work.TaskCompleted ||
		resolved.CompletedBy == nil ||
		resolved.CompletedBy.Kind != access.ActorService {
		t.Fatalf("reconciled recovery Task = %#v, %v", resolved, err)
	}
	processed, err = module.ProcessNextRecoveryReconciliation(context.Background())
	if err != nil || processed {
		t.Fatalf("replayed recovery reconciliation = %v, %v; want idle", processed, err)
	}
	var activityCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM work_task_activities
		WHERE task_id = $1
			AND kind = 'TASK_AUTO_COMPLETED_INBOUND_CALL'
	`, task.ID).Scan(&activityCount); err != nil || activityCount != 1 {
		t.Fatalf("reconciliation Activity count = %d, %v", activityCount, err)
	}
}

func TestCreateAITaskCommitsSourceAndReturnsSafeReplay(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	service := access.ServiceIdentity{
		Subject:       "abita-synthetic",
		PracticeID:    authorization.Practice.ID,
		LocationScope: access.LocationScopeAll,
		Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
	}
	command := work.CreateAITaskCommand{
		Service:        service,
		OfficeKey:      "spring-hill",
		OfficePhone:    "+17275919997",
		SourceCallID:   "source-call-1",
		IdempotencyKey: "staff_task_3f94a1",
		Phone:          "+17275551212",
		CallerName:     "Jane Doe",
		Summary:        "Caller needs an appointment.",
		Message:        "Caller asked the office to schedule an annual exam.",
		Category:       work.TaskCategoryAppointments,
		Urgency:        work.TaskUrgencyNormal,
	}

	var workspaceVersionBefore int64
	if err := pool.QueryRow(context.Background(), `
		SELECT workspace_version
		FROM access_practices
		WHERE id = $1
	`, authorization.Practice.ID).Scan(&workspaceVersionBefore); err != nil {
		t.Fatalf("read workspace version before AI Task: %v", err)
	}
	first, firstStatus, err := module.CreateAITask(context.Background(), command)
	if err != nil {
		t.Fatalf("create AI Task: %v", err)
	}
	var acknowledgementCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM work_task_acknowledgements
		WHERE task_id = $1
			AND purpose = 'CALLER_TASK_RECEIVED'
			AND state = 'PENDING'
	`, first.ID).Scan(&acknowledgementCount); err != nil {
		t.Fatalf("read automatic Task acknowledgement intent: %v", err)
	}
	if acknowledgementCount != 1 {
		t.Fatalf(
			"automatic Task acknowledgement intents = %d, want 1",
			acknowledgementCount,
		)
	}
	var workspaceVersionAfterCreate int64
	if err := pool.QueryRow(context.Background(), `
		SELECT workspace_version
		FROM access_practices
		WHERE id = $1
	`, authorization.Practice.ID).Scan(&workspaceVersionAfterCreate); err != nil {
		t.Fatalf("read workspace version after AI Task: %v", err)
	}
	replayed, replayStatus, err := module.CreateAITask(context.Background(), command)
	if err != nil {
		t.Fatalf("replay AI Task: %v", err)
	}
	if firstStatus != work.TaskCreated ||
		replayStatus != work.TaskDuplicate ||
		replayed.ID != first.ID {
		t.Fatalf(
			"AI Task receipts = (%q, %q, %q), want created, duplicate, %q",
			firstStatus,
			replayStatus,
			replayed.ID,
			first.ID,
		)
	}
	exactDuplicate := command
	exactDuplicate.IdempotencyKey = "staff_task_same_need_new_key"
	exact, exactStatus, err := module.CreateAITask(
		context.Background(),
		exactDuplicate,
	)
	if err != nil || exactStatus != work.TaskDuplicate || exact.ID != first.ID {
		t.Fatalf(
			"exact AI Task duplicate = %#v, status = %q, err = %v",
			exact,
			exactStatus,
			err,
		)
	}

	task, err := module.ReadTask(context.Background(), identity, first.ID)
	if err != nil {
		t.Fatalf("read AI Task: %v", err)
	}
	if task.Origin != work.TaskOriginAbitaAI ||
		task.CallID != "" ||
		task.Urgency != work.TaskUrgencyNormal ||
		task.Category != work.TaskCategoryAppointments ||
		task.SourceCallID != command.SourceCallID ||
		task.SourceMessage != command.Message ||
		task.CallerName != command.CallerName ||
		task.CreatedBy.Kind != access.ActorService ||
		task.CreatedBy.Subject != service.Subject ||
		task.CreatedBy.Email != "" {
		t.Fatalf("created AI Task = %#v", task)
	}
	sourceSearch, err := workspace.New(pool, accessModule).QueryTasks(
		context.Background(),
		workspace.QueryTasksCommand{
			Identity:   identity,
			PracticeID: authorization.Practice.ID,
			Search:     "annual exam",
		},
	)
	if err != nil {
		t.Fatalf("search AI Task source detail: %v", err)
	}
	if len(sourceSearch.Items) != 0 {
		t.Fatalf(
			"immutable AI source detail leaked into Task search: %#v",
			sourceSearch.Items,
		)
	}
	for _, search := range []string{
		"Jane Doe",
		authorization.Locations[0].Name,
		"appointments",
	} {
		page, err := workspace.New(pool, accessModule).QueryTasks(
			context.Background(),
			workspace.QueryTasksCommand{
				Identity: identity, PracticeID: authorization.Practice.ID,
				Search: search,
			},
		)
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != first.ID {
			t.Fatalf("search %q = %#v, %v", search, page.Items, err)
		}
	}

	var activityCount int
	var actorKind, actorSubject string
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*), min(actor_kind), min(actor_subject)
		FROM work_task_activities
		WHERE task_id = $1 AND kind = 'TASK_CREATED'
	`, task.ID).Scan(&activityCount, &actorKind, &actorSubject); err != nil {
		t.Fatalf("read AI Task creation Activity: %v", err)
	}
	if activityCount != 1 ||
		actorKind != string(access.ActorService) ||
		actorSubject != service.Subject {
		t.Fatalf(
			"AI Task creation Activity = (%d, %q, %q)",
			activityCount,
			actorKind,
			actorSubject,
		)
	}
	var workspaceVersionAfterReplay int64
	if err := pool.QueryRow(context.Background(), `
		SELECT workspace_version
		FROM access_practices
		WHERE id = $1
	`, authorization.Practice.ID).Scan(&workspaceVersionAfterReplay); err != nil {
		t.Fatalf("read workspace version after AI Task replay: %v", err)
	}
	if workspaceVersionAfterCreate != workspaceVersionBefore+1 ||
		workspaceVersionAfterReplay != workspaceVersionAfterCreate {
		t.Fatalf(
			"AI Task workspace versions = (%d, %d, %d)",
			workspaceVersionBefore,
			workspaceVersionAfterCreate,
			workspaceVersionAfterReplay,
		)
	}

	changed := command
	changed.Message = "Changed immutable request."
	if _, _, err := module.CreateAITask(
		context.Background(),
		changed,
	); !errors.Is(err, work.ErrConflict) {
		t.Fatalf("changed AI Task replay error = %v, want conflict", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE work_tasks
		SET source_message = 'Direct rewrite'
		WHERE id = $1
	`, task.ID); err == nil {
		t.Fatal("direct AI Task source rewrite unexpectedly succeeded")
	}
	unchanged, err := module.ReadTask(context.Background(), identity, task.ID)
	if err != nil {
		t.Fatalf("read AI Task after rejected rewrites: %v", err)
	}
	if unchanged.SourceMessage != command.Message || unchanged.Version != 1 {
		t.Fatalf("AI Task changed after rejected rewrites: %#v", unchanged)
	}
}

func TestCreateAITaskSupportsMultipleOutcomesAndConcurrentReplay(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	service := access.ServiceIdentity{
		Subject:       "abita-concurrent",
		PracticeID:    authorization.Practice.ID,
		LocationScope: access.LocationScopeAll,
		Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
	}
	command := work.CreateAITaskCommand{
		Service:        service,
		OfficeKey:      "spring-hill",
		OfficePhone:    "+17275919997",
		SourceCallID:   "source-call-shared",
		IdempotencyKey: "staff_task_concurrent",
		Phone:          "+17275551212",
		Summary:        "First accountable outcome",
		Message:        "Caller requested the first independent staff action.",
		Category:       work.TaskCategoryDocumentation,
		Urgency:        work.TaskUrgencyNormal,
	}

	type result struct {
		task   work.Task
		status work.TaskCreateStatus
		err    error
	}
	results := make(chan result, 4)
	var wait sync.WaitGroup
	for range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			task, status, err := module.CreateAITask(
				context.Background(),
				command,
			)
			results <- result{task: task, status: status, err: err}
		}()
	}
	wait.Wait()
	close(results)

	taskID := ""
	created := 0
	duplicates := 0
	for outcome := range results {
		if outcome.err != nil {
			t.Fatalf("concurrent AI Task replay: %v", outcome.err)
		}
		if taskID == "" {
			taskID = outcome.task.ID
		} else if outcome.task.ID != taskID {
			t.Fatalf(
				"concurrent AI Task IDs = %q and %q",
				taskID,
				outcome.task.ID,
			)
		}
		switch outcome.status {
		case work.TaskCreated:
			created++
		case work.TaskDuplicate:
			duplicates++
		default:
			t.Fatalf("concurrent AI Task status = %q", outcome.status)
		}
	}
	if created != 1 || duplicates != 3 {
		t.Fatalf(
			"concurrent AI Task receipts = %d created, %d duplicate",
			created,
			duplicates,
		)
	}

	second := command
	second.IdempotencyKey = "staff_task_second"
	second.Summary = "Second accountable outcome"
	second.Message = "Caller requested a separate staff action on the same call."
	secondTask, status, err := module.CreateAITask(
		context.Background(),
		second,
	)
	if err != nil ||
		status != work.TaskCreated ||
		secondTask.ID == taskID ||
		secondTask.SourceCallID != command.SourceCallID {
		t.Fatalf(
			"second AI Task = %#v, status = %q, err = %v",
			secondTask,
			status,
			err,
		)
	}

	var taskCount, activityCount, acknowledgementCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			count(DISTINCT task.id),
			count(activity.id),
			count(acknowledgement.id)
		FROM work_tasks task
		LEFT JOIN work_task_activities activity
			ON activity.task_id = task.id
			AND activity.kind = 'TASK_CREATED'
		LEFT JOIN work_task_acknowledgements acknowledgement
			ON acknowledgement.task_id = task.id
		WHERE task.created_by_subject = $1
	`, service.Subject).Scan(&taskCount, &activityCount, &acknowledgementCount); err != nil {
		t.Fatalf("count AI Task outcomes: %v", err)
	}
	if taskCount != 2 || activityCount != 2 || acknowledgementCount != 2 {
		t.Fatalf(
			"AI Task outcome counts = %d Tasks, %d Activities, %d acknowledgements",
			taskCount,
			activityCount,
			acknowledgementCount,
		)
	}
}

func TestCreateAITaskRejectsInvalidOrUnauthorizedCommandsWithoutEffects(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	command := work.CreateAITaskCommand{
		Service: access.ServiceIdentity{
			Subject:       "abita-denied",
			PracticeID:    authorization.Practice.ID,
			LocationScope: access.LocationScopeAll,
			Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
		},
		OfficeKey:      "spring-hill",
		OfficePhone:    "+17275919997",
		SourceCallID:   "source-call-denied",
		IdempotencyKey: "staff_task_denied",
		Phone:          "+17275551212",
		Summary:        "Caller needs staff help",
		Message:        "Caller supplied a complete request for staff.",
		Category:       work.TaskCategoryOther,
		Urgency:        work.TaskUrgencyNormal,
	}

	missingCapability := command
	missingCapability.Service.Capabilities = nil
	unknownOffice := command
	unknownOffice.OfficeKey = "unknown-office"
	unknownOffice.IdempotencyKey = "staff_task_unknown_office"
	wrongPractice := command
	wrongPractice.Service.PracticeID = uuid.NewString()
	wrongPractice.IdempotencyKey = "staff_task_wrong_practice"
	for name, candidate := range map[string]work.CreateAITaskCommand{
		"missing capability": missingCapability,
		"unknown office":     unknownOffice,
		"wrong Practice":     wrongPractice,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := module.CreateAITask(
				context.Background(),
				candidate,
			); !errors.Is(err, work.ErrDenied) {
				t.Fatalf("CreateAITask error = %v, want denied", err)
			}
		})
	}
	invalidPhone := command
	invalidPhone.Phone = "7275551212"
	if _, _, err := module.CreateAITask(
		context.Background(),
		invalidPhone,
	); !errors.Is(err, work.ErrInvalidInput) {
		t.Fatalf("invalid AI Task error = %v, want invalid input", err)
	}

	var taskCount, activityCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM work_tasks),
			(SELECT count(*) FROM work_task_activities)
	`).Scan(&taskCount, &activityCount); err != nil {
		t.Fatalf("count rejected AI Task effects: %v", err)
	}
	if taskCount != 0 || activityCount != 0 {
		t.Fatalf(
			"rejected AI Task effects = %d Tasks, %d Activities",
			taskCount,
			activityCount,
		)
	}
}

func TestCreateAITaskRollsBackEveryEffectWhenWorkspaceChangeFails(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	command := work.CreateAITaskCommand{
		Service: access.ServiceIdentity{
			Subject:       "abita-atomic",
			PracticeID:    authorization.Practice.ID,
			LocationScope: access.LocationScopeAll,
			Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
		},
		OfficeKey:      "spring-hill",
		OfficePhone:    "+17275919997",
		SourceCallID:   "source-call-atomic",
		IdempotencyKey: "staff_task_atomic",
		Phone:          "+17275551212",
		Summary:        "Caller needs atomic follow-up",
		Message:        "Caller supplied a request that must commit completely.",
		Category:       work.TaskCategoryOther,
		Urgency:        work.TaskUrgencyNormal,
	}
	if _, err := pool.Exec(context.Background(), `
		CREATE FUNCTION fail_ai_task_workspace_change()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			RAISE EXCEPTION 'injected workspace failure';
		END
		$$;
		CREATE TRIGGER fail_ai_task_workspace_change
		BEFORE UPDATE OF workspace_version ON access_practices
		FOR EACH ROW
		EXECUTE FUNCTION fail_ai_task_workspace_change();
	`); err != nil {
		t.Fatalf("install AI Task workspace failure: %v", err)
	}
	if _, _, err := module.CreateAITask(
		context.Background(),
		command,
	); err == nil {
		t.Fatal("AI Task creation unexpectedly survived workspace failure")
	}
	var taskCount, activityCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM work_tasks),
			(SELECT count(*) FROM work_task_activities)
	`).Scan(&taskCount, &activityCount); err != nil {
		t.Fatalf("count rolled-back AI Task effects: %v", err)
	}
	if taskCount != 0 || activityCount != 0 {
		t.Fatalf(
			"failed AI Task effects = %d Tasks, %d Activities",
			taskCount,
			activityCount,
		)
	}
	if _, err := pool.Exec(context.Background(), `
		DROP TRIGGER fail_ai_task_workspace_change ON access_practices;
		DROP FUNCTION fail_ai_task_workspace_change();
	`); err != nil {
		t.Fatalf("remove AI Task workspace failure: %v", err)
	}
	task, status, err := module.CreateAITask(context.Background(), command)
	if err != nil || status != work.TaskCreated || task.ID == "" {
		t.Fatalf(
			"AI Task recovery = %#v, status = %q, err = %v",
			task,
			status,
			err,
		)
	}
}

func TestTaskLifecycleRejectsStaleRenameAndKeepsCompletionRecoverable(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	callID := insertCall(t, pool, authorization, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	task := ensureCallFollowUp(t, pool, module, work.EnsureCallFollowUpCommand{
		CallID:     callID,
		PracticeID: authorization.Practice.ID,
		LocationID: authorization.Locations[0].ID,
		Phone:      "+15555550100",
		Reason:     "Confirm surgery instructions",
		Creator:    authorization.Actor,
	})

	now = now.Add(time.Minute)
	renamed, err := module.RenameTask(context.Background(), work.RenameTaskCommand{
		Identity:        identity,
		TaskID:          task.ID,
		ExpectedVersion: task.Version,
		Title:           "  Confirm post-op instructions  ",
	})
	if err != nil {
		t.Fatalf("rename Task: %v", err)
	}
	if renamed.Title != "Confirm post-op instructions" || renamed.Version != 2 {
		t.Fatalf("renamed Task = %#v", renamed)
	}

	stale, err := module.RenameTask(context.Background(), work.RenameTaskCommand{
		Identity:        identity,
		TaskID:          task.ID,
		ExpectedVersion: task.Version,
		Title:           "Overwrite committed work",
	})
	if !errors.Is(err, work.ErrConflict) {
		t.Fatalf("stale rename error = %v, want conflict", err)
	}
	if stale.Title != renamed.Title || stale.Version != renamed.Version {
		t.Fatalf("stale rename current Task = %#v, want %#v", stale, renamed)
	}

	now = now.Add(time.Minute)
	completed, err := module.CompleteTask(context.Background(), work.CompleteTaskCommand{
		Identity:        identity,
		TaskID:          task.ID,
		ExpectedVersion: renamed.Version,
	})
	if err != nil {
		t.Fatalf("complete Task: %v", err)
	}
	if completed.State != work.TaskCompleted ||
		completed.Version != 3 ||
		completed.CompletedBy == nil ||
		completed.CompletedBy.Kind != access.ActorHuman ||
		completed.CompletedBy.Subject != identity.Subject ||
		completed.CompletedBy.Email != identity.Email ||
		completed.CompletedAt == nil ||
		!completed.CompletedAt.Equal(now) {
		t.Fatalf("completed Task = %#v", completed)
	}
	replayedCompletion, err := module.CompleteTask(
		context.Background(),
		work.CompleteTaskCommand{
			Identity:        identity,
			TaskID:          task.ID,
			ExpectedVersion: renamed.Version,
		},
	)
	if err != nil || replayedCompletion.Version != completed.Version {
		t.Fatalf("replayed completion = %#v, err = %v", replayedCompletion, err)
	}
	if _, err := module.RenameTask(context.Background(), work.RenameTaskCommand{
		Identity:        identity,
		TaskID:          task.ID,
		ExpectedVersion: completed.Version,
		Title:           "Completed work must not change",
	}); !errors.Is(err, work.ErrConflict) {
		t.Fatalf("completed rename error = %v, want conflict", err)
	}

	now = now.Add(time.Minute)
	reopened, err := module.ReopenTask(context.Background(), work.ReopenTaskCommand{
		Identity:        identity,
		TaskID:          task.ID,
		ExpectedVersion: completed.Version,
	})
	if err != nil {
		t.Fatalf("reopen Task: %v", err)
	}
	if reopened.State != work.TaskOpen ||
		reopened.Version != 4 ||
		reopened.CompletedBy != nil ||
		reopened.CompletedAt != nil ||
		reopened.Title != renamed.Title ||
		reopened.Phone != task.Phone ||
		reopened.CallID != task.CallID {
		t.Fatalf("reopened Task = %#v", reopened)
	}
	replayedReopen, err := module.ReopenTask(
		context.Background(),
		work.ReopenTaskCommand{
			Identity:        identity,
			TaskID:          task.ID,
			ExpectedVersion: completed.Version,
		},
	)
	if err != nil || replayedReopen.Version != reopened.Version {
		t.Fatalf("replayed reopen = %#v, err = %v", replayedReopen, err)
	}

	var activityCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM work_task_activities
		WHERE task_id = $1
	`, task.ID).Scan(&activityCount); err != nil {
		t.Fatalf("count lifecycle Activities: %v", err)
	}
	if activityCount != 4 {
		t.Fatalf("Task Activity count = %d, want 4", activityCount)
	}
}

func TestWorkspaceQueryTasksPreservesScopedQueueOrderingSearchAndCursor(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	writes := work.New(pool, accessModule, func() time.Time { return now })
	reads := workspace.New(pool, accessModule)

	openOld := createTask(
		t, pool, writes, authorization, authorization.Locations[0].ID,
		"+19851230001", "Verify referral", now,
	)
	now = now.Add(time.Minute)
	openNew := createTask(
		t, pool, writes, authorization, authorization.Locations[1].ID,
		"+19851230002", "Confirm surgery instructions", now,
	)
	now = now.Add(time.Minute)
	completedOld := createTask(
		t, pool, writes, authorization, authorization.Locations[0].ID,
		"+19851230003", "Send records", now,
	)
	now = now.Add(time.Minute)
	completedOld, err := writes.CompleteTask(context.Background(), work.CompleteTaskCommand{
		Identity: identity, TaskID: completedOld.ID, ExpectedVersion: completedOld.Version,
	})
	if err != nil {
		t.Fatalf("complete older Task: %v", err)
	}
	now = now.Add(time.Minute)
	completedNew := createTask(
		t, pool, writes, authorization, authorization.Locations[1].ID,
		"+19851230004", "Confirm pharmacy", now,
	)
	now = now.Add(time.Minute)
	completedNew, err = writes.CompleteTask(context.Background(), work.CompleteTaskCommand{
		Identity: identity, TaskID: completedNew.ID, ExpectedVersion: completedNew.Version,
	})
	if err != nil {
		t.Fatalf("complete newer Task: %v", err)
	}

	firstPage, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID, Limit: 2,
	})
	if err != nil {
		t.Fatalf("query first Task page: %v", err)
	}
	assertTaskIDs(t, firstPage.Items, openOld.ID, openNew.ID)
	if firstPage.NextCursor != "" {
		t.Fatalf("last open Task page cursor = %q, want empty", firstPage.NextCursor)
	}
	completedPage, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		State: work.TaskCompleted, Limit: 2,
	})
	if err != nil {
		t.Fatalf("query completed Task page: %v", err)
	}
	assertTaskIDs(t, completedPage.Items, completedNew.ID, completedOld.ID)
	if completedPage.NextCursor != "" {
		t.Fatalf("last completed Task page cursor = %q, want empty", completedPage.NextCursor)
	}

	locationPage, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		LocationID: authorization.Locations[0].ID,
	})
	if err != nil {
		t.Fatalf("query Location Tasks: %v", err)
	}
	assertTaskIDs(t, locationPage.Items, openOld.ID)
	completedLocationPage, err := reads.QueryTasks(
		context.Background(),
		workspace.QueryTasksCommand{
			Identity: identity, PracticeID: authorization.Practice.ID,
			LocationID: authorization.Locations[0].ID, State: work.TaskCompleted,
		},
	)
	if err != nil {
		t.Fatalf("query completed Location Tasks: %v", err)
	}
	assertTaskIDs(t, completedLocationPage.Items, completedOld.ID)

	titlePage, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID, Search: "SURGERY",
	})
	if err != nil {
		t.Fatalf("search Task title: %v", err)
	}
	assertTaskIDs(t, titlePage.Items, openNew.ID)
	phonePage, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID, Search: "(985) 123-0002",
	})
	if err != nil {
		t.Fatalf("search Task phone: %v", err)
	}
	assertTaskIDs(t, phonePage.Items, openNew.ID)
	for _, literal := range []string{"%", "_"} {
		literalPage, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
			Identity: identity, PracticeID: authorization.Practice.ID, Search: literal,
		})
		if err != nil {
			t.Fatalf("search literal %q: %v", literal, err)
		}
		assertTaskIDs(t, literalPage.Items)
	}
}

func TestWorkspaceQueryTasksLoadsRelatedInteractionCountsInOneQuery(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	writes := work.New(pool, accessModule, func() time.Time { return now })

	tasks := make([]work.Task, 3)
	for index := range tasks {
		tasks[index] = createTask(
			t, pool, writes, authorization, authorization.Locations[0].ID,
			fmt.Sprintf("+1985555010%d", index),
			fmt.Sprintf("Query count Task %d", index),
			now.Add(time.Duration(index)*time.Minute),
		)
	}
	attachTaskInteractionFixture(t, pool, tasks[0])

	tracer := &workInteractionQueryTracer{}
	reads := newTracedWorkspaceModule(t, pool, tracer, now)
	page, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		Ordering: work.TaskOrderingTime,
	})
	if err != nil {
		t.Fatalf("query Tasks with related Interaction counts: %v", err)
	}
	if len(page.Items) != len(tasks) {
		t.Fatalf("queried Tasks = %d, want %d", len(page.Items), len(tasks))
	}
	wantCounts := map[string]int{tasks[0].ID: 1, tasks[1].ID: 0, tasks[2].ID: 0}
	for _, task := range page.Items {
		want, ok := wantCounts[task.ID]
		if !ok {
			t.Fatalf("queried unexpected Task %q", task.ID)
		}
		if task.RelatedInteractionCount != want {
			t.Fatalf("Task %q related Interaction count = %d, want %d",
				task.ID, task.RelatedInteractionCount, want)
		}
		delete(wantCounts, task.ID)
	}
	if got := tracer.interactionQueries.Load(); got != 1 {
		t.Fatalf("Workspace QueryTasks Interaction queries = %d, want 1", got)
	}
}

func TestWorkspaceQueryTasksReturnsStableAuthoritativeCountsAcrossPages(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	writes := work.New(pool, accessModule, func() time.Time { return now })
	reads := workspace.New(pool, accessModule)
	service := access.ServiceIdentity{
		Subject: "abita-counts", PracticeID: authorization.Practice.ID,
		LocationScope: access.LocationScopeAll,
		Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
	}
	for index := range 55 {
		key := fmt.Sprintf("count-%02d", index)
		if _, _, err := writes.CreateAITask(context.Background(), work.CreateAITaskCommand{
			Service: service, OfficeKey: "spring-hill", OfficePhone: "+17275919997",
			SourceCallID: "source-" + key, IdempotencyKey: "staff_task_" + key,
			Phone: "+17275551212", Summary: "Review request " + key,
			Message:  "Caller supplied a complete request for " + key + ".",
			Category: work.TaskCategoryOther, Urgency: work.TaskUrgencyNormal,
		}); err != nil {
			t.Fatalf("create Task %d: %v", index, err)
		}
		now = now.Add(time.Second)
	}
	for index, title := range []string{
		"Book annual exam", "Cancel annual exam", "Reschedule annual exam",
	} {
		key := fmt.Sprintf("appointment-count-%d", index)
		if _, _, err := writes.CreateAITask(context.Background(), work.CreateAITaskCommand{
			Service: service, OfficeKey: "spring-hill", OfficePhone: "+17275919997",
			SourceCallID: "source-" + key, IdempotencyKey: "staff_task_" + key,
			Phone: "+17275551212", Summary: title,
			Message:  "Caller requested: " + title + ".",
			Category: work.TaskCategoryAppointments, Urgency: work.TaskUrgencyNormal,
		}); err != nil {
			t.Fatalf("create appointment Task %d: %v", index, err)
		}
		now = now.Add(time.Second)
	}

	firstPage, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID, Limit: 50,
	})
	if err != nil {
		t.Fatalf("query first Task page: %v", err)
	}
	if len(firstPage.Items) != 50 || firstPage.NextCursor == "" {
		t.Fatalf("first Task page = %d items, cursor %q; want 50 items and another page",
			len(firstPage.Items), firstPage.NextCursor)
	}
	wantCounts := work.TaskFolderCounts{
		Tasks:      58,
		Categories: work.TaskCategoryCounts{Appointments: 3, Other: 55},
	}
	if firstPage.Counts != wantCounts {
		t.Fatalf("first Task page counts = %#v, want %#v", firstPage.Counts, wantCounts)
	}
	secondPage, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		Cursor: firstPage.NextCursor, Limit: 50,
	})
	if err != nil {
		t.Fatalf("query second Task page: %v", err)
	}
	if len(secondPage.Items) != 8 || secondPage.NextCursor != "" {
		t.Fatalf("second Task page = %d items, cursor %q; want 8 items and no cursor",
			len(secondPage.Items), secondPage.NextCursor)
	}
	if secondPage.Counts != wantCounts {
		t.Fatalf("second Task page counts = %#v, want %#v", secondPage.Counts, wantCounts)
	}
}

func TestWorkspaceQueryTasksOrdersOpenWorkByPriorityOnlyWhenRequested(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	writes := work.New(pool, accessModule, func() time.Time { return now })
	reads := workspace.New(pool, accessModule)
	service := access.ServiceIdentity{
		Subject: "abita-priority", PracticeID: authorization.Practice.ID,
		LocationScope: access.LocationScopeAll,
		Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
	}
	createAI := func(key, title string, urgency work.TaskUrgency) work.Task {
		t.Helper()
		task, _, err := writes.CreateAITask(context.Background(), work.CreateAITaskCommand{
			Service: service, OfficeKey: "spring-hill", OfficePhone: "+17275919997",
			SourceCallID: "source-" + key, IdempotencyKey: "staff_task_" + key,
			Phone: "+17275551212", Summary: title,
			Message:  "Caller supplied the complete request for " + title + ".",
			Category: work.TaskCategoryOther, Urgency: urgency,
		})
		if err != nil {
			t.Fatalf("create %s AI Task: %v", key, err)
		}
		return task
	}
	normalOld := createAI("normal-old", "Normal old", work.TaskUrgencyNormal)
	now = now.Add(time.Minute)
	nonUrgent := createAI("non-urgent", "Non-urgent", work.TaskUrgencyNonUrgent)
	now = now.Add(time.Minute)
	high := createAI("high", "High priority", work.TaskUrgencyHighPriority)
	now = now.Add(time.Minute)
	normalNew := createAI("normal-new", "Normal new", work.TaskUrgencyNormal)

	timePage, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		Ordering: work.TaskOrderingTime,
	})
	if err != nil {
		t.Fatalf("query time-ordered Tasks: %v", err)
	}
	assertTaskIDs(t, timePage.Items, normalOld.ID, nonUrgent.ID, high.ID, normalNew.ID)
	priorityPage, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		Ordering: work.TaskOrderingPriority,
	})
	if err != nil {
		t.Fatalf("query priority-ordered Tasks: %v", err)
	}
	assertTaskIDs(t, priorityPage.Items, high.ID, normalOld.ID, normalNew.ID, nonUrgent.ID)
	firstPriorityPage, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		Ordering: work.TaskOrderingPriority, Limit: 2,
	})
	if err != nil {
		t.Fatalf("query first priority cursor page: %v", err)
	}
	assertTaskIDs(t, firstPriorityPage.Items, high.ID, normalOld.ID)
	if firstPriorityPage.NextCursor == "" {
		t.Fatal("first priority cursor page has no next cursor")
	}
	secondPriorityPage, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		Ordering: work.TaskOrderingPriority,
		Cursor:   firstPriorityPage.NextCursor, Limit: 2,
	})
	if err != nil {
		t.Fatalf("query second priority cursor page: %v", err)
	}
	assertTaskIDs(t, secondPriorityPage.Items, normalNew.ID, nonUrgent.ID)
}

func TestReadTaskDerivesRelatedInteractionCountFromLoadedInteractions(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 15, 13, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	task := createTask(
		t,
		pool,
		module,
		authorization,
		authorization.Locations[0].ID,
		"+19855550200",
		"Read count Task",
		now,
	)
	attachTaskInteractionFixture(t, pool, task)

	tracer := &workInteractionQueryTracer{}
	tracedModule := newTracedWorkModule(t, pool, tracer, now)
	read, err := tracedModule.ReadTask(context.Background(), identity, task.ID)
	if err != nil {
		t.Fatalf("read Task with related Interactions: %v", err)
	}
	if read.RelatedInteractionCount != 1 || len(read.Interactions) != 1 ||
		read.Interactions[0].CallID != task.CallID {
		t.Fatalf("read Task related Interactions = %#v", read)
	}
	if got := tracer.interactionQueries.Load(); got != 1 {
		t.Fatalf("ReadTask Interaction queries = %d, want 1", got)
	}
}

func TestConcurrentCompletionAndReopenCommitOneActivityPerTransition(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, identity := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	task := createTask(
		t,
		pool,
		module,
		authorization,
		authorization.Locations[0].ID,
		"+19855550100",
		"Concurrent follow-up",
		now,
	)

	runConcurrent := func(action func() (work.Task, error)) []work.Task {
		t.Helper()
		start := make(chan struct{})
		results := make(chan work.Task, 2)
		errorsFound := make(chan error, 2)
		var group sync.WaitGroup
		for range 2 {
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				result, err := action()
				if err != nil {
					errorsFound <- err
					return
				}
				results <- result
			}()
		}
		close(start)
		group.Wait()
		close(results)
		close(errorsFound)
		for err := range errorsFound {
			t.Fatalf("concurrent Task transition: %v", err)
		}
		collected := []work.Task{}
		for result := range results {
			collected = append(collected, result)
		}
		if len(collected) != 2 {
			t.Fatalf("concurrent Task results = %#v", collected)
		}
		return collected
	}

	now = now.Add(time.Minute)
	completed := runConcurrent(func() (work.Task, error) {
		return module.CompleteTask(
			context.Background(),
			work.CompleteTaskCommand{
				Identity:        identity,
				TaskID:          task.ID,
				ExpectedVersion: task.Version,
			},
		)
	})
	if completed[0].Version != 2 || completed[1].Version != 2 ||
		completed[0].CompletedBy == nil || completed[1].CompletedBy == nil ||
		completed[0].CompletedBy.Subject != completed[1].CompletedBy.Subject {
		t.Fatalf("concurrent completion results = %#v", completed)
	}

	now = now.Add(time.Minute)
	reopened := runConcurrent(func() (work.Task, error) {
		return module.ReopenTask(
			context.Background(),
			work.ReopenTaskCommand{
				Identity:        identity,
				TaskID:          task.ID,
				ExpectedVersion: completed[0].Version,
			},
		)
	})
	if reopened[0].Version != 3 || reopened[1].Version != 3 ||
		reopened[0].State != work.TaskOpen || reopened[1].State != work.TaskOpen {
		t.Fatalf("concurrent reopen results = %#v", reopened)
	}

	var completionCount, reopenCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			count(*) FILTER (WHERE kind = 'TASK_COMPLETED'),
			count(*) FILTER (WHERE kind = 'TASK_REOPENED')
		FROM work_task_activities
		WHERE task_id = $1
	`, task.ID).Scan(&completionCount, &reopenCount); err != nil {
		t.Fatalf("count concurrent Task Activities: %v", err)
	}
	if completionCount != 1 || reopenCount != 1 {
		t.Fatalf(
			"completion Activities = %d, reopen Activities = %d, want 1 each",
			completionCount,
			reopenCount,
		)
	}
}

func TestPlatformOperatorReadsAndMutatesGloballyWithAuditedIdentity(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(
		context.Background(),
		access.Provisioning{
			Environment:       "test",
			RequestedBy:       "work-operator-test",
			PlatformOperators: []string{"operator@acuity.test"},
			Practices: []access.PracticeProvision{{
				Key:       "operator-practice",
				Name:      "Operator Practice",
				Locations: []access.LocationProvision{{Key: "office", Name: "Office"}},
				AccessGrants: []access.AccessGrantProvision{{
					Key:           "staff",
					Email:         "staff@operator.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
				}},
			}},
		},
	)
	if err != nil {
		t.Fatalf("provision operator Task fixture: %v", err)
	}
	staffIdentity := access.Identity{
		Subject:       "operator-fixture-staff",
		Email:         "staff@operator.test",
		EmailVerified: true,
	}
	staff := testaccess.Activate(t, accessModule, staffIdentity)
	module := work.New(pool, accessModule, func() time.Time { return now })
	task := createTask(
		t,
		pool,
		module,
		staff,
		staff.Locations[0].ID,
		"+19855550199",
		"Review supported follow-up",
		now,
	)
	operator := access.Identity{
		Subject:       "operator-subject",
		Email:         "operator@acuity.test",
		EmailVerified: true,
	}

	page, err := workspace.New(pool, accessModule).QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity:   operator,
		PracticeID: staff.Practice.ID,
	})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != task.ID {
		t.Fatalf("operator Task read = %#v, err = %v", page, err)
	}
	renamed, err := module.RenameTask(context.Background(), work.RenameTaskCommand{
		Identity:        operator,
		TaskID:          task.ID,
		ExpectedVersion: task.Version,
		Title:           "Operator follow-up",
	})
	if err != nil || renamed.CreatedBy.Subject != staffIdentity.Subject {
		t.Fatalf("operator Task rename = %#v, err = %v", renamed, err)
	}
	completed, err := module.CompleteTask(
		context.Background(),
		work.CompleteTaskCommand{
			Identity:        operator,
			TaskID:          task.ID,
			ExpectedVersion: renamed.Version,
		},
	)
	if err != nil {
		t.Fatalf("operator Task completion: %v", err)
	}
	reopened, err := module.ReopenTask(
		context.Background(),
		work.ReopenTaskCommand{
			Identity:        operator,
			TaskID:          task.ID,
			ExpectedVersion: completed.Version,
		},
	)
	if err != nil {
		t.Fatalf("operator Task reopen: %v", err)
	}

	auditEvents, err := accessModule.AuditTrail(
		context.Background(),
		operator,
		staff.Practice.ID,
	)
	if err != nil {
		t.Fatalf("load operator Task audit trail: %v", err)
	}
	expectedVersions := map[string]int64{
		"task.title_changed": renamed.Version,
		"task.completed":     completed.Version,
		"task.reopened":      reopened.Version,
	}
	for _, event := range auditEvents {
		version, expected := expectedVersions[event.Action]
		if !expected {
			continue
		}
		if event.ActorSubject != operator.Subject {
			t.Fatalf("operator Task audit = %#v", event)
		}
		var resourceType string
		var taskID string
		var taskVersion int64
		if err := pool.QueryRow(context.Background(), `
			SELECT
				details ->> 'resourceType',
				details ->> 'resourceId',
				(details ->> 'resourceVersion')::bigint
			FROM access_audit_events
			WHERE id = $1
		`, event.ID).Scan(&resourceType, &taskID, &taskVersion); err != nil {
			t.Fatalf("load operator Task audit details: %v", err)
		}
		if resourceType != "task" ||
			taskID != task.ID ||
			taskVersion != version {
			t.Fatalf(
				"operator Task audit details = (%q, %q, %d), want (%q, %q, %d)",
				resourceType,
				taskID,
				taskVersion,
				"task",
				task.ID,
				version,
			)
		}
		delete(expectedVersions, event.Action)
	}
	if len(expectedVersions) != 0 {
		t.Fatalf("missing operator Task audit actions = %#v", expectedVersions)
	}
}

func TestOperatorTaskAuditFailureRollsBackMutation(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	staff, staffIdentity := provisionStaff(t, accessModule, now)
	module := work.New(pool, accessModule, func() time.Time { return now })
	task := createTask(
		t,
		pool,
		module,
		staff,
		staff.Locations[0].ID,
		"+19855550200",
		"Keep this title",
		now,
	)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO access_platform_operators (email)
		VALUES ('operator@acuity.test')
	`); err != nil {
		t.Fatalf("provision rollback-test operator: %v", err)
	}
	operator := access.Identity{
		Subject:       "rollback-test-operator",
		Email:         "operator@acuity.test",
		EmailVerified: true,
	}
	if _, err := pool.Exec(context.Background(), `
		CREATE FUNCTION reject_operator_task_audit()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			RAISE EXCEPTION 'synthetic operator Task audit failure';
		END
		$$;

		CREATE TRIGGER reject_operator_task_audit
		BEFORE INSERT ON access_audit_events
		FOR EACH ROW
		WHEN (NEW.action LIKE 'task.%')
		EXECUTE FUNCTION reject_operator_task_audit()
	`); err != nil {
		t.Fatalf("install operator Task audit failure: %v", err)
	}

	_, err := module.RenameTask(context.Background(), work.RenameTaskCommand{
		Identity:        operator,
		TaskID:          task.ID,
		ExpectedVersion: task.Version,
		Title:           "Must roll back",
	})
	if err == nil {
		t.Fatal("operator Task rename succeeded without its audit")
	}
	current, err := module.ReadTask(context.Background(), staffIdentity, task.ID)
	if err != nil {
		t.Fatalf("read Task after audit failure: %v", err)
	}
	if current.Title != task.Title || current.Version != task.Version {
		t.Fatalf("Task after audit failure = %#v, want %#v", current, task)
	}
	var activityCount, auditCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(
				SELECT count(*)
				FROM work_task_activities
				WHERE task_id = $1 AND task_version > $2
			),
			(
				SELECT count(*)
				FROM access_audit_events
				WHERE action LIKE 'task.%'
					AND details ->> 'resourceId' = $1::text
			)
	`, task.ID, task.Version).Scan(&activityCount, &auditCount); err != nil {
		t.Fatalf("count rolled-back Task evidence: %v", err)
	}
	if activityCount != 0 || auditCount != 0 {
		t.Fatalf(
			"rolled-back Task evidence = (%d Activities, %d audits)",
			activityCount,
			auditCount,
		)
	}
}

func ensureCallFollowUp(
	t *testing.T,
	pool interface {
		BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	},
	module *work.Module,
	command work.EnsureCallFollowUpCommand,
) work.Task {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin Task transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := module.EnsureCallFollowUp(ctx, tx, command)
	if err != nil {
		t.Fatalf("ensure Call follow-up: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit Task transaction: %v", err)
	}
	return task
}

func attachTaskInteractionFixture(
	t *testing.T,
	pool interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	},
	task work.Task,
) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO work_task_interactions (
			task_id,
			call_id,
			practice_id,
			location_id,
			occurred_at
		)
		VALUES ($1, $2, $3, $4, $5)
	`, task.ID, task.CallID, task.PracticeID, task.LocationID, task.CreatedAt); err != nil {
		t.Fatalf("attach Task Interaction fixture: %v", err)
	}
}

type workInteractionQueryTracer struct {
	interactionQueries atomic.Int64
}

func (tracer *workInteractionQueryTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	if strings.Contains(data.SQL, "work_task_interactions") {
		tracer.interactionQueries.Add(1)
	}
	return ctx
}

func (*workInteractionQueryTracer) TraceQueryEnd(
	context.Context,
	*pgx.Conn,
	pgx.TraceQueryEndData,
) {
}

func newTracedWorkModule(
	t *testing.T,
	pool *pgxpool.Pool,
	tracer pgx.QueryTracer,
	now time.Time,
) *work.Module {
	t.Helper()
	config, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse traced Work database config: %v", err)
	}
	config.ConnConfig.Tracer = tracer
	tracedPool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open traced Work database pool: %v", err)
	}
	t.Cleanup(tracedPool.Close)
	return work.New(
		tracedPool,
		access.New(tracedPool, func() time.Time { return now }),
		func() time.Time { return now },
	)
}

func newTracedWorkspaceModule(
	t *testing.T,
	pool *pgxpool.Pool,
	tracer pgx.QueryTracer,
	now time.Time,
) *workspace.Module {
	t.Helper()
	config, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse traced Workspace database config: %v", err)
	}
	config.ConnConfig.Tracer = tracer
	tracedPool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open traced Workspace database pool: %v", err)
	}
	t.Cleanup(tracedPool.Close)
	return workspace.New(
		tracedPool,
		access.New(tracedPool, func() time.Time { return now }),
	)
}

func provisionStaff(
	t *testing.T,
	module *access.Module,
	now time.Time,
) (access.Authorization, access.Identity) {
	t.Helper()
	_, err := module.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "work-lifecycle-test",
		Practices: []access.PracticeProvision{{
			Key:  "synthetic-practice",
			Name: "Synthetic Practice",
			Locations: []access.LocationProvision{
				{
					Key:             "synthetic-location-1",
					Name:            "Synthetic Location 1",
					AbitaOfficeKeys: []string{"spring-hill"},
				},
				{Key: "synthetic-location-2", Name: "Synthetic Location 2"},
			},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "synthetic-staff",
				Email:         "staff@synthetic.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision Staff: %v", err)
	}
	identity := access.Identity{
		Subject:       "synthetic-staff-subject",
		Email:         "staff@synthetic.test",
		EmailVerified: true,
	}
	authorization := testaccess.Activate(t, module, identity)
	return authorization, identity
}

func insertCall(
	t *testing.T,
	pool interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	},
	authorization access.Authorization,
	now time.Time,
) string {
	t.Helper()
	return insertCallAt(
		t,
		pool,
		authorization,
		authorization.Locations[0].ID,
		"+15555550100",
		"Confirm surgery instructions",
		now,
	)
}

func createTask(
	t *testing.T,
	pool interface {
		BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	},
	module *work.Module,
	authorization access.Authorization,
	locationID string,
	phone string,
	reason string,
	now time.Time,
) work.Task {
	t.Helper()
	callID := insertCallAt(
		t,
		pool,
		authorization,
		locationID,
		phone,
		reason,
		now,
	)
	return ensureCallFollowUp(t, pool, module, work.EnsureCallFollowUpCommand{
		CallID:     callID,
		PracticeID: authorization.Practice.ID,
		LocationID: locationID,
		Phone:      phone,
		Reason:     reason,
		Creator:    authorization.Actor,
	})
}

func insertCallAt(
	t *testing.T,
	pool interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	},
	authorization access.Authorization,
	locationID string,
	phone string,
	reason string,
	now time.Time,
) string {
	t.Helper()
	handoffID := uuid.NewString()
	callID := uuid.NewString()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_handoffs (
			id,
			service_subject,
			practice_id,
			location_id,
			source_call_id,
			idempotency_key,
			input_fingerprint,
			phone,
			phone_source,
			display_name,
			name_source,
			transfer_reason,
			reason_source,
			expires_at,
			consumed_at,
			created_at
		)
		VALUES ($1, 'abita-synthetic', $2, $3, $4, $5, $6,
			$7, 'Abita', 'Synthetic Caller', 'Abita',
			$8, 'Abita AI', $9, $10, $10)
	`, handoffID, authorization.Practice.ID, locationID,
		"source-"+callID, "idempotency-"+callID, []byte(callID),
		phone, reason, now.Add(time.Minute), now,
	); err != nil {
		t.Fatalf("insert handoff fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_calls (
			id,
			source_handoff_id,
			practice_id,
			location_id,
			disposition_at,
			disposition_actor_subject,
			disposition_outcome,
			terminal_outcome,
			caller_phone,
			ended_at,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'FOLLOW_UP_REQUIRED',
			'FOLLOW_UP_REQUIRED', $7, $5, $5, $5)
	`, callID, handoffID, authorization.Practice.ID, locationID,
		now, authorization.Actor.Subject, phone,
	); err != nil {
		t.Fatalf("insert Call fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, staff_subject, staff_session_id, state,
			provider_call_control_id, provider_call_leg_id, provider_call_session_id,
			answered_at, bridge_pending_at, bridged_at, ending_at, ended_at,
			created_at, updated_at
		) VALUES
			($1, 'CALLER', 1, NULL, NULL, 'ENDED', $2, $3, $4,
				$5, $5, $5, $5, $5, $5, $5),
			($1, 'STAFF', 1, $6, 'browser-session', 'ENDED', $7, $8, $4,
				$5, $5, $5, $5, $5, $5, $5)
	`, callID, "control-"+callID, "leg-"+callID, "session-"+callID, now,
		authorization.Actor.Subject, "staff-control-"+callID, "staff-leg-"+callID); err != nil {
		t.Fatalf("insert CallLeg fixtures: %v", err)
	}
	return callID
}

func assertTaskIDs(t *testing.T, tasks []work.Task, expected ...string) {
	t.Helper()
	if len(tasks) != len(expected) {
		t.Fatalf("Task count = %d, want %d: %#v", len(tasks), len(expected), tasks)
	}
	for index, task := range tasks {
		if task.ID != expected[index] {
			t.Fatalf("Task %d ID = %q, want %q", index, task.ID, expected[index])
		}
	}
}
