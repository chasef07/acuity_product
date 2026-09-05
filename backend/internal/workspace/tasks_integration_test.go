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

func TestQueryTasksPreservesPriorityCursorSearchAndAuthoritativeCounts(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 15, 14, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
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
	workModule := work.New(pool, accessModule, func() time.Time { return now })
	service := access.ServiceIdentity{
		Subject:       "workspace-queue-service",
		PracticeID:    authorization.Practice.ID,
		LocationScope: access.LocationScopeAll,
		Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
	}
	create := func(key, title string, urgency work.TaskUrgency) work.Task {
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
				Category:       work.TaskCategoryDocumentation,
				Urgency:        urgency,
			},
		)
		if err != nil || status != work.TaskCreated {
			t.Fatalf("create Queue Task %q = %#v, %q, %v", key, task, status, err)
		}
		now = now.Add(time.Minute)
		return task
	}
	high := create("high", "Urgent referral", work.TaskUrgencyHighPriority)
	normalOld := create("normal-old", "Routine records", work.TaskUrgencyNormal)
	normalNew := create("normal-new", "Routine surgery records", work.TaskUrgencyNormal)
	nonUrgent := create("non-urgent", "Optional follow-up", work.TaskUrgencyNonUrgent)

	reads := workspace.New(pool, accessModule)
	first, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID, Limit: 2,
	})
	if err != nil {
		t.Fatalf("query first priority page: %v", err)
	}
	assertWorkspaceTaskIDs(t, first.Items, high.ID, normalOld.ID)
	if first.NextCursor == "" || first.Counts.Tasks != 4 ||
		first.Counts.Categories.Documentation != 4 {
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
	if second.NextCursor != "" || second.Counts == nil || first.Counts == nil || *second.Counts != *first.Counts {
		t.Fatalf("second priority page metadata = %#v, want counts %#v", second, first.Counts)
	}
	timeOrdered, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		Ordering: work.TaskOrderingTime,
	})
	if err != nil {
		t.Fatalf("query time-ordered Queue: %v", err)
	}
	assertWorkspaceTaskIDs(t, timeOrdered.Items, high.ID, normalOld.ID, normalNew.ID, nonUrgent.ID)
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
}

func TestQueryTasksMissedCallsFolderStartsWithNewestRecoveryTask(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 16, 10, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test", RequestedBy: "workspace-recovery-folder-test",
		Practices: []access.PracticeProvision{{
			Key: "workspace-recovery-practice", Name: "Workspace Recovery Practice",
			Locations: []access.LocationProvision{{
				Key: "workspace-recovery-location", Name: "Workspace Recovery Location",
				AbitaOfficeKeys: []string{"workspace-recovery-office"},
			}},
			AccessGrants: []access.AccessGrantProvision{{
				Key: "workspace-recovery-staff", Email: "staff@workspace-recovery.test",
				Role: access.RoleStaff, LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision recovery fixture: %v", err)
	}
	identity := access.Identity{
		Subject: "workspace-recovery-staff-subject", Email: "staff@workspace-recovery.test",
		EmailVerified: true,
	}
	authorization := testaccess.Activate(t, accessModule, identity)
	locationID := authorization.Locations[0].ID
	workModule := work.New(pool, accessModule, func() time.Time { return now })

	ensureRecovery := func(phone string, occurredAt time.Time) work.Task {
		t.Helper()
		callID := insertRecoveryCall(
			t, pool, authorization, locationID, phone, occurredAt,
		)
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin recovery Task transaction: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		task, err := workModule.EnsureRecoveryTask(
			context.Background(), tx, work.EnsureRecoveryTaskCommand{
				CallID: callID, PracticeID: authorization.Practice.ID,
				LocationID: locationID, Phone: phone,
				Outcome: work.RecoveryOutcomeMissedCall, OccurredAt: occurredAt,
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

	older := ensureRecovery("+15555550101", now)
	now = now.Add(time.Minute)
	newest := ensureRecovery("+15555550102", now)
	now = now.Add(time.Minute)
	service := access.ServiceIdentity{
		Subject: "workspace-recovery-service", PracticeID: authorization.Practice.ID,
		LocationScope: access.LocationScopeAll,
		Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
	}
	if _, status, err := workModule.CreateAITask(
		context.Background(), work.CreateAITaskCommand{
			Service: service, OfficeKey: "workspace-recovery-office",
			OfficePhone: "+17275550100", SourceCallID: "newer-general-call",
			IdempotencyKey: "newer-general-task", Phone: "+17275550199",
			Summary: "Review a newer request", Message: "General queue fixture",
			Category: work.TaskCategoryOther, Urgency: work.TaskUrgencyNormal,
		},
	); err != nil || status != work.TaskCreated {
		t.Fatalf("create general Task = %q, %v", status, err)
	}

	reads := workspace.New(pool, accessModule)
	workPage, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		Folder: work.TaskFolderWork, Ordering: work.TaskOrderingRecent, Limit: 50,
	})
	if err != nil || len(workPage.Items) != 1 {
		t.Fatalf("query work folder = %#v, %v; want one general Task", workPage, err)
	}
	first, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		Folder: work.TaskFolderMissedCalls, Ordering: work.TaskOrderingRecent, Limit: 1,
	})
	if err != nil {
		t.Fatalf("query first Missed Calls page: %v", err)
	}
	assertWorkspaceTaskIDs(t, first.Items, newest.ID)
	if first.NextCursor == "" {
		t.Fatal("first Missed Calls page has no cursor")
	}
	second, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: authorization.Practice.ID,
		Folder: work.TaskFolderMissedCalls, Ordering: work.TaskOrderingRecent,
		Cursor: first.NextCursor, Limit: 1,
	})
	if err != nil {
		t.Fatalf("query second Missed Calls page: %v", err)
	}
	assertWorkspaceTaskIDs(t, second.Items, older.ID)
	if second.NextCursor != "" {
		t.Fatalf("second Missed Calls cursor = %q, want empty", second.NextCursor)
	}
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
