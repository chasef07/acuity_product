package workspace_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestQueueReadsProjectConversationAndUnreadInOneAuthorizedFlow(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "workspace-read-test",
		Practices: []access.PracticeProvision{{
			Key:  "workspace-read-practice",
			Name: "Workspace Read Practice",
			Locations: []access.LocationProvision{{
				Key:             "workspace-read-location",
				Name:            "Workspace Read Location",
				AbitaOfficeKeys: []string{"workspace-read-office"},
			}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "workspace-read-staff",
				Email:         "staff@workspace-read.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision Workspace read fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "workspace-read-staff-subject",
		Email:         "staff@workspace-read.test",
		EmailVerified: true,
	}
	authorization := testaccess.Activate(t, accessModule, identity)
	workModule := work.New(pool, accessModule, func() time.Time { return now })
	task, status, err := workModule.CreateAITask(
		context.Background(),
		work.CreateAITaskCommand{
			Service: access.ServiceIdentity{
				Subject:       "workspace-read-service",
				PracticeID:    authorization.Practice.ID,
				LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{
					access.ServiceCapabilityCreateTask,
				},
			},
			OfficeKey:      "workspace-read-office",
			OfficePhone:    "+17275550100",
			SourceCallID:   "workspace-read-source-call",
			IdempotencyKey: "workspace-read-task",
			Phone:          "+17275550199",
			CallerName:     "Workspace caller",
			Summary:        "Return the caller's message.",
			Message:        "The caller requested a text response.",
			Category:       work.TaskCategoryDocumentation,
			Urgency:        work.TaskUrgencyNormal,
		},
	)
	if err != nil || status != work.TaskCreated {
		t.Fatalf("create Workspace Task = %#v, %q, %v", task, status, err)
	}
	messages := messaging.New(
		pool, accessModule, workModule, nil, messaging.Config{}, func() time.Time { return now },
	)
	if err := messages.Provision(context.Background(), []messaging.LocationProvision{{
		PracticeKey:        "workspace-read-practice",
		LocationKey:        "workspace-read-location",
		Sender:             "+17275550100",
		MessagingProfileID: "workspace-read-profile",
	}}); err != nil {
		t.Fatalf("provision Workspace Messaging fixture: %v", err)
	}
	now = now.Add(time.Minute)
	message, _, err := messages.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     task.PracticeID,
		LocationID:     task.LocationID,
		Destination:    task.Phone,
		TaskID:         task.ID,
		Body:           "We received your request.",
		IdempotencyKey: "workspace-read-message",
	})
	if err != nil {
		t.Fatalf("create Workspace Message: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO messaging_thread_unreads (
			thread_id, user_subject, unread_since, latest_message_id
		)
		VALUES ($1, $2, $3, $4)
	`, message.Thread.ID, identity.Subject, now, message.ID); err != nil {
		t.Fatalf("mark Workspace Thread unread: %v", err)
	}

	reads := workspace.New(pool, accessModule)
	page, err := reads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: task.PracticeID,
	})
	if err != nil {
		t.Fatalf("query Workspace Queue: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != task.ID ||
		page.Items[0].ConversationThreadID != message.Thread.ID ||
		!page.Items[0].Unread || page.Items[0].RelatedInteractionCount != 0 {
		t.Fatalf("Workspace Queue projection = %#v", page)
	}
	tracer := &workspaceQueryTracer{}
	tracedConfig, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse traced Workspace database config: %v", err)
	}
	tracedConfig.ConnConfig.Tracer = tracer
	tracedPool, err := pgxpool.NewWithConfig(context.Background(), tracedConfig)
	if err != nil {
		t.Fatalf("open traced Workspace database pool: %v", err)
	}
	t.Cleanup(tracedPool.Close)
	tracedReads := workspace.New(tracedPool, access.New(tracedPool, func() time.Time { return now }))
	if _, err := tracedReads.QueryTasks(context.Background(), workspace.QueryTasksCommand{
		Identity: identity, PracticeID: task.PracticeID,
	}); err != nil {
		t.Fatalf("query traced Workspace Queue: %v", err)
	}
	if tracer.taskResponseQueries.Load() != 1 ||
		tracer.conversationProjectionQueries.Load() != 1 ||
		tracer.interactionProjectionQueries.Load() != 1 {
		t.Fatalf(
			"Workspace Queue response queries = task %d, conversation %d, interactions %d; want 1, 1, 1",
			tracer.taskResponseQueries.Load(),
			tracer.conversationProjectionQueries.Load(),
			tracer.interactionProjectionQueries.Load(),
		)
	}
	read, err := reads.ReadTask(context.Background(), identity, task.ID)
	if err != nil {
		t.Fatalf("read Workspace Task: %v", err)
	}
	if read.ConversationThreadID != message.Thread.ID || !read.Unread {
		t.Fatalf("Workspace Task projection = %#v", read)
	}
	timeline, err := reads.QueryTimeline(context.Background(), workspace.QueryTimelineCommand{
		Identity: identity, ThreadID: message.Thread.ID,
	})
	if err != nil {
		t.Fatalf("query Workspace conversation: %v", err)
	}
	if len(timeline.Items) != 2 || timeline.Items[0].Type != "TASK" ||
		timeline.Items[1].Type != "MESSAGE" {
		t.Fatalf("Workspace conversation projection = %#v", timeline)
	}

	completed, err := workModule.CompleteTask(context.Background(), work.CompleteTaskCommand{
		Identity: identity, TaskID: task.ID, ExpectedVersion: task.Version,
	})
	if err != nil {
		t.Fatalf("complete Workspace Task: %v", err)
	}
	completedRead, err := reads.ReadTask(context.Background(), identity, completed.ID)
	if err != nil || completedRead.ConversationThreadID != message.Thread.ID ||
		completedRead.Unread {
		t.Fatalf("completed Workspace Task projection = %#v, %v", completedRead, err)
	}
}

type workspaceQueryTracer struct {
	taskResponseQueries           atomic.Int64
	conversationProjectionQueries atomic.Int64
	interactionProjectionQueries  atomic.Int64
}

func (tracer *workspaceQueryTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	if strings.Contains(data.SQL, "FROM work_tasks task") &&
		strings.Contains(data.SQL, "LEFT JOIN LATERAL") &&
		strings.Contains(data.SQL, "messaging_thread_unreads") {
		tracer.taskResponseQueries.Add(1)
		tracer.conversationProjectionQueries.Add(1)
	}
	if strings.Contains(data.SQL, "FROM work_task_interactions interaction") {
		tracer.interactionProjectionQueries.Add(1)
	}
	return ctx
}

func (*workspaceQueryTracer) TraceQueryEnd(
	context.Context,
	*pgx.Conn,
	pgx.TraceQueryEndData,
) {
}
