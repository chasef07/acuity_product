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

func TestCommunicationReviewsStaySharedWithinAuthorizedLocations(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	a := access.New(pool, func() time.Time { return now })
	staff := []struct {
		key      string
		category work.TaskCategory
		role     string
	}{
		{"call-center", work.TaskCategoryAppointments, "primary"},
		{"insurance", work.TaskCategoryInsurance, "backup"},
		{"optical", work.TaskCategoryOptical, "primary"},
	}
	grants := []access.AccessGrantProvision{{Key: "admin", Email: "admin@shared-review.test", Role: access.RoleAdmin, LocationScope: access.LocationScopeAll}}
	for _, member := range staff {
		grants = append(grants, access.AccessGrantProvision{Key: member.key, Email: member.key + "@shared-review.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"allowed"}})
	}
	if _, err := a.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "shared-review-test", Practices: []access.PracticeProvision{{
		Key: "shared-review", Name: "Synthetic Shared Review", AccessGrants: grants,
		Locations: []access.LocationProvision{{Key: "allowed", Name: "Allowed", AbitaOfficeKeys: []string{"allowed"}}, {Key: "denied", Name: "Denied", AbitaOfficeKeys: []string{"denied"}}},
	}}}); err != nil {
		t.Fatal(err)
	}
	admin := access.Identity{Subject: "shared-review-admin", Email: "admin@shared-review.test", EmailVerified: true}
	auth := testaccess.Activate(t, a, admin)
	m := work.New(pool, a, func() time.Time { return now })
	reads := workspace.New(pool, a)
	locations := map[string]string{}
	for _, key := range []string{"allowed", "denied"} {
		var locationID string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM access_locations WHERE practice_id=$1 AND provisioning_key=$2`, auth.Practice.ID, key).Scan(&locationID); err != nil {
			t.Fatal(err)
		}
		locations[key] = locationID
		if _, err := pool.Exec(ctx, `INSERT INTO work_responsibility_locations VALUES($1,$2)`, auth.Practice.ID, locationID); err != nil {
			t.Fatal(err)
		}
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
	for _, member := range staff {
		t.Run(member.key, func(t *testing.T) {
			identity := access.Identity{Subject: member.key, Email: member.key + "@shared-review.test", EmailVerified: true}
			testaccess.Activate(t, a, identity)
			if _, err := pool.Exec(ctx, `INSERT INTO work_responsibilities VALUES($1,$2,$3,$4,$5)`, auth.Practice.ID, locations["allowed"], identity.Email, member.category, member.role); err != nil {
				t.Fatal(err)
			}
			for _, classified := range []bool{false, true} {
				category := any(nil)
				if classified {
					category = "documentation"
				}
				if _, err := pool.Exec(ctx, `UPDATE work_tasks SET category=$1 WHERE origin IN ('INBOUND_MESSAGE_REVIEW','MISSED_CALL_RECOVERY','VOICEMAIL_RECOVERY')`, category); err != nil {
					t.Fatal(err)
				}
				for _, kind := range []string{"", "texts", "calls"} {
					page, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Responsibility: "mine", Kind: kind, Grouped: true})
					want := map[string]int{"": 4, "texts": 1, "calls": 2}[kind]
					if err != nil || len(page.Items) != want || page.Counts == nil || page.Counts.Tasks != 4 || page.Counts.Texts != 1 || page.Counts.CallRecovery != 2 || page.Counts.Categories.Appointments != 1 {
						t.Fatalf("classified=%t kind=%q: rows=%d counts=%+v err=%v; want %d visible shared reviews and counts 4/1/2 with one appointment", classified, kind, len(page.Items), page.Counts, err, want)
					}
					for _, task := range page.Items {
						if task.LocationID != locations["allowed"] || task.Origin == work.TaskOriginAbitaAI {
							t.Fatalf("leaked unauthorized or another category's ordinary Task: %s", task.ID)
						}
					}
				}
			}
			all, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Responsibility: "all"})
			if err != nil || len(all.Items) != 5 || all.Counts.Tasks != 5 {
				t.Fatalf("All tasks must include authorized documentation but no other location: rows=%d err=%v", len(all.Items), err)
			}
			if _, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, LocationID: locations["denied"], Responsibility: "all"}); !errors.Is(err, workspace.ErrDenied) {
				t.Fatalf("explicit unauthorized location: %v", err)
			}
		})
	}
	// One staff member checking an appointment clears that shared review for all.
	checkingStaff := access.Identity{Subject: "call-center", Email: "call-center@shared-review.test", EmailVerified: true}
	appointments, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: checkingStaff, PracticeID: auth.Practice.ID, Responsibility: "mine", Category: work.TaskCategoryAppointments})
	if err != nil || len(appointments.Items) != 1 {
		t.Fatalf("appointment review query: rows=%d err=%v", len(appointments.Items), err)
	}
	appointment := appointments.Items[0]
	if _, err := m.CompleteTask(ctx, work.CompleteTaskCommand{Identity: checkingStaff, TaskID: appointment.ID, ExpectedVersion: appointment.Version}); err != nil {
		t.Fatal(err)
	}
	for _, member := range staff {
		identity := access.Identity{Subject: member.key, Email: member.key + "@shared-review.test", EmailVerified: true}
		page, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{Identity: identity, PracticeID: auth.Practice.ID, Responsibility: "mine"})
		if err != nil || len(page.Items) != 3 || page.Counts.Categories.Appointments != 0 {
			t.Fatalf("%s retained a checked appointment: rows=%d counts=%+v err=%v", member.key, len(page.Items), page.Counts, err)
		}
	}
}
