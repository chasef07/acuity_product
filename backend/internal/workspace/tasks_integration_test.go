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
	if second.NextCursor != "" || second.Counts != first.Counts {
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
