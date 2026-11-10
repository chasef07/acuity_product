package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

const retireHiddenReviewTasks = "0121_retire_hidden_review_tasks.sql"

type reviewTaskRow struct {
	State, CompletedKind, CompletedSubject, CompletedEmail string
	Version                                                int64
	CompletedAt                                            *time.Time
	UpdatedAt                                              time.Time
	Activities                                             int
}

func TestHiddenReviewTasksAreRetiredOnce(t *testing.T) {
	ctx := context.Background()
	pool := testdb.OpenThrough(t, "0120_delete_media_jwt_commands.sql")
	const (
		unroutedOpen      = "00000000-0000-0000-0000-000000000211"
		routedOpen        = "00000000-0000-0000-0000-000000000212"
		unroutedCompleted = "00000000-0000-0000-0000-000000000213"
		unroutedFollowUp  = "00000000-0000-0000-0000-000000000214"
		quietText         = "00000000-0000-0000-0000-000000000215"
		recentText        = "00000000-0000-0000-0000-000000000216"
	)
	if _, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000201','review-retirement','Synthetic');
 INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES
  ('00000000-0000-0000-0000-000000000202','00000000-0000-0000-0000-000000000201','unrouted','Unrouted'),
  ('00000000-0000-0000-0000-000000000203','00000000-0000-0000-0000-000000000201','routed','Routed');
 INSERT INTO access_abita_office_locations(practice_id,office_key,location_id) VALUES
  ('00000000-0000-0000-0000-000000000201','hollywood','00000000-0000-0000-0000-000000000202'),
  ('00000000-0000-0000-0000-000000000201','spring-hill','00000000-0000-0000-0000-000000000203');
 INSERT INTO work_tasks(id,practice_id,location_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_at,updated_at,version,source_call_id,category,source_review_key,source_message,completed_by_kind,completed_by_subject,completed_by_email,completed_at) VALUES
  ('`+unroutedOpen+`','00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000202','+15555550123','Review booked appointment','OPEN','APPOINTMENT_REVIEW','normal','SERVICE','appointment-review','2026-09-01','2026-09-02',2,'call-unrouted-open','appointments','review-unrouted-open','Verify insurance',NULL,NULL,NULL,NULL),
  ('`+routedOpen+`','00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000203','+15555550123','Review booked appointment','OPEN','APPOINTMENT_REVIEW','normal','SERVICE','appointment-review','2026-09-01','2026-09-01',1,'call-routed-open','appointments','review-routed-open','Verify insurance',NULL,NULL,NULL,NULL),
  ('`+unroutedCompleted+`','00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000202','+15555550123','Review cancelled appointment','COMPLETED','APPOINTMENT_REVIEW','normal','SERVICE','appointment-review','2026-09-01','2026-09-03',2,'call-unrouted-completed','appointments','review-unrouted-completed','Confirm cancellation','HUMAN','staff','staff@example.test','2026-09-03');
 INSERT INTO work_tasks(id,practice_id,location_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_at,updated_at,source_call_id,category,source_message,ai_idempotency_key,ai_input_fingerprint) VALUES
  ('`+unroutedFollowUp+`','00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000202','+15555550123','Refill question','OPEN','ABITA_AI','normal','SERVICE','abita','2026-09-01','2026-09-01','call-follow-up','medication','Caller asked about a refill','follow-up',decode(repeat('00',32),'hex'));
 INSERT INTO messaging_threads(id,practice_id,location_id,office_phone,external_phone,created_at,updated_at) VALUES
  ('00000000-0000-0000-0000-000000000221','00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000203','+15555550100','+15555550124','2026-09-01','2026-09-01'),
  ('00000000-0000-0000-0000-000000000222','00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000203','+15555550100','+15555550125','2026-09-01','2026-09-01');
 INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,provider_message_id,created_at,updated_at) VALUES
  ('00000000-0000-0000-0000-000000000231','00000000-0000-0000-0000-000000000221','00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000203','INBOUND','Quiet text','+15555550124','+15555550100','DELIVERED','quiet-text',now()-interval '121 hours',now()-interval '121 hours'),
  ('00000000-0000-0000-0000-000000000232','00000000-0000-0000-0000-000000000222','00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000203','INBOUND','Recent text','+15555550125','+15555550100','DELIVERED','recent-text',now()-interval '119 hours',now()-interval '119 hours');
 INSERT INTO work_tasks(id,practice_id,location_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_at,updated_at,source_message_id,message_thread_id) VALUES
  ('`+quietText+`','00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000203','+15555550124','Review inbound text','OPEN','INBOUND_MESSAGE_REVIEW','normal','SERVICE','inbound-message-review',now()-interval '121 hours',now()-interval '121 hours','00000000-0000-0000-0000-000000000231','00000000-0000-0000-0000-000000000221'),
  ('`+recentText+`','00000000-0000-0000-0000-000000000201','00000000-0000-0000-0000-000000000203','+15555550125','Review inbound text','OPEN','INBOUND_MESSAGE_REVIEW','normal','SERVICE','inbound-message-review',now()-interval '119 hours',now()-interval '119 hours','00000000-0000-0000-0000-000000000232','00000000-0000-0000-0000-000000000222');
 INSERT INTO work_task_activities(task_id,task_version,kind,actor_kind,actor_subject,actor_email,occurred_at)
  SELECT id,1,'TASK_CREATED','SERVICE',created_by_subject,NULL,created_at FROM work_tasks;
 INSERT INTO work_task_activities(task_id,task_version,kind,actor_kind,actor_subject,actor_email,occurred_at) VALUES
  ('`+unroutedOpen+`',2,'SOURCE_UPDATED','SERVICE','appointment-review',NULL,'2026-09-02'),
  ('`+unroutedCompleted+`',2,'TASK_COMPLETED','HUMAN','staff','staff@example.test','2026-09-03');
 `); err != nil {
		t.Fatal(err)
	}
	read := func(id string) reviewTaskRow {
		t.Helper()
		var row reviewTaskRow
		if err := pool.QueryRow(ctx, `SELECT state,COALESCE(completed_by_kind,''),COALESCE(completed_by_subject,''),COALESCE(completed_by_email,''),version,completed_at,updated_at,
 (SELECT count(*) FROM work_task_activities WHERE task_id=work_tasks.id) FROM work_tasks WHERE id=$1`, id).
			Scan(&row.State, &row.CompletedKind, &row.CompletedSubject, &row.CompletedEmail, &row.Version, &row.CompletedAt, &row.UpdatedAt, &row.Activities); err != nil {
			t.Fatal(err)
		}
		return row
	}
	lastLive := read(unroutedOpen).UpdatedAt
	untouched := map[string]reviewTaskRow{}
	for _, id := range []string{routedOpen, unroutedCompleted, unroutedFollowUp, recentText} {
		untouched[id] = read(id)
	}
	workspaceVersion := func() (version int64) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT workspace_version FROM access_practices WHERE id='00000000-0000-0000-0000-000000000201'`).Scan(&version); err != nil {
			t.Fatal(err)
		}
		return version
	}
	versionBefore := workspaceVersion()

	before := time.Now().Add(-time.Second)
	if err := migrations.ApplyThrough(ctx, pool, retireHiddenReviewTasks); err != nil {
		t.Fatal(err)
	}

	retired := read(unroutedOpen)
	if retired.State != "COMPLETED" || retired.CompletedKind != "SERVICE" || retired.CompletedSubject != "hidden-review-retirement" ||
		retired.CompletedEmail != "" || retired.Version != 3 || retired.Activities != 3 {
		t.Fatalf("unrouted open review not retired by the service: %+v", retired)
	}
	if retired.CompletedAt == nil || !retired.CompletedAt.Equal(lastLive) || !retired.UpdatedAt.Equal(lastLive) {
		t.Fatalf("retired review would sort as recently completed: completed %v, updated %v, want %v", retired.CompletedAt, retired.UpdatedAt, lastLive)
	}
	var kind, actorKind, actorSubject, reason string
	var actorEmail *string
	var occurredAt, retiredAt time.Time
	if err := pool.QueryRow(ctx, `SELECT kind,actor_kind,actor_subject,actor_email,occurred_at,details->>'reason',(details->>'retiredAt')::timestamptz
 FROM work_task_activities WHERE task_id=$1 AND task_version=3`, unroutedOpen).Scan(&kind, &actorKind, &actorSubject, &actorEmail, &occurredAt, &reason, &retiredAt); err != nil {
		t.Fatal(err)
	}
	if kind != "TASK_COMPLETED" || actorKind != "SERVICE" || actorSubject != "hidden-review-retirement" || actorEmail != nil ||
		!occurredAt.Equal(lastLive) || reason != "LOCATION_HAS_NO_APPOINTMENT_REVIEW_ROUTE" || retiredAt.Before(before) {
		t.Fatalf("retirement activity = %s %s %s %v at %s, reason %q, retired at %s", kind, actorKind, actorSubject, actorEmail, occurredAt, reason, retiredAt)
	}
	quiet := read(quietText)
	var quietReason string
	if err := pool.QueryRow(ctx, `SELECT details->>'reason' FROM work_task_activities WHERE task_id=$1 AND task_version=2`, quietText).Scan(&quietReason); err != nil {
		t.Fatal(err)
	}
	if quiet.State != "COMPLETED" || quiet.CompletedSubject != "hidden-review-retirement" || quiet.Version != 2 || quietReason != "NO_INBOUND_TEXT_FOR_120_HOURS" {
		t.Fatalf("text review with no inbound text for 120 hours not retired: %+v, reason %q", quiet, quietReason)
	}
	for id, want := range untouched {
		if got := read(id); !sameReviewTaskRow(got, want) {
			t.Fatalf("Task %s changed: %+v, want %+v", id, got, want)
		}
	}
	if got := workspaceVersion(); got != versionBefore+1 {
		t.Fatalf("workspace version = %d, want %d", got, versionBefore+1)
	}

	statement, err := os.ReadFile("sql/" + retireHiddenReviewTasks)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, string(statement)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := read(unroutedOpen); !sameReviewTaskRow(got, retired) {
		t.Fatalf("second run changed the retired review: %+v, want %+v", got, retired)
	}
	for id, want := range untouched {
		if got := read(id); !sameReviewTaskRow(got, want) {
			t.Fatalf("second run changed Task %s: %+v, want %+v", id, got, want)
		}
	}
	if got := workspaceVersion(); got != versionBefore+1 {
		t.Fatalf("second run moved the workspace version to %d", got)
	}
}

func sameReviewTaskRow(a, b reviewTaskRow) bool {
	sameCompletion := a.CompletedAt == nil && b.CompletedAt == nil ||
		a.CompletedAt != nil && b.CompletedAt != nil && a.CompletedAt.Equal(*b.CompletedAt)
	return sameCompletion && a.UpdatedAt.Equal(b.UpdatedAt) && a.State == b.State && a.CompletedKind == b.CompletedKind &&
		a.CompletedSubject == b.CompletedSubject && a.CompletedEmail == b.CompletedEmail && a.Version == b.Version && a.Activities == b.Activities
}
