package work_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
)

func TestSharedReviewTasksPreserveEvidenceAndRejectStaleCompletion(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	a := access.New(pool, func() time.Time { return now })
	auth, identity := provisionStaff(t, a, now)
	m := work.New(pool, a, func() time.Time { return now })
	practice, location := auth.Practice.ID, auth.Locations[0].ID
	var thread string
	if err := pool.QueryRow(ctx, `INSERT INTO messaging_threads(practice_id,location_id,office_phone,external_phone,created_at,updated_at) VALUES($1,$2,'+15555550100','+15555550123',$3,$3) RETURNING id::text`, practice, location, now).Scan(&thread); err != nil {
		t.Fatal(err)
	}
	inbound := func(rollback bool) string {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		id := uuid.NewString()
		if _, err := tx.Exec(ctx, `INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,provider_message_id,created_at,updated_at) VALUES($1::uuid,$2,$3,$4,'INBOUND','Synthetic request','+15555550123','+15555550100','DELIVERED',$1::text,$5,$5)`, id, thread, practice, location, now); err != nil {
			t.Fatal(err)
		}
		if err := m.EnsureInboundMessageReview(ctx, tx, practice, location, "+15555550123", thread, id, now); err != nil {
			t.Fatal(err)
		}
		if !rollback {
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	inbound(true)
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM work_tasks WHERE origin='INBOUND_MESSAGE_REVIEW'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback leaked work: %d %v", count, err)
	}
	firstMessage := inbound(false)
	var taskID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM work_tasks WHERE origin='INBOUND_MESSAGE_REVIEW'`).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	original, err := m.ReadTask(ctx, identity, taskID)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	inbound(false)
	changed, err := m.ReadTask(ctx, identity, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Version != original.Version+1 || !changed.CreatedAt.Equal(original.CreatedAt) || changed.MessageID != firstMessage {
		t.Fatalf("new evidence rewrote source: %#v", changed)
	}
	if _, err := m.CompleteTask(ctx, work.CompleteTaskCommand{Identity: identity, TaskID: taskID, ExpectedVersion: original.Version}); !errors.Is(err, work.ErrConflict) {
		t.Fatalf("stale completion: %v", err)
	}
	if _, err := m.CompleteTask(ctx, work.CompleteTaskCommand{Identity: identity, TaskID: taskID, ExpectedVersion: changed.Version}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	inbound(false)
	if _, err := m.ReopenTask(ctx, work.ReopenTaskCommand{Identity: identity, TaskID: taskID, ExpectedVersion: changed.Version + 1}); !errors.Is(err, work.ErrConflict) {
		t.Fatalf("reopened older review over current thread work: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM work_tasks WHERE origin='INBOUND_MESSAGE_REVIEW'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("next inbound failed to create fresh review: %d %v", count, err)
	}
	var updates int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM work_task_activities WHERE task_id=$1 AND kind='SOURCE_UPDATED'`, taskID).Scan(&updates); err != nil || updates != 1 {
		t.Fatalf("source Activity: %d %v", updates, err)
	}
}
