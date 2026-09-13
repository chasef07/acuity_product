package work_test

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
)

func TestTextReplyCompletionTracksProviderEvidence(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Now().UTC()
	a := access.New(pool, func() time.Time { return now })
	auth, identity := provisionStaff(t, a, now)
	m := work.New(pool, a, func() time.Time { return now })
	var thread string
	if err := pool.QueryRow(ctx, `INSERT INTO messaging_threads(practice_id,location_id,office_phone,external_phone,created_at,updated_at) VALUES($1,$2,'+15555550100','+15555550123',$3,$3) RETURNING id::text`, auth.Practice.ID, auth.Locations[0].ID, now).Scan(&thread); err != nil {
		t.Fatal(err)
	}
	insert := func(inbound bool) string {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		id := uuid.NewString()
		direction, delivery := "OUTBOUND", "SENDING"
		if inbound {
			direction, delivery = "INBOUND", "DELIVERED"
		}
		if _, err = tx.Exec(ctx, `INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,created_by_kind,created_by_subject,created_at,updated_at) VALUES($1,$2,$3,$4,$5,'Synthetic text','+15555550123','+15555550100',$6,CASE WHEN $5='OUTBOUND' THEN 'HUMAN' ELSE NULL END,CASE WHEN $5='OUTBOUND' THEN 'synthetic-staff' ELSE NULL END,$7,$7)`, id, thread, auth.Practice.ID, auth.Locations[0].ID, direction, delivery, now); err != nil {
			t.Fatal(err)
		}
		if inbound {
			err = m.EnsureInboundMessageReview(ctx, tx, auth.Practice.ID, auth.Locations[0].ID, "+15555550123", thread, id, now)
		} else {
			err = m.CaptureTextReply(ctx, tx, thread, id, access.Actor{Subject: identity.Subject, Email: identity.Email})
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return id
	}
	apply := func(id, state string) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, `UPDATE messaging_messages SET delivery_state=$2 WHERE id=$1`, id, state); err != nil {
			t.Fatal(err)
		}
		if err = m.ApplyTextReply(ctx, tx, id); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertOpen := func(want int) {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM work_tasks WHERE state='OPEN'`).Scan(&n); err != nil || n != want {
			t.Fatalf("open=%d want=%d err=%v", n, want, err)
		}
	}
	insert(true)
	r := insert(false)
	apply(r, "SENDING")
	assertOpen(1)
	apply(r, "FAILED")
	assertOpen(1)
	r = insert(false)
	insert(true)
	apply(r, "SENT")
	assertOpen(1) // newer evidence survives
	r = insert(false)
	second := insert(false)
	apply(r, "SENT")
	assertOpen(0)
	apply(second, "SENT")
	apply(r, "FAILED")
	assertOpen(0)
	apply(second, "FAILED")
	assertOpen(1)
	r = insert(false)
	apply(r, "UNKNOWN")
	assertOpen(1)
	apply(r, "DELIVERED")
	assertOpen(0)
	apply(r, "DELIVERED")
	assertOpen(0)
	var actor string
	if err := pool.QueryRow(ctx, `SELECT completed_by_email FROM work_tasks WHERE state='COMPLETED'`).Scan(&actor); err != nil || actor != identity.Email {
		t.Fatalf("actor=%s err=%v", actor, err)
	}
	insert(true)
	apply(r, "FAILED")
	assertOpen(1) // a newer review already owns the follow-up
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.EnsureAppointmentReview(ctx, tx, uuid.NewString(), auth.Practice.ID, auth.Locations[0].ID, "+15555550123", "synthetic-separate-obligation", "BOOKED", "Verify this synthetic appointment.", now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r = insert(false)
	apply(r, "SENT")
	assertOpen(1) // replying never completes the appointment verification
	var openOrigin string
	if err = pool.QueryRow(ctx, `SELECT origin FROM work_tasks WHERE state='OPEN'`).Scan(&openOrigin); err != nil || openOrigin != "APPOINTMENT_REVIEW" {
		t.Fatalf("remaining work=%s err=%v", openOrigin, err)
	}

}
