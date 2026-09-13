package work

import (
	"context"
	"errors"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

// CaptureTextReply records exactly which open review a human reply addresses.
// Messaging holds the thread lock, so inbound projection cannot cross this snapshot.
func (m *Module) CaptureTextReply(ctx context.Context, tx pgx.Tx, threadID, messageID string, actor access.Actor) error {
	_, err := tx.Exec(ctx, `INSERT INTO work_text_replies(message_id,task_id,task_version,actor_subject,actor_email)
 SELECT $1,id,version,$3,$4 FROM work_tasks
 WHERE message_thread_id=$2 AND origin='INBOUND_MESSAGE_REVIEW' AND state='OPEN'`, messageID, threadID, actor.Subject, actor.Email)
	return err
}

// ApplyTextReply follows committed provider evidence, never the send click.
func (m *Module) ApplyTextReply(ctx context.Context, tx pgx.Tx, messageID string) error {
	var delivery string
	var taskID, subject, email string
	var version int64
	var completedVersion *int64
	err := tx.QueryRow(ctx, `SELECT reply.task_id::text,reply.task_version,reply.actor_subject,reply.actor_email,reply.completed_version,message.delivery_state FROM work_text_replies reply JOIN messaging_messages message ON message.id=reply.message_id WHERE reply.message_id=$1 FOR UPDATE OF reply`, messageID).Scan(&taskID, &version, &subject, &email, &completedVersion, &delivery)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if delivery != "SENT" && delivery != "DELIVERED" && delivery != "FAILED" {
		return nil
	}
	// Take the same thread lock as inbound projection before locking its review.
	if _, err = tx.Exec(ctx, `SELECT id FROM messaging_threads WHERE id=(SELECT message_thread_id FROM work_tasks WHERE id=$1) FOR UPDATE`, taskID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM work_tasks WHERE id=$1 FOR UPDATE`, taskID); err != nil {
		return err
	}
	task, err := loadTask(ctx, tx, taskID)
	if err != nil {
		return err
	}
	actor := ActorSnapshot{Kind: "HUMAN", Subject: subject, Email: email}
	now := m.now()
	kind := "TASK_COMPLETED"
	if delivery == "FAILED" {
		if completedVersion == nil || task.State != TaskCompleted || task.Version != *completedVersion {
			return nil
		}
		var otherReplySucceeded bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM work_text_replies reply JOIN messaging_messages message ON message.id=reply.message_id WHERE reply.task_id=$1 AND reply.task_version=$2 AND reply.message_id<>$3 AND message.delivery_state IN ('SENT','DELIVERED'))`, taskID, version, messageID).Scan(&otherReplySucceeded); err != nil {
			return err
		}
		if otherReplySucceeded {
			return nil
		}
		var newer bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM work_tasks WHERE message_thread_id=(SELECT message_thread_id FROM work_tasks WHERE id=$1) AND origin='INBOUND_MESSAGE_REVIEW' AND state='OPEN')`, taskID).Scan(&newer); err != nil {
			return err
		}
		if newer {
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE work_tasks SET state='OPEN',completed_at=NULL,completed_by_kind=NULL,completed_by_subject=NULL,completed_by_email=NULL,version=version+1,updated_at=$2 WHERE id=$1`, taskID, now)
		kind = "TASK_REOPENED"
		actor = ActorSnapshot{Kind: "SERVICE", Subject: "text-delivery"}
	} else {
		// A second reply can also support the same completion. Preserve that
		// evidence so one failed delivery cannot undo another successful reply.
		if completedVersion == nil && task.State == TaskCompleted && task.Version == version+1 {
			_, err = tx.Exec(ctx, `UPDATE work_text_replies SET completed_version=$2 WHERE message_id=$1 AND EXISTS(SELECT 1 FROM work_text_replies prior WHERE prior.task_id=$3 AND prior.completed_version=$2)`, messageID, task.Version, taskID)
			return err
		}
		if completedVersion != nil || task.State != TaskOpen || task.Version != version {
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE work_tasks SET state='COMPLETED',completed_at=$4,completed_by_kind='HUMAN',completed_by_subject=$2,completed_by_email=$3,version=version+1,updated_at=$4 WHERE id=$1`, taskID, subject, email, now)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE work_text_replies SET completed_version=$2 WHERE message_id=$1`, messageID, version+1)
		}
	}
	if err != nil {
		return err
	}
	task, err = loadTask(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if err = appendActivityDetails(ctx, tx, task, kind, actor, map[string]any{"messageId": messageID, "deliveryState": delivery}); err != nil {
		return err
	}
	_, err = m.access.RecordWorkspaceChange(ctx, tx, task.PracticeID)
	return err
}
