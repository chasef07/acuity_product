package work_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestTaskCompletionsPublishOneWorkspaceHintPerTransaction(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	authorization, identity := provisionStaff(t, db.Owner, accessModule, now)
	module := work.New(db.As(testdb.Portal), accessModule, func() time.Time { return now })
	create := func(key string) work.Task {
		t.Helper()
		task, _, err := module.CreateAITask(ctx, work.CreateAITaskCommand{
			Service: access.ServiceIdentity{
				Subject: "completion-agent", PracticeID: authorization.Practice.ID,
				LocationScope: access.LocationScopeAll,
				Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
			},
			OfficeKey: "spring-hill", OfficePhone: "+12025550100", SourceCallID: key, IdempotencyKey: key,
			Phone: "+12025550123", CallerName: "Synthetic Caller", Summary: "Optical request " + key,
			Message: "Synthetic glasses prescription follow-up", Category: work.TaskCategoryOptical,
			Urgency: work.TaskUrgencyNormal,
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	statements := &statementCounter{}
	traced := newTracedWorkModule(t, db.As(testdb.Portal), statements, now)
	listener := listenForWorkspaceHints(t, db)

	single := create("single")
	drainWorkspaceHints(t, listener)
	statements.count.Store(0)
	if _, err := traced.CompleteTask(ctx, work.CompleteTaskCommand{
		Identity: identity, TaskID: single.ID, ExpectedVersion: single.Version,
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("CompleteTask: %d statements", statements.count.Load())
	assertOneWorkspaceHint(t, db, listener, authorization.Practice.ID)

	members := []work.ReviewedTask{}
	for index := range 5 {
		task := create(fmt.Sprintf("group-%d", index))
		members = append(members, work.ReviewedTask{ID: task.ID, ExpectedVersion: task.Version})
	}
	drainWorkspaceHints(t, listener)
	statements.count.Store(0)
	anchor, err := traced.CompleteTaskGroup(ctx, work.CompleteTaskGroupCommand{
		Identity: identity, TaskID: members[2].ID, Members: members,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CompleteTaskGroup of 5: %d statements", statements.count.Load())
	assertOneWorkspaceHint(t, db, listener, authorization.Practice.ID)
	if anchor.ID != members[2].ID || anchor.State != work.TaskCompleted ||
		anchor.Version != members[2].ExpectedVersion+1 || anchor.CompletedBy == nil ||
		anchor.CompletedBy.Subject != identity.Subject || anchor.Title != "Optical request group-2" {
		t.Fatalf("completed group anchor = %#v", anchor)
	}
	for _, member := range members {
		var activities int
		if err := db.Owner.QueryRow(ctx, `
			SELECT count(*) FROM work_task_activities
			WHERE task_id = $1 AND kind = 'TASK_COMPLETED' AND task_version = $2
		`, member.ID, member.ExpectedVersion+1).Scan(&activities); err != nil {
			t.Fatal(err)
		}
		if activities != 1 {
			t.Fatalf("Task %s completion Activities = %d, want 1", member.ID, activities)
		}
	}

	stale := create("stale")
	_, err = traced.CompleteTaskGroup(ctx, work.CompleteTaskGroupCommand{
		Identity: identity, TaskID: stale.ID,
		Members: []work.ReviewedTask{{ID: stale.ID, ExpectedVersion: stale.Version + 1}},
	})
	if !errors.Is(err, work.ErrConflict) {
		t.Fatalf("stale group completion error = %v, want ErrConflict", err)
	}
	_, err = traced.CompleteTaskGroup(ctx, work.CompleteTaskGroupCommand{
		Identity: identity, TaskID: stale.ID,
		Members: []work.ReviewedTask{
			{ID: stale.ID, ExpectedVersion: stale.Version},
			{ID: "00000000-0000-4000-8000-000000000000", ExpectedVersion: 1},
		},
	})
	if !errors.Is(err, work.ErrDenied) {
		t.Fatalf("missing group member error = %v, want ErrDenied", err)
	}
}

func listenForWorkspaceHints(t *testing.T, db *testdb.DB) *pgx.Conn {
	t.Helper()
	connection, err := pgx.ConnectConfig(context.Background(), db.As(testdb.Portal).Config().ConnConfig.Copy())
	if err != nil {
		t.Fatalf("connect workspace hint listener: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close(context.Background()) })
	if _, err := connection.Exec(context.Background(), "LISTEN acuity_workspace_hints"); err != nil {
		t.Fatalf("listen for workspace hints: %v", err)
	}
	return connection
}

func drainWorkspaceHints(t *testing.T, connection *pgx.Conn) []*pgconn.Notification {
	t.Helper()
	var notifications []*pgconn.Notification
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		notification, err := connection.WaitForNotification(ctx)
		cancel()
		if err != nil && ctx.Err() != nil {
			return notifications
		}
		if err != nil {
			t.Fatalf("wait for workspace hint: %v", err)
		}
		notifications = append(notifications, notification)
	}
}

func assertOneWorkspaceHint(t *testing.T, db *testdb.DB, connection *pgx.Conn, practiceID string) {
	t.Helper()
	notifications := drainWorkspaceHints(t, connection)
	if len(notifications) != 1 {
		t.Fatalf("workspace hints = %d, want 1", len(notifications))
	}
	var version int64
	if err := db.Owner.QueryRow(context.Background(), `
		SELECT workspace_version FROM access_practices WHERE id = $1
	`, practiceID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(notifications[0].Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2 || payload["practiceId"] != practiceID || payload["version"] != float64(version) {
		t.Fatalf("workspace hint payload = %s, want Practice %s at version %d",
			notifications[0].Payload, practiceID, version)
	}
}

type statementCounter struct {
	count atomic.Int64
}

func (counter *statementCounter) TraceQueryStart(
	ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData,
) context.Context {
	counter.count.Add(1)
	return ctx
}

func (*statementCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
