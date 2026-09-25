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

	create("optical", "Optical request", work.TaskUrgencyNormal, work.TaskCategoryOptical)
	create("other", "Other request", work.TaskUrgencyNormal, work.TaskCategoryOther)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO work_responsibility_locations (practice_id, location_id) VALUES ($1, $2)`, authorization.Practice.ID, authorization.Locations[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO work_responsibilities (practice_id, location_id, account_email, category, role)
		VALUES ($1,$2,$3,'documentation','primary'), ($1,$2,$3,'optical','backup')`, authorization.Practice.ID, authorization.Locations[0].ID, identity.Email); err != nil {
		t.Fatal(err)
	}
	for _, responsibility := range []string{"all", "mine"} {
		t.Run(responsibility+" category menu counts", func(t *testing.T) {
			command := workspace.QueryTasksCommand{Identity: identity, PracticeID: authorization.Practice.ID, Responsibility: responsibility, Folder: work.TaskFolderWork, Grouped: true}
			baseline, err := reads.QueryTasks(ctx, command)
			if err != nil {
				t.Fatal(err)
			}
			wantTotal, wantOther := 6, 1
			if responsibility == "mine" {
				wantTotal, wantOther = 5, 0
			}
			if baseline.Counts == nil || baseline.Counts.Tasks != wantTotal || baseline.Counts.Categories.Other != wantOther {
				t.Fatalf("responsibility-scoped counts = %+v", baseline.Counts)
			}
			for _, category := range []work.TaskCategory{work.TaskCategoryDocumentation, work.TaskCategoryOptical, work.TaskCategoryOther} {
				command.Category = category
				filtered, err := reads.QueryTasks(ctx, command)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range filtered.Items {
					if item.Category != category {
						t.Fatalf("category %s returned %s", category, item.Category)
					}
				}
				if filtered.Counts == nil || *filtered.Counts != *baseline.Counts {
					t.Errorf("category %s changed menu totals: got %+v, want %+v", category, filtered.Counts, baseline.Counts)
				}
			}
		})
	}

	_, err = workModule.CompleteTask(ctx, work.CompleteTaskCommand{Identity: identity, TaskID: high.ID, ExpectedVersion: high.Version})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := workModule.CompleteTask(ctx, work.CompleteTaskCommand{Identity: identity, TaskID: normalOld.ID, ExpectedVersion: normalOld.Version}); err != nil {
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
