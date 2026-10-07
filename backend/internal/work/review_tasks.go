package work

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	TaskOriginAppointmentReview    TaskOrigin = "APPOINTMENT_REVIEW"
	TaskOriginInboundMessageReview TaskOrigin = "INBOUND_MESSAGE_REVIEW"

	appointmentReviewOfficeKey = "spring-hill"
)

func (m *Module) EnsureAppointmentReview(ctx context.Context, tx pgx.Tx, interactionID, practiceID, locationID, phone, sourceCallID, title, message string, occurredAt time.Time) error {
	title = strings.TrimSpace(title)
	message = strings.TrimSpace(message)
	if tx == nil || m.access == nil || strings.TrimSpace(interactionID) == "" || strings.TrimSpace(sourceCallID) == "" ||
		occurredAt.IsZero() || !textLengthBetween(title, 1, 500) || !textLengthBetween(message, 1, 2500) {
		return ErrInvalidInput
	}
	routed, err := m.access.LocationHasAbitaOfficeRoute(ctx, tx, practiceID, locationID, appointmentReviewOfficeKey)
	if err != nil || !routed {
		return err
	}
	var id, state, currentTitle, currentMessage string
	err = tx.QueryRow(ctx, `SELECT id::text,state,title,COALESCE(source_message,'') FROM work_tasks
 WHERE practice_id=$1 AND source_call_id=$2 AND origin='APPOINTMENT_REVIEW'
 ORDER BY (state='OPEN') DESC,created_at DESC,id DESC LIMIT 1 FOR UPDATE`, practiceID, sourceCallID).Scan(&id, &state, &currentTitle, &currentMessage)
	switch {
	case err == nil && currentMessage == message:
		return nil
	case err == nil && TaskState(state) == TaskOpen:
		if _, err := tx.Exec(ctx, `UPDATE work_tasks SET title=$2,source_message=$3,version=version+1,updated_at=GREATEST(updated_at,$4) WHERE id=$1`, id, title, message, occurredAt); err != nil {
			return err
		}
		return m.recordReviewActivity(ctx, tx, id, practiceID, "SOURCE_UPDATED", "appointment-review", occurredAt, map[string]any{"previousTitle": currentTitle, "previousMessage": currentMessage})
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	key := fmt.Sprintf("%s:%s:%x", interactionID, occurredAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano), sha256.Sum256([]byte(message)))
	err = tx.QueryRow(ctx, `INSERT INTO work_tasks(practice_id,location_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_at,updated_at,source_call_id,category,source_review_key,source_message)
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
	task, err := loadTask(ctx, tx, id, false)
	if err != nil {
		return err
	}
	if err := appendActivity(ctx, tx, task, kind, ActorSnapshot{Kind: "SERVICE", Subject: subject}, at, details); err != nil {
		return err
	}
	_, err = m.access.RecordWorkspaceChange(ctx, tx, practiceID, id)
	return err
}
