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

type expiryScenario struct {
	t          *testing.T
	ctx        context.Context
	db         *testdb.DB
	now        time.Time
	auth       access.Authorization
	identity   access.Identity
	staff      *work.Module
	sweep      *work.Module
	nextThread int
}

func newExpiryScenario(t *testing.T) *expiryScenario {
	t.Helper()
	s := &expiryScenario{t: t, ctx: context.Background(), db: testdb.Open(t), now: time.Date(2026, time.November, 10, 15, 0, 0, 0, time.UTC)}
	clock := func() time.Time { return s.now }
	portalAccess := access.New(s.db.As(testdb.Portal), clock)
	s.auth, s.identity = provisionStaff(t, s.db.Owner, portalAccess, s.now)
	s.staff = work.New(s.db.As(testdb.Portal), portalAccess, clock)
	s.sweep = work.New(s.db.As(testdb.Worker), access.New(s.db.As(testdb.Worker), clock), clock)
	return s
}

func (s *expiryScenario) thread() string {
	s.t.Helper()
	s.nextThread++
	var id string
	if err := s.db.Owner.QueryRow(s.ctx, `INSERT INTO messaging_threads(practice_id,location_id,office_phone,external_phone,created_at,updated_at) VALUES($1,$2,'+15555550100',$3,$4,$4) RETURNING id::text`,
		s.auth.Practice.ID, s.auth.Locations[0].ID, s.phone(s.nextThread), s.now).Scan(&id); err != nil {
		s.t.Fatalf("insert thread: %v", err)
	}
	return id
}

func (s *expiryScenario) phone(thread int) string {
	return "+1555555" + string(rune('0'+thread/1000%10)) + string(rune('0'+thread/100%10)) + string(rune('0'+thread/10%10)) + string(rune('0'+thread%10))
}

func (s *expiryScenario) inboundText(thread string, age time.Duration) {
	s.t.Helper()
	at := s.now.Add(-age)
	tx, err := s.db.As(testdb.Worker).Begin(s.ctx)
	if err != nil {
		s.t.Fatalf("begin inbound text: %v", err)
	}
	defer func() { _ = tx.Rollback(s.ctx) }()
	id := uuid.NewString()
	var phone string
	if err := tx.QueryRow(s.ctx, `SELECT external_phone FROM messaging_threads WHERE id=$1`, thread).Scan(&phone); err != nil {
		s.t.Fatalf("read thread phone: %v", err)
	}
	if _, err := tx.Exec(s.ctx, `INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,created_at,updated_at) VALUES($1,$2,$3,$4,'INBOUND','Synthetic question',$5,'+15555550100','DELIVERED',$6,$6)`,
		id, thread, s.auth.Practice.ID, s.auth.Locations[0].ID, phone, at); err != nil {
		s.t.Fatalf("insert inbound text: %v", err)
	}
	if err := s.staff.EnsureInboundMessageReview(s.ctx, tx, s.auth.Practice.ID, s.auth.Locations[0].ID, phone, thread, id, at); err != nil {
		s.t.Fatalf("ensure inbound text review: %v", err)
	}
	if err := tx.Commit(s.ctx); err != nil {
		s.t.Fatalf("commit inbound text: %v", err)
	}
}

type reviewRow struct {
	id, state, reason, actorKind, actorSubject string
	version                                    int64
	completedAt                                *time.Time
}

func (s *expiryScenario) reviews(thread string) []reviewRow {
	s.t.Helper()
	rows, err := s.db.Owner.Query(s.ctx, `SELECT id::text,state,COALESCE(close_reason,''),COALESCE(completed_by_kind,''),COALESCE(completed_by_subject,''),version,completed_at
		FROM work_tasks WHERE message_thread_id=$1 AND origin='INBOUND_MESSAGE_REVIEW' ORDER BY created_at,id`, thread)
	if err != nil {
		s.t.Fatalf("read reviews: %v", err)
	}
	defer rows.Close()
	var reviews []reviewRow
	for rows.Next() {
		var review reviewRow
		if err := rows.Scan(&review.id, &review.state, &review.reason, &review.actorKind, &review.actorSubject, &review.version, &review.completedAt); err != nil {
			s.t.Fatalf("scan review: %v", err)
		}
		reviews = append(reviews, review)
	}
	if err := rows.Err(); err != nil {
		s.t.Fatalf("read reviews: %v", err)
	}
	return reviews
}

func (s *expiryScenario) onlyReview(thread string) reviewRow {
	s.t.Helper()
	reviews := s.reviews(thread)
	if len(reviews) != 1 {
		s.t.Fatalf("reviews on thread = %#v, want exactly one", reviews)
	}
	return reviews[0]
}

func (s *expiryScenario) expire(want int) {
	s.t.Helper()
	closed, err := s.sweep.ExpireTextReviews(s.ctx)
	if err != nil || closed != want {
		s.t.Fatalf("ExpireTextReviews = %d, %v; want %d closed", closed, err, want)
	}
}

func (s *expiryScenario) count(query string, arguments ...any) int {
	s.t.Helper()
	var n int
	if err := s.db.Owner.QueryRow(s.ctx, query, arguments...).Scan(&n); err != nil {
		s.t.Fatalf("count: %v", err)
	}
	return n
}

func (s *expiryScenario) workspaceVersion() int {
	return s.count(`SELECT workspace_version FROM access_practices WHERE id=$1`, s.auth.Practice.ID)
}

func TestExpireTextReviewsClosesAReviewOnceItsNewestTextIsOlderThan120Hours(t *testing.T) {
	s := newExpiryScenario(t)
	fresh, stale, answered := s.thread(), s.thread(), s.thread()
	s.inboundText(fresh, 119*time.Hour)
	s.inboundText(stale, 121*time.Hour)
	s.inboundText(answered, 130*time.Hour)
	s.inboundText(answered, time.Hour)
	versionBefore := s.workspaceVersion()

	s.expire(1)

	if review := s.onlyReview(fresh); review.state != "OPEN" {
		t.Fatalf("review 119 hours after its text = %#v, want it open", review)
	}
	if review := s.onlyReview(answered); review.state != "OPEN" {
		t.Fatalf("review whose patient texted again an hour ago = %#v, want it open", review)
	}
	expired := s.onlyReview(stale)
	if expired.state != "COMPLETED" || expired.reason != string(work.CloseReasonExpired) ||
		expired.actorKind != "SERVICE" || expired.actorSubject != "text-review-expiry" ||
		expired.completedAt == nil || !expired.completedAt.Equal(s.now) {
		t.Fatalf("review 121 hours after its text = %#v, want it closed as EXPIRED by the expiry service at %s", expired, s.now)
	}
	read, err := s.staff.ReadTask(s.ctx, s.identity, expired.id)
	if err != nil || read.State != work.TaskCompleted || read.CloseReason != work.CloseReasonExpired ||
		read.CompletedBy == nil || read.CompletedBy.Kind != "SERVICE" {
		t.Fatalf("expired review read by staff = %#v, %v", read, err)
	}
	var kind, actorKind, actorSubject, closeReason string
	var activityVersion int64
	var occurredAt time.Time
	if err := s.db.Owner.QueryRow(s.ctx, `SELECT kind,actor_kind,actor_subject,task_version,occurred_at,details->>'closeReason'
		FROM work_task_activities WHERE task_id=$1 ORDER BY task_version DESC LIMIT 1`, expired.id).
		Scan(&kind, &actorKind, &actorSubject, &activityVersion, &occurredAt, &closeReason); err != nil {
		t.Fatalf("read expiry activity: %v", err)
	}
	if kind != "TASK_EXPIRED" || actorKind != "SERVICE" || actorSubject != "text-review-expiry" ||
		activityVersion != expired.version || !occurredAt.Equal(s.now) || closeReason != string(work.CloseReasonExpired) {
		t.Fatalf("expiry activity = %s by %s/%s at version %d, %s, reason %s; review version %d",
			kind, actorKind, actorSubject, activityVersion, occurredAt, closeReason, expired.version)
	}
	if after := s.workspaceVersion(); after <= versionBefore {
		t.Fatalf("workspace version after expiry = %d, was %d; staff screens would not refresh", after, versionBefore)
	}
}

func TestExpireTextReviewsIsIdempotent(t *testing.T) {
	s := newExpiryScenario(t)
	s.inboundText(s.thread(), 121*time.Hour)
	s.inboundText(s.thread(), 500*time.Hour)
	s.expire(2)
	activities := s.count(`SELECT count(*) FROM work_task_activities`)
	versions := s.count(`SELECT sum(version) FROM work_tasks`)
	workspace := s.workspaceVersion()

	s.expire(0)

	if got := s.count(`SELECT count(*) FROM work_task_activities`); got != activities {
		t.Fatalf("activities after a second sweep = %d, want %d", got, activities)
	}
	if got := s.count(`SELECT sum(version) FROM work_tasks`); got != versions {
		t.Fatalf("Task versions after a second sweep sum to %d, want %d", got, versions)
	}
	if got := s.workspaceVersion(); got != workspace {
		t.Fatalf("workspace version after a second sweep = %d, want %d", got, workspace)
	}
}

func TestExpireTextReviewsLeavesAReviewStaffAlreadyClosed(t *testing.T) {
	s := newExpiryScenario(t)
	thread := s.thread()
	s.inboundText(thread, 121*time.Hour)
	open := s.onlyReview(thread)
	if _, err := s.staff.CompleteTask(s.ctx, work.CompleteTaskCommand{Identity: s.identity, TaskID: open.id, ExpectedVersion: open.version, Reason: work.CloseReasonDone}); err != nil {
		t.Fatalf("complete review as staff: %v", err)
	}
	closed := s.onlyReview(thread)

	s.expire(0)

	after := s.onlyReview(thread)
	if after.reason != string(work.CloseReasonDone) || after.actorKind != "HUMAN" || after.version != closed.version {
		t.Fatalf("review staff closed as DONE = %#v after the sweep, want %#v", after, closed)
	}
}

func TestExpireTextReviewsLeavesOtherKindsOfTask(t *testing.T) {
	s := newExpiryScenario(t)
	phone := "+15555550177"
	tx, err := s.db.As(testdb.Worker).Begin(s.ctx)
	if err != nil {
		t.Fatalf("begin recovery transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(s.ctx) }()
	callID := insertCallAt(t, s.db.Owner, s.auth, s.auth.Locations[0].ID, phone, "Missed", s.now)
	if _, err := s.staff.EnsureRecoveryTask(s.ctx, tx, work.EnsureRecoveryTaskCommand{
		CallID: callID, PracticeID: s.auth.Practice.ID, LocationID: s.auth.Locations[0].ID,
		Phone: phone, Outcome: work.RecoveryOutcomeMissedCall, OccurredAt: s.now,
	}); err != nil {
		t.Fatalf("ensure recovery Task: %v", err)
	}
	if err := tx.Commit(s.ctx); err != nil {
		t.Fatalf("commit recovery Task: %v", err)
	}
	if _, _, err := s.staff.CreateAITask(s.ctx, work.CreateAITaskCommand{
		Service: access.ServiceIdentity{
			Subject: "abita-synthetic", PracticeID: s.auth.Practice.ID,
			LocationScope: access.LocationScopeAll,
			Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
		},
		OfficeKey: "spring-hill", OfficePhone: "+17275919997",
		SourceCallID: "synthetic-ai-call", IdempotencyKey: "task-synthetic-ai-call",
		Phone: "+15555550178", Summary: "Caller needs a refill.",
		Message:  "Caller asked the office to refill eye drops.",
		Category: work.TaskCategoryMedication, Urgency: work.TaskUrgencyNormal,
	}); err != nil {
		t.Fatalf("create AI Task: %v", err)
	}
	createTask(t, s.db, s.staff, s.auth, s.auth.Locations[0].ID, "+15555550179", "Confirm surgery instructions", s.now)
	if _, err := s.db.Owner.Exec(s.ctx, `UPDATE work_tasks SET updated_at=$1`, s.now.Add(-500*time.Hour)); err != nil {
		t.Fatalf("age Tasks: %v", err)
	}
	if origins := s.count(`SELECT count(DISTINCT origin) FROM work_tasks WHERE state='OPEN' AND origin IN ('MISSED_CALL_RECOVERY','ABITA_AI','HUMAN_CALL_FOLLOW_UP')`); origins != 3 {
		t.Fatalf("open missed-call, AI and follow-up Tasks cover %d origins, want 3", origins)
	}

	s.expire(0)

	if open := s.count(`SELECT count(*) FROM work_tasks WHERE state='OPEN'`); open != 3 {
		t.Fatalf("open Tasks after the sweep = %d, want all 3 left open", open)
	}
}

func TestANewTextAfterExpiryOpensANewReview(t *testing.T) {
	s := newExpiryScenario(t)
	thread := s.thread()
	s.inboundText(thread, 121*time.Hour)
	s.expire(1)
	expired := s.onlyReview(thread)

	s.inboundText(thread, 0)

	reviews := s.reviews(thread)
	if len(reviews) != 2 || reviews[0].id != expired.id || reviews[0].version != expired.version ||
		reviews[0].reason != string(work.CloseReasonExpired) || reviews[1].state != "OPEN" {
		t.Fatalf("reviews after the patient texted again = %#v, want the expired one unchanged and a new open one", reviews)
	}
	s.expire(0)
}

func TestAReviewStaffReopenAfterExpiryStaysOpenForAnother120Hours(t *testing.T) {
	s := newExpiryScenario(t)
	thread := s.thread()
	s.inboundText(thread, 121*time.Hour)
	s.expire(1)
	expired := s.onlyReview(thread)
	if _, err := s.staff.ReopenTask(s.ctx, work.ReopenTaskCommand{Identity: s.identity, TaskID: expired.id, ExpectedVersion: expired.version}); err != nil {
		t.Fatalf("reopen expired review: %v", err)
	}

	s.expire(0)
	if review := s.onlyReview(thread); review.state != "OPEN" || review.reason != "" {
		t.Fatalf("reopened review = %#v, want it open with no close reason", review)
	}
	s.now = s.now.Add(119 * time.Hour)
	s.expire(0)
	s.now = s.now.Add(2 * time.Hour)
	s.expire(1)
	if review := s.onlyReview(thread); review.state != "COMPLETED" || review.reason != string(work.CloseReasonExpired) {
		t.Fatalf("reopened review 121 hours later = %#v, want it expired again", review)
	}
}

func TestExpireTextReviewsClosesABacklogLargerThanOneBatch(t *testing.T) {
	s := newExpiryScenario(t)
	const backlog = 5000
	if _, err := s.db.Owner.Exec(s.ctx, `
		WITH threads AS (
			INSERT INTO messaging_threads(practice_id,location_id,office_phone,external_phone,created_at,updated_at)
			SELECT $1,$2,'+15555550100','+1555' || lpad(n::text,7,'0'),$3,$3 FROM generate_series(1,$4::int) n
			RETURNING id, external_phone
		), messages AS (
			INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,created_at,updated_at)
			SELECT gen_random_uuid(),id,$1,$2,'INBOUND','Synthetic question',external_phone,'+15555550100','DELIVERED',$3,$3 FROM threads
			RETURNING id, thread_id, sender
		), tasks AS (
			INSERT INTO work_tasks(practice_id,location_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_at,updated_at,source_message_id,message_thread_id)
			SELECT $1,$2,sender,'Review inbound text','OPEN','INBOUND_MESSAGE_REVIEW','normal','SERVICE','inbound-message-review',$3,$3,id,thread_id FROM messages
			RETURNING id
		)
		INSERT INTO work_task_activities(task_id,task_version,kind,actor_kind,actor_subject,occurred_at)
		SELECT id,1,'TASK_CREATED','SERVICE','inbound-message-review',$3 FROM tasks`,
		s.auth.Practice.ID, s.auth.Locations[0].ID, s.now.Add(-121*time.Hour), backlog); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}
	fresh := s.thread()
	s.inboundText(fresh, time.Hour)

	started := time.Now()
	s.expire(backlog)
	t.Logf("one sweep closed %d text reviews in %s", backlog, time.Since(started))

	if open := s.count(`SELECT count(*) FROM work_tasks WHERE state='OPEN'`); open != 1 {
		t.Fatalf("open Tasks after the backlog sweep = %d, want only the fresh review", open)
	}
	if expired := s.count(`SELECT count(*) FROM work_task_activities WHERE kind='TASK_EXPIRED'`); expired != backlog {
		t.Fatalf("expiry activities = %d, want %d", expired, backlog)
	}
	s.expire(0)
}
