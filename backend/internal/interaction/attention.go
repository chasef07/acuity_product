package interaction

import (
	"context"
	"fmt"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

// ReviewRecentOutcomes is scoped to the real User and all eligible appointments,
// including unloaded pages. Reading evidence never changes a Task's state.
func (m *Module) ReviewRecentOutcomes(ctx context.Context, identity access.Identity, practiceID, locationID string) error {
	if m.database == nil || m.access == nil {
		return ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockReadAuthorization(ctx, tx, identity, practiceID, locationID)
	if err != nil {
		return ErrDenied
	}
	locations := authorization.Locations
	if authorization.ActiveLocation != nil {
		locations = []access.Location{*authorization.ActiveLocation}
	}
	now := m.now()
	changed := false
	for _, location := range locations {
		mutation, err := m.access.LockMutationAuthorization(ctx, tx, identity, practiceID, location.ID)
		if err != nil {
			return ErrDenied
		}
		tag, err := tx.Exec(ctx, `
     UPDATE ai_interaction_attention attention SET reviewed_at = $4
     FROM ai_interactions interaction
     WHERE attention.interaction_id = interaction.id
       AND attention.user_subject = $3 AND attention.reviewed_at IS NULL
       AND attention.outcome_occurred_at >= $5 AND attention.outcome_occurred_at <= $4
       AND interaction.practice_id = $1 AND interaction.location_id = $2
       AND interaction.appointment_action IN ('BOOKED', 'CANCELLED', 'RESCHEDULED')
       AND NOT EXISTS (
         SELECT 1 FROM work_tasks task
         WHERE task.practice_id = interaction.practice_id
           AND task.source_call_id = interaction.source_call_id AND task.state = 'OPEN'
       )`, practiceID, location.ID, identity.Subject, now, now.Add(-7*24*time.Hour))
		if err != nil {
			return fmt.Errorf("review recent appointment outcomes: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		changed = true
		if err := m.access.AuditOperatorMutation(ctx, tx, mutation, access.OperatorMutationAudit{
			Action: "ai_interactions.review_recent", ResourceType: "location", ResourceID: location.ID,
			ResourceVersion: 1, OccurredAt: now,
		}); err != nil {
			return err
		}
	}
	if changed {
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
