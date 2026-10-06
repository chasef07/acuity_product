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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

func TestAppointmentReviewIsOneSpringHillTaskPerInteraction(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	a := access.New(pool, func() time.Time { return now })
	auth, identity := provisionStaff(t, a, now)
	m := work.New(pool, a, func() time.Time { return now })
	practice, springHill, otherOffice := auth.Practice.ID, auth.Locations[0].ID, auth.Locations[1].ID
	ensure, reviews := appointmentReviewHarness(t, pool, m, practice)

	ensure(otherOffice, "synthetic-other-office", "Review booked appointment", "Verify insurance and provider for this appointment.", now)
	if got := reviews(); len(got) != 0 {
		t.Fatalf("appointment review created outside the Spring Hill route: %#v", got)
	}

	checkpoint := now.Add(-2 * time.Minute)
	ensure(springHill, "synthetic-spring-hill", "Check appointment booking", "The agent could not confirm the booking.", checkpoint)
	ensure(springHill, "synthetic-spring-hill", "Check appointment booking", "The agent could not confirm the booking.", checkpoint)
	first := reviews()
	if len(first) != 1 || first[0].state != "OPEN" {
		t.Fatalf("checkpoint review = %#v", first)
	}

	closeout := now.Add(-time.Minute)
	ensure(springHill, "synthetic-spring-hill", "Review booked appointment", "Verify insurance and provider for this appointment.", closeout)
	updated := reviews()
	if len(updated) != 1 || updated[0].id != first[0].id || updated[0].title != "Review booked appointment" ||
		updated[0].message != "Verify insurance and provider for this appointment." || updated[0].version != first[0].version+1 {
		t.Fatalf("closeout did not update the one open review: %#v", updated)
	}
	var previousMessage string
	if err := pool.QueryRow(ctx, `SELECT details->>'previousMessage' FROM work_task_activities WHERE task_id=$1 AND kind='SOURCE_UPDATED'`, first[0].id).Scan(&previousMessage); err != nil || previousMessage != first[0].message {
		t.Fatalf("source update history = %q, %v", previousMessage, err)
	}
	if _, err := m.CompleteTask(ctx, work.CompleteTaskCommand{Identity: identity, TaskID: first[0].id, ExpectedVersion: first[0].version}); !errors.Is(err, work.ErrConflict) {
		t.Fatalf("stale completion cleared newer appointment evidence: %v", err)
	}
	if _, err := m.CompleteTask(ctx, work.CompleteTaskCommand{Identity: identity, TaskID: first[0].id, ExpectedVersion: updated[0].version}); err != nil {
		t.Fatal(err)
	}

	ensure(springHill, "synthetic-spring-hill", "Review booked appointment", "Verify insurance and provider for this appointment.", now)
	if got := reviews(); len(got) != 1 || got[0].state != "COMPLETED" {
		t.Fatalf("unchanged evidence reopened checked work: %#v", got)
	}
	ensure(springHill, "synthetic-spring-hill", "Review appointment change", "Verify insurance and provider for the rescheduled appointment.", now.Add(time.Minute))
	if got := reviews(); len(got) != 2 || got[0].state != "COMPLETED" || got[1].state != "OPEN" || got[1].title != "Review appointment change" {
		t.Fatalf("new appointment evidence after completion = %#v", got)
	}
}

type appointmentReview struct {
	id, title, message, state string
	version                   int64
}

func appointmentReviewHarness(t *testing.T, pool *pgxpool.Pool, m *work.Module, practice string) (func(string, string, string, string, time.Time), func() []appointmentReview) {
	ctx := context.Background()
	interactionID := uuid.NewString()
	ensure := func(locationID, sourceCallID, title, message string, at time.Time) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err := m.EnsureAppointmentReview(ctx, tx, interactionID, practice, locationID, "+15555550123", sourceCallID, title, message, at); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	reviews := func() []appointmentReview {
		t.Helper()
		rows, _ := pool.Query(ctx, `SELECT id::text,title,source_message,state,version FROM work_tasks WHERE origin='APPOINTMENT_REVIEW' ORDER BY created_at,state,id`)
		result, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (appointmentReview, error) {
			var item appointmentReview
			return item, row.Scan(&item.id, &item.title, &item.message, &item.state, &item.version)
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	return ensure, reviews
}

func TestCompletedCheckpointReviewCannotHideSameTimeCloseoutEvidence(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	a := access.New(pool, func() time.Time { return now })
	auth, identity := provisionStaff(t, a, now)
	m := work.New(pool, a, func() time.Time { return now })
	ensure, reviews := appointmentReviewHarness(t, pool, m, auth.Practice.ID)
	location, occurredAt := auth.Locations[0].ID, now.Add(-time.Minute)

	ensure(location, "synthetic-same-time", "Check appointment booking", "The agent could not confirm the booking.", occurredAt)
	checkpoint := reviews()
	if len(checkpoint) != 1 {
		t.Fatalf("checkpoint review = %#v", checkpoint)
	}
	if _, err := m.CompleteTask(ctx, work.CompleteTaskCommand{Identity: identity, TaskID: checkpoint[0].id, ExpectedVersion: checkpoint[0].version}); err != nil {
		t.Fatal(err)
	}

	ensure(location, "synthetic-same-time", "Review booked appointment", "Verify insurance and provider for this appointment.", occurredAt)
	ensure(location, "synthetic-same-time", "Review booked appointment", "Verify insurance and provider for this appointment.", occurredAt)
	got := reviews()
	if len(got) != 2 || got[0].id != checkpoint[0].id || got[0].state != "COMPLETED" || got[0].message != checkpoint[0].message ||
		got[1].state != "OPEN" || got[1].title != "Review booked appointment" {
		t.Fatalf("same-time closeout evidence after completed checkpoint review = %#v", got)
	}
}
