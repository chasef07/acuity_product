package work_test

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
)

func TestBlockingClosesOnlyTextReviewsTheBlockerCanActOn(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	now := time.Now().UTC()
	a := access.New(db.As(testdb.Portal), func() time.Time { return now })
	auth, _ := provisionStaff(t, db.Owner, a, now)
	provisionColleagues(t, db.Owner, now)
	elsewhere := access.Identity{Subject: "synthetic-elsewhere-subject", Email: colleagueCoveringOnlyLocation2, EmailVerified: true}
	elsewhereAuth := testaccess.Activate(t, a, elsewhere)
	if len(elsewhereAuth.Locations) != 1 || elsewhereAuth.Locations[0].ID == auth.Locations[0].ID {
		t.Fatalf("colleague locations = %+v, want only the second office", elsewhereAuth.Locations)
	}
	m := work.New(db.As(testdb.Portal), a, func() time.Time { return now })
	const phone = "+15555550123"
	openReview := func(locationID string) string {
		t.Helper()
		tx, err := db.Owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		var thread string
		if err := tx.QueryRow(ctx, `INSERT INTO messaging_threads(practice_id,location_id,office_phone,external_phone,created_at,updated_at) VALUES($1,$2,'+15555550100',$3,$4,$4) RETURNING id::text`, auth.Practice.ID, locationID, phone, now).Scan(&thread); err != nil {
			t.Fatal(err)
		}
		message := uuid.NewString()
		if _, err := tx.Exec(ctx, `INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,created_at,updated_at) VALUES($1,$2,$3,$4,'INBOUND','Synthetic spam',$5,'+15555550100','DELIVERED',$6,$6)`, message, thread, auth.Practice.ID, locationID, phone, now); err != nil {
			t.Fatal(err)
		}
		if err := m.EnsureInboundMessageReview(ctx, tx, auth.Practice.ID, locationID, phone, thread, message, "", now); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return locationID
	}
	firstOffice := openReview(auth.Locations[0].ID)
	secondOffice := openReview(elsewhereAuth.Locations[0].ID)

	tx, err := db.As(testdb.Portal).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := m.CloseTextReviewsForBlockedSender(ctx, tx, elsewhere, auth.Practice.ID, phone); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	for location, want := range map[string]string{firstOffice: "OPEN", secondOffice: "COMPLETED"} {
		var state, reason, completedBy string
		if err := db.Owner.QueryRow(ctx, `SELECT state, COALESCE(close_reason,''), COALESCE(completed_by_subject,'') FROM work_tasks WHERE location_id = $1 AND origin = 'INBOUND_MESSAGE_REVIEW'`, location).Scan(&state, &reason, &completedBy); err != nil {
			t.Fatal(err)
		}
		if state != want {
			t.Fatalf("review at %s is %s, want %s: a blocker closes only the offices they can act on", location, state, want)
		}
		if want == "COMPLETED" && (reason != string(work.CloseReasonNotNeeded) || completedBy != elsewhere.Subject) {
			t.Fatalf("closed review reason=%q by=%q, want NOT_NEEDED by the blocker", reason, completedBy)
		}
	}
}
