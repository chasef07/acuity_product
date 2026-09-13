package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestSharedReviewMigrationPreservesPendingHistoryAndReplay(t *testing.T) {
	ctx := context.Background()
	pool := testdb.OpenThrough(t, "0068_task_responsibilities.sql")
	if _, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000101','review-migration','Synthetic');
 INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000102','00000000-0000-0000-0000-000000000101','main','Main');
 INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,appointment_action,appointment_outcome,appointment_occurred_at)
 VALUES('00000000-0000-0000-0000-000000000103','synthetic','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','synthetic-call','+15555550123','+15555550100','2026-01-01','2026-01-01','COMPLETED',3,'BOOKED','BOOKING','2026-01-01 01:02:03.123450+00');
 INSERT INTO ai_interaction_attention(interaction_id,user_subject,outcome_occurred_at,created_at) VALUES('00000000-0000-0000-0000-000000000103','one','2026-01-01 01:02:03.123450+00','2026-01-01'),('00000000-0000-0000-0000-000000000103','two','2026-01-01 01:02:03.123450+00','2026-01-01');
 INSERT INTO messaging_threads(id,practice_id,location_id,office_phone,external_phone,created_at,updated_at) VALUES('00000000-0000-0000-0000-000000000104','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','+15555550100','+15555550123','2026-01-01','2026-01-01');
 INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,provider_message_id,created_at,updated_at)
 VALUES('00000000-0000-0000-0000-000000000105','00000000-0000-0000-0000-000000000104','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','INBOUND','Synthetic request','+15555550123','+15555550100','DELIVERED','synthetic-message','2026-01-01','2026-01-01');
 INSERT INTO messaging_thread_unreads(thread_id,user_subject,unread_since,latest_message_id) VALUES('00000000-0000-0000-0000-000000000104','one','2026-01-01','00000000-0000-0000-0000-000000000105'),('00000000-0000-0000-0000-000000000104','two','2026-01-01','00000000-0000-0000-0000-000000000105');
 UPDATE ai_interaction_attention SET reviewed_at='2026-01-02' WHERE user_subject='one';
 INSERT INTO messaging_threads(id,practice_id,location_id,office_phone,external_phone,created_at,updated_at) VALUES('00000000-0000-0000-0000-000000000106','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','+15555550100','+15555550124','2026-01-01','2026-01-01');
 INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,provider_message_id,created_at,updated_at) VALUES('00000000-0000-0000-0000-000000000107','00000000-0000-0000-0000-000000000106','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','INBOUND','STOP','+15555550124','+15555550100','DELIVERED','synthetic-stop','2026-01-01','2026-01-01');
 INSERT INTO messaging_thread_unreads(thread_id,user_subject,unread_since,latest_message_id) VALUES('00000000-0000-0000-0000-000000000106','one','2026-01-01','00000000-0000-0000-0000-000000000107');

 `); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 1, 2, 3, 123450000, time.UTC)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	m := work.New(pool, access.New(pool, nil), nil)
	if err := m.EnsureAppointmentReview(ctx, tx, "00000000-0000-0000-0000-000000000103", practiceID, locationID, "+15555550123", "synthetic-call", "BOOKED", "Synthetic verification", at); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var tasks, activities, unreads, attention, acknowledgements int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM work_tasks),(SELECT count(*) FROM work_task_activities),(SELECT count(*) FROM messaging_thread_unreads),(SELECT count(*) FROM ai_interaction_attention),(SELECT count(*) FROM work_task_acknowledgements)`).Scan(&tasks, &activities, &unreads, &attention, &acknowledgements); err != nil {
		t.Fatal(err)
	}
	if tasks != 2 || activities != 2 || unreads != 3 || attention != 2 || acknowledgements != 0 {
		t.Fatalf("migration/replay changed sources or duplicated work: %d %d %d %d %d", tasks, activities, unreads, attention, acknowledgements)
	}
	var created time.Time
	if err := pool.QueryRow(ctx, `SELECT created_at FROM work_tasks WHERE origin='APPOINTMENT_REVIEW'`).Scan(&created); err != nil || !created.Equal(at) {
		t.Fatalf("original outcome age lost: %s %v", created, err)
	}
}
