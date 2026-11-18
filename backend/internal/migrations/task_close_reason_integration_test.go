package migrations_test

import (
	"context"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestCompletedTasksAreBackfilledWithACloseReason(t *testing.T) {
	ctx := context.Background()
	pool := testdb.OpenThrough(t, "0120_delete_media_jwt_commands.sql")
	const (
		practice           = "00000000-0000-0000-0000-000000000301"
		location           = "00000000-0000-0000-0000-000000000302"
		open               = "00000000-0000-0000-0000-000000000311"
		staffCompleted     = "00000000-0000-0000-0000-000000000312"
		duplicate          = "00000000-0000-0000-0000-000000000313"
		duplicateThenStaff = "00000000-0000-0000-0000-000000000314"
		inboundCall        = "00000000-0000-0000-0000-000000000315"
		alreadyRetired     = "00000000-0000-0000-0000-000000000316"
		retiredBy0121      = "00000000-0000-0000-0000-000000000317"
		noCompletionRecord = "00000000-0000-0000-0000-000000000318"
	)
	if _, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name) VALUES('`+practice+`','close-reason','Synthetic');
 INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES('`+location+`','`+practice+`','office','Office');
 INSERT INTO work_tasks(id,practice_id,location_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_at,updated_at,version,source_call_id,category,source_message,ai_idempotency_key,ai_input_fingerprint,completed_by_kind,completed_by_subject,completed_by_email,completed_at)
  SELECT id::uuid,'`+practice+`','`+location+`','+15555550140','Synthetic request '||id,state,'ABITA_AI','normal','SERVICE','abita','2026-09-01','2026-09-04',version,id,'other','Synthetic evidence',id,decode(repeat('00',32),'hex'),completed_kind,completed_subject,completed_email,completed_at::timestamptz
  FROM (VALUES
   ('`+open+`','OPEN',1,NULL,NULL,NULL,NULL),
   ('`+staffCompleted+`','COMPLETED',2,'HUMAN','staff','staff@example.test','2026-09-03'),
   ('`+duplicate+`','COMPLETED',2,'SERVICE','work-recovery-resolution',NULL,'2026-09-03'),
   ('`+duplicateThenStaff+`','COMPLETED',4,'HUMAN','staff','staff@example.test','2026-09-04'),
   ('`+inboundCall+`','COMPLETED',2,'SERVICE','work-recovery-resolution',NULL,'2026-09-03'),
   ('`+alreadyRetired+`','COMPLETED',2,'SERVICE','hidden-review-retirement',NULL,'2026-09-03'),
   ('`+noCompletionRecord+`','COMPLETED',1,'HUMAN','staff','staff@example.test','2026-09-03')
  ) AS fixture(id,state,version,completed_kind,completed_subject,completed_email,completed_at);
 INSERT INTO work_tasks(id,practice_id,location_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_at,updated_at,source_call_id,category,source_review_key,source_message) VALUES
  ('`+retiredBy0121+`','`+practice+`','`+location+`','+15555550140','Review booked appointment','OPEN','APPOINTMENT_REVIEW','normal','SERVICE','appointment-review','2026-09-01','2026-09-02','call-unrouted','appointments','review-unrouted','Verify insurance');
 INSERT INTO work_task_activities(task_id,task_version,kind,actor_kind,actor_subject,actor_email,occurred_at)
  SELECT id,1,'TASK_CREATED','SERVICE',created_by_subject,NULL,created_at FROM work_tasks;
 INSERT INTO work_task_activities(task_id,task_version,kind,actor_kind,actor_subject,actor_email,occurred_at) VALUES
  ('`+staffCompleted+`',2,'TASK_COMPLETED','HUMAN','staff','staff@example.test','2026-09-03'),
  ('`+duplicate+`',2,'TASK_AUTO_COMPLETED_DUPLICATE','SERVICE','work-recovery-resolution',NULL,'2026-09-03'),
  ('`+duplicateThenStaff+`',2,'TASK_AUTO_COMPLETED_DUPLICATE','SERVICE','work-recovery-resolution',NULL,'2026-09-02'),
  ('`+duplicateThenStaff+`',3,'TASK_REOPENED','HUMAN','staff','staff@example.test','2026-09-03'),
  ('`+duplicateThenStaff+`',4,'TASK_COMPLETED','HUMAN','staff','staff@example.test','2026-09-04'),
  ('`+inboundCall+`',2,'TASK_AUTO_COMPLETED_INBOUND_CALL','SERVICE','work-recovery-resolution',NULL,'2026-09-03'),
  ('`+alreadyRetired+`',2,'TASK_COMPLETED','SERVICE','hidden-review-retirement',NULL,'2026-09-03');
 `); err != nil {
		t.Fatal(err)
	}

	if err := migrations.ApplyThrough(ctx, pool, "0122_task_close_reason.sql"); err != nil {
		t.Fatal(err)
	}

	for id, want := range map[string]string{
		open:               "",
		staffCompleted:     "DONE",
		duplicate:          "NOT_NEEDED",
		duplicateThenStaff: "DONE",
		inboundCall:        "DONE",
		alreadyRetired:     "EXPIRED",
		retiredBy0121:      "EXPIRED",
		noCompletionRecord: "DONE",
	} {
		var got string
		var version int64
		if err := pool.QueryRow(ctx, `SELECT COALESCE(close_reason,''),version FROM work_tasks WHERE id=$1`, id).Scan(&got, &version); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("Task %s close reason = %q, want %q", id, got, want)
		}
		if id == staffCompleted && version != 2 {
			t.Errorf("the backfill moved Task %s to version %d", id, version)
		}
	}
	var withoutReason int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM work_tasks WHERE state='COMPLETED' AND close_reason IS NULL`).Scan(&withoutReason); err != nil || withoutReason != 0 {
		t.Fatalf("completed Tasks without a close reason = %d, %v", withoutReason, err)
	}

	for name, statement := range map[string]string{
		"an unknown reason":                 `UPDATE work_tasks SET close_reason='ABANDONED' WHERE id='` + staffCompleted + `'`,
		"a closing call that is not a call": `UPDATE work_tasks SET closed_via_call_id=gen_random_uuid() WHERE id='` + staffCompleted + `'`,
	} {
		if _, err := pool.Exec(ctx, statement); err == nil {
			t.Errorf("the database accepted %s", name)
		}
	}
	for name, statement := range map[string]string{
		"completes a Task":              `UPDATE work_tasks SET state='COMPLETED',completed_by_kind='HUMAN',completed_by_subject='staff',completed_by_email='staff@example.test',completed_at='2026-09-05',version=version+1 WHERE id='` + open + `'`,
		"reopens a Task closed as done": `UPDATE work_tasks SET state='OPEN',completed_by_kind=NULL,completed_by_subject=NULL,completed_by_email=NULL,completed_at=NULL,version=version+1 WHERE id='` + staffCompleted + `'`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Errorf("the database refused the previous backend when it %s: %v", name, err)
		}
	}
}
