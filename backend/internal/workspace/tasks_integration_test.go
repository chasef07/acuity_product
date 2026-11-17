package workspace_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestQueryTasksPagesEveryOrderingAndSearches(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Date(2026, time.August, 15, 14, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	_, err := access.New(pool, func() time.Time { return now }).Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "workspace-queue-order-test",
		Practices: []access.PracticeProvision{{
			Key:  "workspace-queue-practice",
			Name: "Workspace Queue Practice",
			Locations: []access.LocationProvision{{
				Key:             "workspace-queue-location",
				Name:            "Workspace Queue Location",
				AbitaOfficeKeys: []string{"workspace-queue-office"},
			}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "workspace-queue-staff",
				Email:         "staff@workspace-queue.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}, {
				Key:           "workspace-queue-colleague",
				Email:         "colleague@workspace-queue.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision Workspace Queue fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "workspace-queue-staff-subject",
		Email:         "staff@workspace-queue.test",
		EmailVerified: true,
	}
	authorization := testaccess.Activate(t, accessModule, identity)
	testaccess.Activate(t, accessModule, access.Identity{
		Subject:       "workspace-queue-colleague-subject",
		Email:         "colleague@workspace-queue.test",
		EmailVerified: true,
	})
	workModule := work.New(db.As(testdb.Portal), accessModule, func() time.Time { return now })
	service := access.ServiceIdentity{
		Subject:       "workspace-queue-service",
		PracticeID:    authorization.Practice.ID,
		LocationScope: access.LocationScopeAll,
		Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
	}
	create := func(key, title string, urgency work.TaskUrgency, category work.TaskCategory) work.Task {
		t.Helper()
		task, status, err := workModule.CreateAITask(
			context.Background(),
			work.CreateAITaskCommand{
				Service:        service,
				OfficeKey:      "workspace-queue-office",
				OfficePhone:    "+17275550100",
				SourceCallID:   "workspace-queue-" + key,
				IdempotencyKey: "workspace-queue-" + key,
				Phone:          "+17275550199",
				Summary:        title,
				Message:        "Queue ordering fixture",
				Category:       category,
				Urgency:        urgency,
			},
		)
		if err != nil || status != work.TaskCreated {
			t.Fatalf("create Queue Task %q = %#v, %q, %v", key, task, status, err)
		}
		now = now.Add(time.Minute)
		return task
	}
	high := create("high", "Urgent referral", work.TaskUrgencyHighPriority, work.TaskCategoryDocumentation)
	normalOld := create("normal-old", "Routine records", work.TaskUrgencyNormal, work.TaskCategoryDocumentation)
	normalNew := create("normal-new", "Routine surgery records", work.TaskUrgencyNormal, work.TaskCategoryDocumentation)
	nonUrgent := create("non-urgent", "Optional follow-up", work.TaskUrgencyNonUrgent, work.TaskCategoryDocumentation)

	reads := workspace.New(db.As(testdb.Portal), accessModule)
	first, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID, Limit: 2,
	})
	if err != nil {
		t.Fatalf("query first priority page: %v", err)
	}
	assertWorkspaceTaskIDs(t, first.Items, high.ID, normalOld.ID)
	if first.NextCursor == "" {
		t.Fatalf("first priority page metadata = %#v", first)
	}
	second, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		Cursor: first.NextCursor, Limit: 2,
	})
	if err != nil {
		t.Fatalf("query second priority page: %v", err)
	}
	assertWorkspaceTaskIDs(t, second.Items, normalNew.ID, nonUrgent.ID)
	if second.NextCursor != "" {
		t.Fatalf("second priority page metadata = %#v", second)
	}
	for _, test := range []struct {
		ordering work.TaskOrdering
		want     []string
	}{
		{work.TaskOrderingTime, []string{high.ID, normalOld.ID, normalNew.ID, nonUrgent.ID}},
		{work.TaskOrderingRecent, []string{nonUrgent.ID, normalNew.ID, normalOld.ID, high.ID}},
	} {
		command := workspace.QueryTasksCommand{
			Identity: identity, PracticeID: authorization.Practice.ID,
			Ordering: test.ordering, Limit: 1,
		}
		var paged []work.Task
		for {
			page, err := reads.QueryTasks(context.Background(), command)
			if err != nil {
				t.Fatalf("query %s page: %v", test.ordering, err)
			}
			paged = append(paged, page.Items...)
			if page.NextCursor == "" || len(paged) > len(test.want) {
				break
			}
			command.Cursor = page.NextCursor
		}
		assertWorkspaceTaskIDs(t, paged, test.want...)
	}
	search, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID, Search: "SURGERY",
	})
	if err != nil {
		t.Fatalf("search Queue title: %v", err)
	}
	assertWorkspaceTaskIDs(t, search.Items, normalNew.ID)
	if _, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		Ordering: work.TaskOrderingTime, Cursor: first.NextCursor,
	}); !errors.Is(err, workspace.ErrInvalidInput) {
		t.Fatalf("cross-order cursor error = %v, want invalid input", err)
	}

	ctx := context.Background()
	_, err = workModule.CompleteTask(ctx, work.CompleteTaskCommand{Reason: work.CloseReasonDone, Identity: identity, TaskID: high.ID, ExpectedVersion: high.Version})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := workModule.CompleteTask(ctx, work.CompleteTaskCommand{Reason: work.CloseReasonDone, Identity: identity, TaskID: normalOld.ID, ExpectedVersion: normalOld.Version}); err != nil {
		t.Fatal(err)
	}
	completedCommand := workspace.QueryTasksCommand{Identity: identity, PracticeID: authorization.Practice.ID, State: work.TaskCompleted, Ordering: work.TaskOrderingRecent, Limit: 1}
	completed, err := reads.QueryTasks(ctx, completedCommand)
	if err != nil {
		t.Fatal(err)
	}
	assertWorkspaceTaskIDs(t, completed.Items, normalOld.ID)
	completedCommand.Cursor = completed.NextCursor
	completed, err = reads.QueryTasks(ctx, completedCommand)
	if err != nil {
		t.Fatal(err)
	}
	assertWorkspaceTaskIDs(t, completed.Items, high.ID)

}

func insertRecoveryCall(
	t *testing.T,
	pool interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	},
	authorization access.Authorization,
	locationID string,
	phone string,
	now time.Time,
) string {
	t.Helper()
	handoffID := uuid.NewString()
	callID := uuid.NewString()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, phone, phone_source,
			display_name, name_source, transfer_reason, reason_source,
			expires_at, consumed_at, created_at
		) VALUES ($1, 'workspace-recovery-service', $2, $3, $4, $5, $6,
			$7, 'Abita', 'Recovery caller', 'Abita', 'Missed call', 'Abita AI',
			$8, $9, $9)
	`, handoffID, authorization.Practice.ID, locationID, "source-"+callID,
		"idempotency-"+callID, []byte(callID), phone, now.Add(time.Minute), now,
	); err != nil {
		t.Fatalf("insert recovery handoff: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_calls (
			id, source_handoff_id, practice_id, location_id, disposition_at,
			disposition_actor_subject, disposition_outcome, terminal_outcome,
			caller_phone, ended_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'FOLLOW_UP_REQUIRED',
			'FOLLOW_UP_REQUIRED', $7, $5, $5, $5)
	`, callID, handoffID, authorization.Practice.ID, locationID, now,
		authorization.Actor.Subject, phone,
	); err != nil {
		t.Fatalf("insert recovery Call: %v", err)
	}
	return callID
}

func assertWorkspaceTaskIDs(t *testing.T, tasks []work.Task, ids ...string) {
	t.Helper()
	if len(tasks) != len(ids) {
		t.Fatalf("Task IDs = %#v, want %v", tasks, ids)
	}
	for index, id := range ids {
		if tasks[index].ID != id {
			t.Fatalf("Task IDs = %#v, want %v", tasks, ids)
		}
	}
}
