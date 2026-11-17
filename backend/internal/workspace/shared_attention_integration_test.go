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
)

func TestOpenTasksStaySharedWithinAuthorizedLocations(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Now().UTC()
	a := access.New(db.As(testdb.Portal), func() time.Time { return now })
	staff := []string{"call-center", "insurance", "optical"}
	grants := []access.AccessGrantProvision{
		{Key: "admin", Email: "admin@shared-review.test", Role: access.RoleAdmin, LocationScope: access.LocationScopeAll},
	}
	for _, key := range staff {
		grants = append(grants, access.AccessGrantProvision{Key: key, Email: key + "@shared-review.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"allowed"}})
	}
	if _, err := access.New(pool, func() time.Time { return now }).Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "shared-review-test", Practices: []access.PracticeProvision{{
		Key: "shared-review", Name: "Synthetic Shared Review", AccessGrants: grants,
		Locations: []access.LocationProvision{{Key: "allowed", Name: "Allowed", AbitaOfficeKeys: []string{"allowed", "spring-hill"}}, {Key: "denied", Name: "Denied", AbitaOfficeKeys: []string{"denied"}}},
	}}}); err != nil {
		t.Fatal(err)
	}
	admin := access.Identity{Subject: "shared-review-admin", Email: "admin@shared-review.test", EmailVerified: true}
	auth := testaccess.Activate(t, a, admin)
	m := work.New(db.As(testdb.Portal), a, func() time.Time { return now })
	reads := workspace.New(db.As(testdb.Portal), a)
	locations := map[string]string{}
	for _, key := range []string{"allowed", "denied"} {
		var locationID string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM access_locations WHERE practice_id=$1 AND provisioning_key=$2`, auth.Practice.ID, key).Scan(&locationID); err != nil {
			t.Fatal(err)
		}
		locations[key] = locationID
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		for _, outcome := range []work.RecoveryOutcome{work.RecoveryOutcomeMissedCall, work.RecoveryOutcomeVoicemail} {
			phone := "+15555550124"
			if outcome == work.RecoveryOutcomeVoicemail {
				phone = "+15555550125"
			}
			var callID string
			if err := tx.QueryRow(ctx, `INSERT INTO human_calling_calls(practice_id,location_id,direction,entry_point,terminal_outcome,caller_phone,ended_at,created_at,updated_at) VALUES($1,$2,'INBOUND','STANDALONE','VOICEMAIL',$4,$3,$3,$3) RETURNING id::text`, auth.Practice.ID, locationID, now, phone).Scan(&callID); err != nil {
				t.Fatal(err)
			}
			if _, err := m.EnsureRecoveryTask(ctx, tx, work.EnsureRecoveryTaskCommand{CallID: callID, PracticeID: auth.Practice.ID, LocationID: locationID, Phone: phone, Outcome: outcome, OccurredAt: now}); err != nil {
				t.Fatal(err)
			}
		}
		var threadID string
		if err := tx.QueryRow(ctx, `INSERT INTO messaging_threads(practice_id,location_id,office_phone,external_phone,created_at,updated_at) VALUES($1,$2,'+15555550100','+15555550123',$3,$3) RETURNING id::text`, auth.Practice.ID, locationID, now).Scan(&threadID); err != nil {
			t.Fatal(err)
		}
		messageID := uuid.NewString()
		if _, err := tx.Exec(ctx, `INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,provider_message_id,created_at,updated_at) VALUES($1::uuid,$2,$3,$4,'INBOUND','Synthetic request','+15555550123','+15555550100','DELIVERED',$1::text,$5,$5)`, messageID, threadID, auth.Practice.ID, locationID, now); err != nil {
			t.Fatal(err)
		}
		if err := m.EnsureInboundMessageReview(ctx, tx, auth.Practice.ID, locationID, "+15555550123", threadID, messageID, now); err != nil {
			t.Fatal(err)
		}
		if err := m.EnsureAppointmentReview(ctx, tx, uuid.NewString(), auth.Practice.ID, locationID, "+15555550126", "synthetic-appointment-"+key, "BOOKED", "Check the synthetic appointment.", now); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if _, _, err := m.CreateAITask(ctx, work.CreateAITaskCommand{
			Service:   access.ServiceIdentity{Subject: "synthetic-agent", PracticeID: auth.Practice.ID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}},
			OfficeKey: key, OfficePhone: "+15555550100", SourceCallID: key, IdempotencyKey: key,
			Phone: "+15555550123", Summary: "Records request", Message: "Synthetic documentation follow-up", Category: work.TaskCategoryDocumentation, Urgency: work.TaskUrgencyNormal,
		}); err != nil {
			t.Fatal(err)
		}
	}
	identity := func(key string) access.Identity {
		return access.Identity{Subject: key, Email: key + "@shared-review.test", EmailVerified: true}
	}
	for _, key := range staff {
		testaccess.Activate(t, a, identity(key))
		page, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity(key), PracticeID: auth.Practice.ID})
		if err != nil || len(page.Items) != 5 {
			t.Fatalf("%s: rows=%d err=%v; want the 4 reviews and the follow-up at the allowed Location", key, len(page.Items), err)
		}
		for _, task := range page.Items {
			if task.LocationID != locations["allowed"] {
				t.Fatalf("%s: leaked a Task at an unauthorized Location: %s", key, task.ID)
			}
		}
		if _, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity(key), PracticeID: auth.Practice.ID, LocationID: locations["denied"]}); !errors.Is(err, workspace.ErrDenied) {
			t.Fatalf("%s: explicit unauthorized Location: %v", key, err)
		}
	}
	everything, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: admin, PracticeID: auth.Practice.ID})
	perLocation := map[string]int{}
	for _, task := range everything.Items {
		perLocation[task.LocationID]++
	}
	if err != nil || perLocation[locations["allowed"]] != 5 || perLocation[locations["denied"]] == 0 {
		t.Fatalf("admin rows per Location=%v err=%v; want the 5 at the allowed Location and the denied Location's too", perLocation, err)
	}
	checkingStaff := identity("call-center")
	var appointment work.Task
	for _, task := range everything.Items {
		if task.Origin == work.TaskOriginAppointmentReview && task.LocationID == locations["allowed"] {
			appointment = task
		}
	}
	if _, err := m.CompleteTask(ctx, work.CompleteTaskCommand{Reason: work.CloseReasonDone, Identity: checkingStaff, TaskID: appointment.ID, ExpectedVersion: appointment.Version}); err != nil {
		t.Fatal(err)
	}
	for _, key := range staff {
		page, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity(key), PracticeID: auth.Practice.ID})
		if err != nil || len(page.Items) != 4 {
			t.Fatalf("%s retained a checked appointment review: rows=%d err=%v", key, len(page.Items), err)
		}
	}
}
