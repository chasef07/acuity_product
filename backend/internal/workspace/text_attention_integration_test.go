package workspace_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
)

func TestTextsFolderListsExactlyTheOpenTextReviews(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	a := access.New(db.As(testdb.Portal), clock)
	_, err := access.New(pool, clock).Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "text-age-test", Practices: []access.PracticeProvision{{Key: "text-age", Name: "Synthetic Text Age", Locations: []access.LocationProvision{{Key: "office", Name: "Synthetic Office"}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: "staff@text-age.test", Role: access.RoleAdmin, LocationScope: access.LocationScopeAll}}}}})
	if err != nil {
		t.Fatal(err)
	}
	identity := access.Identity{Subject: "text-age-staff", Email: "staff@text-age.test", EmailVerified: true}
	auth := testaccess.Activate(t, a, identity)
	var locationID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_locations WHERE practice_id=$1`, auth.Practice.ID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	m := work.New(db.As(testdb.Portal), a, clock)
	sweep := work.New(db.As(testdb.Worker), access.New(db.As(testdb.Worker), clock), clock)
	inboundText := func(patient int, age time.Duration) {
		t.Helper()
		phone := fmt.Sprintf("+1555555%04d", patient)
		at := now.Add(-age)
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		var threadID string
		if err := tx.QueryRow(ctx, `INSERT INTO messaging_threads(practice_id,location_id,office_phone,external_phone) VALUES($1,$2,'+15555550100',$3) RETURNING id::text`, auth.Practice.ID, locationID, phone).Scan(&threadID); err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,provider_message_id,created_at,updated_at) VALUES($1::uuid,$2,$3,$4,'INBOUND','Synthetic question',$5,'+15555550100','DELIVERED',$1::text,$6,$6)`, id, threadID, auth.Practice.ID, locationID, phone, at); err != nil {
			t.Fatal(err)
		}
		if err = m.EnsureInboundMessageReview(ctx, tx, auth.Practice.ID, locationID, phone, threadID, id, at); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	reads := workspace.New(db.As(testdb.Portal), a)
	assertTextsFolderIsTheOpenReviews := func(want int) {
		t.Helper()
		var open int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM work_tasks WHERE origin='INBOUND_MESSAGE_REVIEW' AND state='OPEN'`).Scan(&open); err != nil || open != want {
			t.Fatalf("open text reviews = %d, %v; want %d", open, err, want)
		}
		for _, grouped := range []bool{false, true} {
			page, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Kind: "texts", Grouped: grouped, Limit: 10})
			if err != nil || len(page.Items) != open || page.Counts == nil || page.Counts.Texts != open {
				t.Fatalf("Texts folder grouped=%v lists %d and counts %#v, %v; want the %d open text reviews", grouped, len(page.Items), page.Counts, err, open)
			}
		}
		all, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Limit: 10})
		if err != nil || len(all.Items) != open || all.Counts == nil || all.Counts.Texts != open {
			t.Fatalf("all open Tasks = %d with counts %#v, %v; want %d", len(all.Items), all.Counts, err, open)
		}
	}

	for patient, age := range []time.Duration{time.Hour, 50 * time.Hour, 119 * time.Hour, 121 * time.Hour, 300 * time.Hour} {
		inboundText(patient, age)
	}
	assertTextsFolderIsTheOpenReviews(5)

	closed, err := sweep.ExpireTextReviews(ctx)
	if err != nil || closed != 2 {
		t.Fatalf("ExpireTextReviews = %d, %v; want the 2 reviews older than 120 hours closed", closed, err)
	}
	assertTextsFolderIsTheOpenReviews(3)

	completed, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Kind: "texts", State: work.TaskCompleted, Limit: 10})
	if err != nil || len(completed.Items) != 2 {
		t.Fatalf("completed text reviews = %#v, %v; want the 2 expired ones", completed, err)
	}
	for _, task := range completed.Items {
		if task.CloseReason != work.CloseReasonExpired || task.CompletedBy == nil || task.CompletedBy.Kind != "SERVICE" {
			t.Fatalf("expired text review = %#v, want it closed as EXPIRED by a service", task)
		}
	}

	inboundText(9, 0)
	assertTextsFolderIsTheOpenReviews(4)
}
