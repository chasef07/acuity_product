package workspace_test

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
)

func TestTextAttentionExpiresWithoutCompletingWorkAndReturnsOnInbound(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Now().UTC()
	a := access.New(pool, func() time.Time { return now })
	_, err := a.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "text-age-test", Practices: []access.PracticeProvision{{Key: "text-age", Name: "Synthetic Text Age", Locations: []access.LocationProvision{{Key: "office", Name: "Synthetic Office"}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: "staff@text-age.test", Role: access.RoleAdmin, LocationScope: access.LocationScopeAll}}}}})
	if err != nil {
		t.Fatal(err)
	}
	identity := access.Identity{Subject: "text-age-staff", Email: "staff@text-age.test", EmailVerified: true}
	auth := testaccess.Activate(t, a, identity)
	var locationID, threadID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_locations WHERE practice_id=$1`, auth.Practice.ID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO messaging_threads(practice_id,location_id,office_phone,external_phone) VALUES($1,$2,'+15555550100','+15555550123') RETURNING id::text`, auth.Practice.ID, locationID).Scan(&threadID); err != nil {
		t.Fatal(err)
	}
	m := work.New(pool, a, func() time.Time { return now })
	addMessage := func(direction string, at time.Time) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		id := uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,provider_message_id,created_by_subject,created_at,updated_at) VALUES($1::uuid,$2,$3,$4,$5,'Synthetic question','+15555550123','+15555550100','DELIVERED',$1::text,CASE WHEN $5='OUTBOUND' THEN 'synthetic-staff' END,$6,$6)`, id, threadID, auth.Practice.ID, locationID, direction, at); err != nil {
			t.Fatal(err)
		}
		if direction == "INBOUND" {
			if err = m.EnsureInboundMessageReview(ctx, tx, auth.Practice.ID, locationID, "+15555550123", threadID, id, at); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	addMessage("INBOUND", now.Add(-120*time.Hour-time.Minute))
	reads := workspace.New(pool, a)
	check := func(want int) {
		t.Helper()
		for _, grouped := range []bool{false, true} {
			page, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Kind: "texts", Grouped: grouped, Limit: 1})
			if err != nil || len(page.Items) != want || page.Counts == nil || page.Counts.Texts != want {
				t.Fatalf("text attention grouped=%v: %#v, %v; want %d", grouped, page, err, want)
			}
		}
		all, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID})
		if err != nil || len(all.Items) != 1 || all.Items[0].State != work.TaskOpen || all.Counts.Texts != want {
			t.Fatalf("preserved open work = %#v, %v", all, err)
		}
	}
	check(0)
	// An outbound message or a Task edit must not renew the inbound window.
	addMessage("OUTBOUND", now)
	if _, err := pool.Exec(ctx, `UPDATE work_tasks SET updated_at=$2 WHERE message_thread_id=$1`, threadID, now); err != nil {
		t.Fatal(err)
	}
	check(0)
	addMessage("INBOUND", now.Add(-120*time.Hour+time.Minute))
	check(1)
	// Completion still preserves the review in history.
	page, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID})
	if err != nil {
		t.Fatal(err)
	}
	task := page.Items[0]
	if _, err := m.CompleteTask(ctx, work.CompleteTaskCommand{Identity: identity, TaskID: task.ID, ExpectedVersion: task.Version}); err != nil {
		t.Fatal(err)
	}
	completed, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Kind: "texts", State: work.TaskCompleted})
	if err != nil || len(completed.Items) != 1 {
		t.Fatalf("completed history = %#v, %v", completed, err)
	}
}
