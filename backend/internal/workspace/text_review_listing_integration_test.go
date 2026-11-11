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

type textReviewFixture struct {
	t          *testing.T
	ctx        context.Context
	db         *testdb.DB
	now        time.Time
	identity   access.Identity
	auth       access.Authorization
	locationID string
	staff      *work.Module
	sweep      *work.Module
	reads      *workspace.Module
}

func newTextReviewFixture(t *testing.T) *textReviewFixture {
	t.Helper()
	f := &textReviewFixture{t: t, ctx: context.Background(), db: testdb.Open(t), now: time.Now().UTC()}
	clock := func() time.Time { return f.now }
	portal := access.New(f.db.As(testdb.Portal), clock)
	if _, err := access.New(f.db.Owner, clock).Provision(f.ctx, access.Provisioning{Environment: "test", RequestedBy: "text-review-test", Practices: []access.PracticeProvision{{Key: "text-review", Name: "Synthetic Text Reviews", Locations: []access.LocationProvision{{Key: "office", Name: "Synthetic Office"}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: "staff@text-review.test", Role: access.RoleAdmin, LocationScope: access.LocationScopeAll}}}}}); err != nil {
		t.Fatal(err)
	}
	f.identity = access.Identity{Subject: "text-review-staff", Email: "staff@text-review.test", EmailVerified: true}
	f.auth = testaccess.Activate(t, portal, f.identity)
	if err := f.db.Owner.QueryRow(f.ctx, `SELECT id::text FROM access_locations WHERE practice_id=$1`, f.auth.Practice.ID).Scan(&f.locationID); err != nil {
		t.Fatal(err)
	}
	f.staff = work.New(f.db.As(testdb.Portal), portal, clock)
	f.sweep = work.New(f.db.As(testdb.Worker), access.New(f.db.As(testdb.Worker), clock), clock)
	f.reads = workspace.New(f.db.As(testdb.Portal), portal)
	return f
}

func (f *textReviewFixture) inboundText(patient int, age time.Duration) string {
	f.t.Helper()
	phone := fmt.Sprintf("+1555555%04d", patient)
	at := f.now.Add(-age)
	tx, err := f.db.Owner.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	var threadID string
	if err := tx.QueryRow(f.ctx, `INSERT INTO messaging_threads(practice_id,location_id,office_phone,external_phone) VALUES($1,$2,'+15555550100',$3) RETURNING id::text`, f.auth.Practice.ID, f.locationID, phone).Scan(&threadID); err != nil {
		f.t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err = tx.Exec(f.ctx, `INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,provider_message_id,created_at,updated_at) VALUES($1::uuid,$2,$3,$4,'INBOUND','Synthetic question',$5,'+15555550100','DELIVERED',$1::text,$6,$6)`, id, threadID, f.auth.Practice.ID, f.locationID, phone, at); err != nil {
		f.t.Fatal(err)
	}
	if err = f.staff.EnsureInboundMessageReview(f.ctx, tx, f.auth.Practice.ID, f.locationID, phone, threadID, id, at); err != nil {
		f.t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	return threadID
}

func (f *textReviewFixture) expire(want int) {
	f.t.Helper()
	closed, err := f.sweep.ExpireTextReviews(f.ctx)
	if err != nil || closed != want {
		f.t.Fatalf("ExpireTextReviews = %d, %v; want %d closed", closed, err, want)
	}
}

func (f *textReviewFixture) completeAsStaff(threadID string) {
	f.t.Helper()
	var id string
	var version int64
	if err := f.db.Owner.QueryRow(f.ctx, `SELECT id::text,version FROM work_tasks WHERE message_thread_id=$1 AND state='OPEN'`, threadID).Scan(&id, &version); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.staff.CompleteTask(f.ctx, work.CompleteTaskCommand{Identity: f.identity, TaskID: id, ExpectedVersion: version, Reason: work.CloseReasonDone}); err != nil {
		f.t.Fatalf("complete review as staff: %v", err)
	}
}

func (f *textReviewFixture) query(command workspace.QueryTasksCommand) work.TaskPage {
	f.t.Helper()
	command.Identity, command.PracticeID, command.Limit = f.identity, f.auth.Practice.ID, 10
	page, err := f.reads.QueryTasks(f.ctx, command)
	if err != nil {
		f.t.Fatalf("query Tasks %#v: %v", command, err)
	}
	return page
}

func TestTextsFolderListsExactlyTheOpenTextReviews(t *testing.T) {
	f := newTextReviewFixture(t)
	assertTextsFolderIsTheOpenReviews := func(want int) {
		t.Helper()
		var open int
		if err := f.db.Owner.QueryRow(f.ctx, `SELECT count(*) FROM work_tasks WHERE origin='INBOUND_MESSAGE_REVIEW' AND state='OPEN'`).Scan(&open); err != nil || open != want {
			t.Fatalf("open text reviews = %d, %v; want %d", open, err, want)
		}
		for _, command := range []workspace.QueryTasksCommand{{Kind: "texts"}, {Kind: "texts", Grouped: true}, {}} {
			page := f.query(command)
			if len(page.Items) != open || page.Counts == nil || page.Counts.Texts != open {
				t.Fatalf("kind %q grouped %v lists %d and counts %#v; want the %d open text reviews", command.Kind, command.Grouped, len(page.Items), page.Counts, open)
			}
		}
	}

	for patient, age := range []time.Duration{time.Hour, 50 * time.Hour, 119 * time.Hour, 121 * time.Hour, 300 * time.Hour} {
		f.inboundText(patient, age)
	}
	assertTextsFolderIsTheOpenReviews(5)

	f.expire(2)
	assertTextsFolderIsTheOpenReviews(3)

	f.inboundText(9, 0)
	assertTextsFolderIsTheOpenReviews(4)
}

func TestRecentlyCompletedLeavesOutExpiredReviewsAndSearchStillFindsThem(t *testing.T) {
	f := newTextReviewFixture(t)
	answered := f.inboundText(1, time.Hour)
	f.inboundText(2, 121*time.Hour)
	f.inboundText(3, 300*time.Hour)
	f.completeAsStaff(answered)
	f.expire(2)

	for _, kind := range []string{"", "texts"} {
		page := f.query(workspace.QueryTasksCommand{State: work.TaskCompleted, Kind: kind})
		if len(page.Items) != 1 || page.Items[0].CloseReason != work.CloseReasonDone {
			t.Fatalf("completed Tasks of kind %q = %#v; want only the review staff closed as DONE", kind, page.Items)
		}
	}
	found := f.query(workspace.QueryTasksCommand{State: work.TaskCompleted, Search: "5555550002"})
	if len(found.Items) != 1 || found.Items[0].CloseReason != work.CloseReasonExpired ||
		found.Items[0].CompletedBy == nil || found.Items[0].CompletedBy.Kind != "SERVICE" {
		t.Fatalf("search for the expired review's phone = %#v; want it, closed as EXPIRED by a service", found.Items)
	}
}
