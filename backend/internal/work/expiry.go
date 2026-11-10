package work

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	textReviewLifetime    = 120 * time.Hour
	textReviewExpiryBatch = 500
	textReviewExpiryActor = "text-review-expiry"
)

func (m *Module) ExpireTextReviews(ctx context.Context) (int, error) {
	if m.access == nil {
		return 0, ErrInvalidInput
	}
	closed := 0
	for {
		batch, err := m.expireTextReviewBatch(ctx)
		closed += batch
		if err != nil || batch < textReviewExpiryBatch {
			return closed, err
		}
	}
}

func (m *Module) expireTextReviewBatch(ctx context.Context) (int, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin text review expiry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := m.now()
	rows, err := tx.Query(ctx, `
		WITH due AS (
			SELECT task.id
			FROM work_tasks task
			WHERE `+TaskIsTextReviewSQL+`
				AND task.state = 'OPEN'
				AND task.updated_at < $1
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		), expired AS (
			UPDATE work_tasks task
			SET
				state = 'COMPLETED',
				completed_by_kind = 'SERVICE',
				completed_by_subject = $3,
				completed_by_email = NULL,
				completed_at = $4,
				close_reason = $5,
				closed_via_call_id = NULL,
				version = task.version + 1,
				updated_at = $4
			FROM due
			WHERE task.id = due.id
			RETURNING task.id, task.version, task.practice_id
		), recorded AS (
			INSERT INTO work_task_activities (
				task_id,
				task_version,
				kind,
				actor_kind,
				actor_subject,
				actor_email,
				occurred_at,
				details
			)
			SELECT id, version, 'TASK_COMPLETED', 'SERVICE', $3, NULL, $4,
				jsonb_build_object('closeReason', $5::text)
			FROM expired
		)
		SELECT practice_id::text, count(*)
		FROM expired
		GROUP BY practice_id
	`, now.Add(-textReviewLifetime), textReviewExpiryBatch, textReviewExpiryActor,
		now, CloseReasonExpired)
	if err != nil {
		return 0, fmt.Errorf("expire text reviews: %w", err)
	}
	type practiceCount struct {
		practiceID string
		closed     int
	}
	counts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (practiceCount, error) {
		var count practiceCount
		err := row.Scan(&count.practiceID, &count.closed)
		return count, err
	})
	if err != nil {
		return 0, fmt.Errorf("expire text reviews: %w", err)
	}
	closed := 0
	for _, count := range counts {
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, count.practiceID); err != nil {
			return 0, err
		}
		closed += count.closed
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit text review expiry: %w", err)
	}
	return closed, nil
}
