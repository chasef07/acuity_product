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
	return s.threadAt(s.auth.Practice.ID, s.auth.Locations[0].ID)
}

func (s *expiryScenario) threadAt(practiceID, locationID string) string {
	s.t.Helper()
	s.nextThread++
	var id string
	if err := s.db.Owner.QueryRow(s.ctx, `INSERT INTO messaging_threads(practice_id,location_id,office_phone,external_phone,created_at,updated_at) VALUES($1,$2,'+15555550100',$3,$4,$4) RETURNING id::text`,
		practiceID, locationID, s.phone(s.nextThread), s.now).Scan(&id); err != nil {
		s.t.Fatalf("insert thread: %v", err)
	}
	return id
}

func (s *expiryScenario) secondPractice() (string, string) {
	s.t.Helper()
	var practiceID, locationID string
	if err := s.db.Owner.QueryRow(s.ctx, `
		WITH practice AS (
			INSERT INTO access_practices(provisioning_key,name) VALUES('second-synthetic-practice','Second Synthetic Practice') RETURNING id
		)
		INSERT INTO access_locations(practice_id,provisioning_key,name)
		SELECT id,'second-synthetic-location','Second Synthetic Location' FROM practice
		RETURNING practice_id::text,id::text`).Scan(&practiceID, &locationID); err != nil {
		s.t.Fatalf("insert second practice: %v", err)
	}
	return practiceID, locationID
}

func (s *expiryScenario) staffReply(thread string) string {
	s.t.Helper()
	tx, err := s.db.Owner.Begin(s.ctx)
	if err != nil {
		s.t.Fatalf("begin staff reply: %v", err)
	}
	defer func() { _ = tx.Rollback(s.ctx) }()
	id := uuid.NewString()
	if _, err := tx.Exec(s.ctx, `INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,created_by_kind,created_by_subject,created_at,updated_at)
		SELECT $1,id,practice_id,location_id,'OUTBOUND','Synthetic answer',office_phone,external_phone,'SENDING','HUMAN',$2,$3,$3 FROM messaging_threads WHERE id=$4`,
		id, s.identity.Subject, s.now, thread); err != nil {
		s.t.Fatalf("insert staff reply: %v", err)
	}
	if err := s.staff.CaptureTextReply(s.ctx, tx, thread, id, access.Actor{Subject: s.identity.Subject, Email: s.identity.Email}); err != nil {
		s.t.Fatalf("capture staff reply: %v", err)
	}
	if err := tx.Commit(s.ctx); err != nil {
		s.t.Fatalf("commit staff reply: %v", err)
	}
	return id
}

func (s *expiryScenario) deliveryReceipt(messageID, state string) {
	s.t.Helper()
	tx, err := s.db.Owner.Begin(s.ctx)
	if err != nil {
		s.t.Fatalf("begin delivery receipt: %v", err)
	}
	defer func() { _ = tx.Rollback(s.ctx) }()
	if _, err := tx.Exec(s.ctx, `UPDATE messaging_messages SET delivery_state=$2 WHERE id=$1`, messageID, state); err != nil {
		s.t.Fatalf("project delivery state: %v", err)
	}
	if err := s.staff.ApplyTextReply(s.ctx, tx, messageID); err != nil {
		s.t.Fatalf("apply delivery receipt: %v", err)
	}
	if err := tx.Commit(s.ctx); err != nil {
		s.t.Fatalf("commit delivery receipt: %v", err)
	}
}

func (s *expiryScenario) drain(want int) {
	s.t.Helper()
	closed := 0
	for {
		batch, err := s.sweep.ExpireTextReviews(s.ctx)
		if err != nil {
			s.t.Fatalf("ExpireTextReviews after %d closed: %v", closed, err)
		}
		if batch > 500 {
			s.t.Fatalf("one sweep closed %d text reviews, want at most 500 per call", batch)
		}
		closed += batch
		if batch == 0 {
			break
		}
	}
	if closed != want {
		s.t.Fatalf("sweeps closed %d text reviews before running dry, want %d", closed, want)
	}
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
	var phone, practiceID, locationID string
	if err := tx.QueryRow(s.ctx, `SELECT external_phone,practice_id::text,location_id::text FROM messaging_threads WHERE id=$1`, thread).Scan(&phone, &practiceID, &locationID); err != nil {
		s.t.Fatalf("read thread: %v", err)
	}
	if _, err := tx.Exec(s.ctx, `INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,created_at,updated_at) VALUES($1,$2,$3,$4,'INBOUND','Synthetic question',$5,'+15555550100','DELIVERED',$6,$6)`,
		id, thread, practiceID, locationID, phone, at); err != nil {
		s.t.Fatalf("insert inbound text: %v", err)
	}
	if err := s.staff.EnsureInboundMessageReview(s.ctx, tx, practiceID, locationID, phone, thread, id, at); err != nil {
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
	if kind != "TASK_COMPLETED" || actorKind != "SERVICE" || actorSubject != "text-review-expiry" ||
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

func TestExpireTextReviewsClosesOneBatchPerCallUntilABacklogDrains(t *testing.T) {
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

	s.expire(500)
	if open := s.count(`SELECT count(*) FROM work_tasks WHERE state='OPEN'`); open != backlog-500+1 {
		t.Fatalf("open Tasks after one sweep = %d, want one batch of 500 closed and %d left", open, backlog-500+1)
	}
	started := time.Now()
	s.drain(backlog - 500)
	t.Logf("repeated sweeps closed %d text reviews in %s", backlog-500, time.Since(started))

	if open := s.count(`SELECT count(*) FROM work_tasks WHERE state='OPEN'`); open != 1 {
		t.Fatalf("open Tasks after the backlog sweep = %d, want only the fresh review", open)
	}
	if expired := s.count(`SELECT count(*) FROM work_task_activities WHERE kind='TASK_COMPLETED' AND details->>'closeReason'='EXPIRED'`); expired != backlog {
		t.Fatalf("expiry activities = %d, want %d", expired, backlog)
	}
	s.expire(0)
}

func TestAStaffReplyInFlightKeepsAReviewOpenUntilItsReceipt(t *testing.T) {
	s := newExpiryScenario(t)
	delivered, failed := s.thread(), s.thread()
	s.inboundText(delivered, 119*time.Hour)
	s.inboundText(failed, 119*time.Hour)
	deliveredReply, failedReply := s.staffReply(delivered), s.staffReply(failed)

	s.now = s.now.Add(2 * time.Hour)
	s.expire(0)

	s.deliveryReceipt(deliveredReply, "DELIVERED")
	if review := s.onlyReview(delivered); review.state != "COMPLETED" || review.reason != string(work.CloseReasonDone) || review.actorKind != "HUMAN" {
		t.Fatalf("review whose reply was delivered = %#v, want it closed as DONE by the staff member", review)
	}
	s.deliveryReceipt(failedReply, "FAILED")
	if review := s.onlyReview(failed); review.state != "OPEN" {
		t.Fatalf("review whose reply failed = %#v, want it still open", review)
	}
	s.expire(0)
}

func TestAFailedDeliveryReopenRestartsThe120Hours(t *testing.T) {
	s := newExpiryScenario(t)
	thread := s.thread()
	s.inboundText(thread, 100*time.Hour)
	reply := s.staffReply(thread)
	s.deliveryReceipt(reply, "SENT")
	if review := s.onlyReview(thread); review.reason != string(work.CloseReasonDone) {
		t.Fatalf("review after its reply was sent = %#v, want it closed as DONE", review)
	}

	s.now = s.now.Add(30 * time.Hour)
	s.deliveryReceipt(reply, "FAILED")
	if review := s.onlyReview(thread); review.state != "OPEN" {
		t.Fatalf("review after its reply failed = %#v, want it reopened", review)
	}
	s.expire(0)
	s.now = s.now.Add(119 * time.Hour)
	s.expire(0)
	s.now = s.now.Add(2 * time.Hour)
	s.expire(1)
}

func TestExpireTextReviewsSweepsEveryPractice(t *testing.T) {
	s := newExpiryScenario(t)
	first := s.thread()
	secondPractice, secondLocation := s.secondPractice()
	second := s.threadAt(secondPractice, secondLocation)
	s.inboundText(first, 121*time.Hour)
	s.inboundText(second, 121*time.Hour)
	secondVersion := s.count(`SELECT workspace_version FROM access_practices WHERE id=$1`, secondPractice)
	firstVersion := s.workspaceVersion()

	s.expire(2)

	for _, thread := range []string{first, second} {
		if review := s.onlyReview(thread); review.reason != string(work.CloseReasonExpired) {
			t.Fatalf("review = %#v, want it expired in both practices", review)
		}
	}
	if s.workspaceVersion() <= firstVersion || s.count(`SELECT workspace_version FROM access_practices WHERE id=$1`, secondPractice) <= secondVersion {
		t.Fatalf("workspace versions did not advance in both practices")
	}
}

func TestExpireTextReviewsDoesNotWaitForAReviewAnotherWriterHolds(t *testing.T) {
	s := newExpiryScenario(t)
	held, free := s.thread(), s.thread()
	s.inboundText(held, 121*time.Hour)
	s.inboundText(free, 121*time.Hour)
	tx, err := s.db.Owner.Begin(s.ctx)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	defer func() { _ = tx.Rollback(s.ctx) }()
	if _, err := tx.Exec(s.ctx, `SELECT id FROM work_tasks WHERE id=$1 FOR UPDATE`, s.onlyReview(held).id); err != nil {
		t.Fatalf("hold review: %v", err)
	}

	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	closed, err := s.sweep.ExpireTextReviews(ctx)
	if err != nil || closed != 1 {
		t.Fatalf("ExpireTextReviews beside a held review = %d, %v; want the free review closed without waiting", closed, err)
	}
	if err := tx.Rollback(s.ctx); err != nil {
		t.Fatalf("release review: %v", err)
	}
	s.expire(1)
}

func TestExpireTextReviewsHoldsNoReviewWhileItWaitsForThePractice(t *testing.T) {
	s := newExpiryScenario(t)
	thread := s.thread()
	s.inboundText(thread, 121*time.Hour)
	review := s.onlyReview(thread)
	receipt, err := s.db.Owner.Begin(s.ctx)
	if err != nil {
		t.Fatalf("begin receipt: %v", err)
	}
	defer func() { _ = receipt.Rollback(s.ctx) }()
	if _, err := receipt.Exec(s.ctx, `UPDATE access_practices SET workspace_version=workspace_version+1 WHERE id=$1`, s.auth.Practice.ID); err != nil {
		t.Fatalf("receipt locks the practice: %v", err)
	}
	swept := make(chan error, 1)
	go func() {
		_, err := s.sweep.ExpireTextReviews(s.ctx)
		swept <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for s.count(`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%UPDATE access_practices%SET workspace_version%'`) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the sweep never waited for the practice the receipt holds")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := receipt.Exec(s.ctx, `SET LOCAL lock_timeout='2s'`); err != nil {
		t.Fatalf("bound the receipt's wait: %v", err)
	}
	if _, err := receipt.Exec(s.ctx, `SELECT id FROM work_tasks WHERE id=$1 FOR UPDATE`, review.id); err != nil {
		t.Fatalf("receipt holding the practice could not lock the review while the sweep waited: %v", err)
	}
	if err := receipt.Commit(s.ctx); err != nil {
		t.Fatalf("commit receipt: %v", err)
	}
	if err := <-swept; err != nil {
		t.Fatalf("sweep after the receipt committed: %v", err)
	}
	if after := s.onlyReview(thread); after.reason != string(work.CloseReasonExpired) {
		t.Fatalf("review after the receipt released it = %#v, want the sweep to have expired it", after)
	}
}
