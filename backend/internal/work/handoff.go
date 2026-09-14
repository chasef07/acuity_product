package work

import (
	"context"
	"errors"
	"fmt"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

// ValidateHandoffTask checks an explicit same-need reference before a handoff
// can be admitted. Task source is immutable; completion may race with the call
// without invalidating its evidence or reopening completed work.
func (m *Module) ValidateHandoffTask(
	ctx context.Context,
	tx pgx.Tx,
	service access.ServiceIdentity,
	taskID, locationID, sourceCallID, phone string,
) error {
	var id string
	err := tx.QueryRow(ctx, `
		SELECT id::text FROM work_tasks
		WHERE id = $1 AND practice_id = $2 AND location_id = $3
			AND source_call_id = $4 AND phone = $5
			AND origin = 'ABITA_AI' AND created_by_subject = $6
		FOR SHARE
	`, taskID, service.PracticeID, locationID, sourceCallID, phone, service.Subject).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDenied
	}
	if err != nil {
		return fmt.Errorf("validate handoff Task: %w", err)
	}
	return nil
}
