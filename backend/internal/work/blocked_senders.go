package work

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (m *Module) CloseTextReviewsForBlockedSender(ctx context.Context, tx pgx.Tx, practiceID, phone string, actor ActorSnapshot) error {
	now := m.now()
	rows, err := tx.Query(ctx, `
		UPDATE work_tasks
		SET state = 'COMPLETED',
			completed_by_kind = $3,
			completed_by_subject = $4,
			completed_by_email = NULLIF($5, ''),
			completed_at = $6,
			close_reason = $7,
			closed_via_call_id = NULL,
			version = version + 1,
			updated_at = $6
		WHERE practice_id = $1 AND phone = $2
			AND origin = 'INBOUND_MESSAGE_REVIEW' AND state = 'OPEN'
		RETURNING id::text
	`, practiceID, phone, actor.Kind, actor.Subject, actor.Email, now, CloseReasonNotNeeded)
	if err != nil {
		return fmt.Errorf("close blocked sender's text reviews: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("close blocked sender's text reviews: %w", err)
	}
	for _, id := range ids {
		task, err := loadTask(ctx, tx, id, false)
		if err != nil {
			return err
		}
		if err := appendActivity(ctx, tx, task, "TASK_COMPLETED", actor, now, map[string]any{
			"closeReason": CloseReasonNotNeeded, "blockedSender": true,
		}); err != nil {
			return err
		}
	}
	return nil
}
