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
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskChangesReturnCommittedTasksWithinLocationScope(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	if _, err := accessModule.Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "task-change-feed-test",
		Practices: []access.PracticeProvision{{
			Key: "task-feed", Name: "Task Feed Practice",
			Locations: []access.LocationProvision{
				{Key: "north", Name: "North Office", AbitaOfficeKeys: []string{"task-feed-north"}},
				{Key: "south", Name: "South Office", AbitaOfficeKeys: []string{"task-feed-south"}},
			},
			AccessGrants: []access.AccessGrantProvision{
				{Key: "all", Email: "all@task-feed.test", Role: access.RoleStaff, LocationScope: access.LocationScopeAll},
				{Key: "north", Email: "north@task-feed.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"north"}},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	everywhere := access.Identity{Subject: "task-feed-all", Email: "all@task-feed.test", EmailVerified: true}
	north := access.Identity{Subject: "task-feed-north", Email: "north@task-feed.test", EmailVerified: true}
	authorization := testaccess.Activate(t, accessModule, everywhere)
	testaccess.Activate(t, accessModule, north)
	practiceID := authorization.Practice.ID
	writes := work.New(pool, accessModule, func() time.Time { return now })
	reads := workspace.New(pool, accessModule)
	create := func(key, office, phone string) work.Task {
		t.Helper()
		task, status, err := writes.CreateAITask(ctx, work.CreateAITaskCommand{
			Service: access.ServiceIdentity{
				Subject: "task-feed-service", PracticeID: practiceID, LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask},
			},
			OfficeKey: office, OfficePhone: "+17275550100", SourceCallID: "task-feed-" + key,
			IdempotencyKey: "task-feed-" + key, Phone: phone, Summary: "Synthetic " + key,
			Message: "Synthetic change feed fixture", Category: work.TaskCategoryDocumentation, Urgency: work.TaskUrgencyNormal,
		})
		if err != nil || status != work.TaskCreated {
			t.Fatalf("create Task %q = %q, %v", key, status, err)
		}
		now = now.Add(time.Minute)
		return task
	}
	query := func(identity access.Identity, since int64) workspace.TaskChanges {
		t.Helper()
		changes, err := reads.QueryTaskChanges(ctx, workspace.QueryTaskChangesCommand{
			Identity: identity, PracticeID: practiceID, SinceVersion: since,
			Kind: "follow_up", Responsibility: "all",
		})
		if err != nil {
			t.Fatalf("query Task changes since %d: %v", since, err)
		}
		return changes
	}

	start := workspaceVersion(t, pool, practiceID)
	northTask := create("north-1", "task-feed-north", "+17275550111")
	southTask := create("south-1", "task-feed-south", "+17275550122")

	scoped := query(north, start)
	if !scoped.Complete || scoped.Version != start+2 || scoped.Counts == nil || scoped.Counts.Tasks != 1 {
		t.Fatalf("north Task changes = %#v", scoped)
	}
	assertWorkspaceTaskIDs(t, scoped.Tasks, northTask.ID)
	assertWorkspaceTaskIDs(t, scoped.OpenTasks, northTask.ID)
	assertWorkspaceTaskIDs(t, scoped.CompletedTasks)
	if all := query(everywhere, start); !all.Complete || len(all.Tasks) != 2 || len(all.OpenTasks) != 2 || all.Counts.Tasks != 2 {
		t.Fatalf("all-Location Task changes = %#v", all)
	}
	if _, err := reads.QueryTaskChanges(ctx, workspace.QueryTaskChangesCommand{
		Identity: north, PracticeID: practiceID, LocationID: southTask.LocationID, SinceVersion: start,
	}); !errors.Is(err, workspace.ErrDenied) {
		t.Fatalf("north staff reading south changes error = %v, want denied", err)
	}
	if _, err := reads.QueryTaskChanges(ctx, workspace.QueryTaskChangesCommand{
		Identity: north, PracticeID: practiceID,
	}); !errors.Is(err, workspace.ErrInvalidInput) {
		t.Fatalf("missing sinceVersion error = %v, want invalid input", err)
	}

	grouped := create("north-2", "task-feed-north", "+17275550111")
	beforeCompletion := workspaceVersion(t, pool, practiceID)
	family := query(north, beforeCompletion-1)
	assertWorkspaceTaskIDs(t, family.Tasks, grouped.ID)
	if len(family.OpenTasks) != 1 || family.OpenTasks[0].ID != grouped.ID || len(family.OpenTasks[0].GroupMembers) != 2 {
		t.Fatalf("grouped family changes = %#v", family.OpenTasks)
	}

	completed, err := writes.CompleteTask(ctx, work.CompleteTaskCommand{Identity: north, TaskID: grouped.ID, ExpectedVersion: grouped.Version})
	if err != nil {
		t.Fatal(err)
	}
	afterCompletion := query(north, beforeCompletion)
	if !afterCompletion.Complete || afterCompletion.Version != beforeCompletion+1 {
		t.Fatalf("completion changes = %#v", afterCompletion)
	}
	assertWorkspaceTaskIDs(t, afterCompletion.Tasks, completed.ID)
	if afterCompletion.Tasks[0].State != work.TaskCompleted {
		t.Fatalf("completed Task state = %q", afterCompletion.Tasks[0].State)
	}
	assertWorkspaceTaskIDs(t, afterCompletion.CompletedTasks, completed.ID)
	if len(afterCompletion.OpenTasks) != 1 || afterCompletion.OpenTasks[0].ID != northTask.ID || len(afterCompletion.OpenTasks[0].GroupMembers) != 1 {
		t.Fatalf("open family after completion = %#v", afterCompletion.OpenTasks)
	}
	if current := query(north, afterCompletion.Version); !current.Complete || len(current.Tasks) != 0 || current.Counts == nil {
		t.Fatalf("Task changes at the current version = %#v", current)
	}
	if ahead := query(north, afterCompletion.Version+1); ahead.Complete {
		t.Fatalf("Task changes ahead of the workspace = %#v", ahead)
	}
}

func TestTaskChangesRecordInTheWritingTransactionAndFallBackWhenUnknown(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	if _, err := accessModule.Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "task-change-retention-test",
		Practices: []access.PracticeProvision{{
			Key: "task-feed-retention", Name: "Task Feed Retention Practice",
			Locations: []access.LocationProvision{{Key: "office", Name: "Office"}},
			AccessGrants: []access.AccessGrantProvision{
				{Key: "staff", Email: "staff@task-feed-retention.test", Role: access.RoleStaff, LocationScope: access.LocationScopeAll},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	identity := access.Identity{Subject: "task-feed-retention", Email: "staff@task-feed-retention.test", EmailVerified: true}
	authorization := testaccess.Activate(t, accessModule, identity)
	practiceID := authorization.Practice.ID
	reads := workspace.New(pool, accessModule)
	record := func(commit bool, taskIDs ...string) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := accessModule.RecordWorkspaceChange(ctx, tx, practiceID, taskIDs...); err != nil {
			t.Fatal(err)
		}
		if commit {
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	complete := func(since int64) bool {
		t.Helper()
		changes, err := reads.QueryTaskChanges(ctx, workspace.QueryTaskChangesCommand{Identity: identity, PracticeID: practiceID, SinceVersion: since})
		if err != nil {
			t.Fatal(err)
		}
		return changes.Complete
	}

	start := workspaceVersion(t, pool, practiceID)
	record(false, uuid.NewString())
	if version := workspaceVersion(t, pool, practiceID); version != start || changeRows(t, pool, practiceID, start) != 0 {
		t.Fatalf("rolled-back change left version %d and %d rows", version, changeRows(t, pool, practiceID, start))
	}
	missing := uuid.NewString()
	record(true, missing)
	if rows := changeRows(t, pool, practiceID, start); rows != 1 || !complete(start) {
		t.Fatalf("committed Task change rows = %d", rows)
	}
	declared := start + 1
	record(true)
	if complete(declared) || complete(start) {
		t.Fatal("an undeclared workspace change must force a full reload")
	}
	capped := workspaceVersion(t, pool, practiceID)
	many := make([]string, workspace.TaskChangeLimit+1)
	for i := range many {
		many[i] = uuid.NewString()
	}
	record(true, many...)
	if complete(capped) {
		t.Fatal("more changed Tasks than the cap must force a full reload")
	}
	retained := workspaceVersion(t, pool, practiceID)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for range access.WorkspaceChangeRetention {
		if _, err := accessModule.RecordWorkspaceChange(ctx, tx, practiceID, missing); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	current := workspaceVersion(t, pool, practiceID)
	if rows := changeRows(t, pool, practiceID, 0); rows != access.WorkspaceChangeRetention {
		t.Fatalf("retained %d change rows, want %d", rows, access.WorkspaceChangeRetention)
	}
	if complete(retained - 1) {
		t.Fatal("a version older than retention must force a full reload")
	}
	if current != retained+access.WorkspaceChangeRetention || !complete(retained) {
		t.Fatal("the oldest retained version must still be served")
	}
}

func workspaceVersion(t *testing.T, pool *pgxpool.Pool, practiceID string) int64 {
	t.Helper()
	var version int64
	if err := pool.QueryRow(context.Background(), `SELECT workspace_version FROM access_practices WHERE id=$1`, practiceID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func changeRows(t *testing.T, pool *pgxpool.Pool, practiceID string, after int64) int {
	t.Helper()
	var rows int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM access_workspace_changes WHERE practice_id=$1 AND version>$2`, practiceID, after).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}
