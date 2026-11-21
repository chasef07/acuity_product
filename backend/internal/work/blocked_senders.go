package work

import (
	"context"
	"errors"
	"fmt"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

func (m *Module) CloseTextReviewsForBlockedSender(ctx context.Context, tx pgx.Tx, identity access.Identity, practiceID, phone string) error {
	rows, err := tx.Query(ctx, `
		SELECT id::text FROM work_tasks
		WHERE practice_id = $1 AND phone = $2
			AND origin = 'INBOUND_MESSAGE_REVIEW' AND state = 'OPEN'
	`, practiceID, phone)
	if err != nil {
		return fmt.Errorf("find blocked sender's text reviews: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("find blocked sender's text reviews: %w", err)
	}
	authorizations := make(map[string]access.Authorization, len(ids))
	closing := make([]string, 0, len(ids))
	for _, id := range ids {
		task, err := loadTask(ctx, tx, id, false)
		if err != nil {
			return err
		}
		authorization, err := m.authorizeMutation(ctx, tx, identity, task)
		if errors.Is(err, ErrDenied) {
			continue
		}
		if err != nil {
			return err
		}
		authorizations[id] = authorization
		closing = append(closing, id)
	}
	if err := m.LockTasks(ctx, tx, closing); err != nil {
		return err
	}
	for _, id := range closing {
		task, err := loadTask(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if task.State != TaskOpen {
			continue
		}
		if _, err := m.completeLockedTask(ctx, tx, authorizations[id], task, CloseReasonNotNeeded, ""); err != nil {
			return err
		}
	}
	return nil
}
