package work

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	TaskOriginAppointmentReview    TaskOrigin = "APPOINTMENT_REVIEW"
	TaskOriginInboundMessageReview TaskOrigin = "INBOUND_MESSAGE_REVIEW"
)

// SourceInteractionID identifies the Interaction that created this appointment review.
func (t Task) SourceInteractionID() string {
	if t.Origin != TaskOriginAppointmentReview {
		return ""
	}
	id, _, _ := strings.Cut(t.SourceReviewKey, ":")
	return id
}

// AppointmentReviewKey identifies a durable outcome at database timestamp precision.
func AppointmentReviewKey(interactionID string, occurredAt time.Time) string {
	return interactionID + ":" + occurredAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
}

// EnsureAppointmentReview preserves one review per durable outcome, including
// after completion. The Interaction owner holds its source lock and transaction.
func (m *Module) EnsureAppointmentReview(ctx context.Context, tx pgx.Tx, interactionID, practiceID, locationID, phone, sourceCallID, action, message string, occurredAt time.Time) error {
	key := AppointmentReviewKey(interactionID, occurredAt)
	title := map[string]string{"BOOKED": "Review booked appointment", "CANCELLED": "Review cancelled appointment", "RESCHEDULED": "Review appointment change"}[action]
	if title == "" || occurredAt.IsZero() {
		return ErrInvalidInput
	}
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO work_tasks(practice_id,location_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_at,updated_at,source_call_id,category,source_review_key,source_message)
 VALUES($1,$2,$3,$4,'OPEN','APPOINTMENT_REVIEW','normal','SERVICE','appointment-review',$5,$5,$6,'appointments',$7,$8)
 ON CONFLICT(source_review_key) WHERE source_review_key IS NOT NULL DO NOTHING RETURNING id::text`, practiceID, locationID, phone, title, occurredAt, sourceCallID, key, message).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return m.recordReviewActivity(ctx, tx, id, practiceID, "TASK_CREATED", "appointment-review", occurredAt, nil)
}

// EnsureInboundMessageReview runs only for a newly inserted non-opt-out Message.
// Messaging holds the thread lock. Locking the current Task also serializes new
// evidence against staff completion; evidence arriving afterward creates new work.
func (m *Module) EnsureInboundMessageReview(ctx context.Context, tx pgx.Tx, practiceID, locationID, phone, threadID, messageID string, occurredAt time.Time) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT id::text FROM work_tasks WHERE message_thread_id=$1 AND origin='INBOUND_MESSAGE_REVIEW' AND state='OPEN' FOR UPDATE`, threadID).Scan(&id)
	kind := "SOURCE_UPDATED"
	if errors.Is(err, pgx.ErrNoRows) {
		kind = "TASK_CREATED"
		err = tx.QueryRow(ctx, `INSERT INTO work_tasks(practice_id,location_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_at,updated_at,source_message_id,message_thread_id)
  VALUES($1,$2,$3,'Review inbound text','OPEN','INBOUND_MESSAGE_REVIEW','normal','SERVICE','inbound-message-review',$4,$4,$5,$6) RETURNING id::text`, practiceID, locationID, phone, occurredAt, messageID, threadID).Scan(&id)
	} else if err == nil {
		_, err = tx.Exec(ctx, `UPDATE work_tasks SET version=version+1,updated_at=GREATEST(updated_at,$2) WHERE id=$1`, id, occurredAt)
	}
	if err != nil {
		return err
	}
	return m.recordReviewActivity(ctx, tx, id, practiceID, kind, "inbound-message-review", occurredAt, map[string]any{"messageId": messageID})
}

func (m *Module) recordReviewActivity(ctx context.Context, tx pgx.Tx, id, practiceID, kind, subject string, at time.Time, details map[string]any) error {
	task, err := loadTask(ctx, tx, id)
	if err != nil {
		return err
	}
	task.UpdatedAt = at
	if err := appendActivityDetails(ctx, tx, task, kind, ActorSnapshot{Kind: "SERVICE", Subject: subject}, details); err != nil {
		return err
	}
	_, err = m.access.RecordWorkspaceChange(ctx, tx, practiceID)
	return err
}
