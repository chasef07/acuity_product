package work

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	textReviewLifetime    = 120 * time.Hour
	textReviewExpiryBatch = 500
	textReviewExpiryActor = "text-review-expiry"
)

const textReviewIsDueSQL = TaskIsTextReviewSQL + `
	AND task.state = 'OPEN'
	AND task.updated_at < $1`

func (m *Module) ExpireTextReviews(ctx context.Context) (int, error) {
	if m.access == nil {
		return 0, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin text review expiry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := m.now()
	due := now.Add(-textReviewLifetime)
	rows, err := tx.Query(ctx, `
		SELECT task.id::text, task.practice_id::text
		FROM work_tasks task
		WHERE `+textReviewIsDueSQL+`
		LIMIT $2
	`, due, textReviewExpiryBatch)
	if err != nil {
		return 0, fmt.Errorf("find due text reviews: %w", err)
	}
	var taskIDs, practiceIDs []string
	var taskID, practiceID string
	if _, err := pgx.ForEachRow(rows, []any{&taskID, &practiceID}, func() error {
		taskIDs = append(taskIDs, taskID)
		practiceIDs = append(practiceIDs, practiceID)
		return nil
	}); err != nil {
		return 0, fmt.Errorf("find due text reviews: %w", err)
	}
	if len(taskIDs) == 0 {
		return 0, nil
	}
	slices.Sort(practiceIDs)
	for _, practiceID := range slices.Compact(practiceIDs) {
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
			return 0, err
		}
	}
	var closed int
	if err := tx.QueryRow(ctx, `
		WITH due AS (
			SELECT task.id
			FROM work_tasks task
			WHERE `+textReviewIsDueSQL+`
				AND task.id = ANY($2::uuid[])
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
			RETURNING task.id, task.version
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
		SELECT count(*) FROM expired
	`, due, taskIDs, textReviewExpiryActor, now, CloseReasonExpired).Scan(&closed); err != nil {
		return 0, fmt.Errorf("expire text reviews: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit text review expiry: %w", err)
	}
	return closed, nil
}
