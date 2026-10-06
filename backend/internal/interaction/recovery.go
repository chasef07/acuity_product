package interaction

import (
	"context"
	"errors"
	"fmt"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

func (m *Module) RecoverSourceClock(ctx context.Context, operator access.Identity, receiptID string) (Interaction, error) {
	if err := m.available(); err != nil {
		return Interaction{}, err
	}
	if !validUUID(receiptID) {
		return Interaction{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Interaction{}, fmt.Errorf("begin source clock recovery read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var receipt acceptedReceipt
	var raw []byte
	err = tx.QueryRow(ctx, `
		SELECT id::text, service_subject, practice_id::text, location_id::text,
			source_call_id, state, payload
		FROM ai_interaction_receipts WHERE id = $1
	`, receiptID).Scan(&receipt.ID, &receipt.ServiceSubject, &receipt.PracticeID,
		&receipt.LocationID, &receipt.SourceCallID, &receipt.State, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Interaction{}, ErrDenied
	}
	if err != nil {
		return Interaction{}, fmt.Errorf("read source clock recovery receipt: %w", err)
	}
	if _, err := m.authorize(ctx, tx, operator, receipt.PracticeID, receipt.LocationID, true, audienceOperator); err != nil {
		return Interaction{}, err
	}
	if err := tx.Rollback(ctx); err != nil {
		return Interaction{}, fmt.Errorf("finish source clock recovery read: %w", err)
	}
	command, stage, valid := receiptCommand(receipt, raw)
	if !valid {
		return Interaction{}, ErrInvalidInput
	}
	current, _, err := m.projectReceiptWithRecovery(ctx, receipt, command, stage, m.now().UTC(), &operator)
	return current, err
}

func (m *Module) RetireLegacySummary(ctx context.Context, operator access.Identity, receiptID string) error {
	if err := m.available(); err != nil {
		return err
	}
	if !validUUID(receiptID) {
		return ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin legacy receipt retirement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID, locationID, sourceCallID, state string
	err = tx.QueryRow(ctx, `
		SELECT practice_id::text, location_id::text, source_call_id, state
		FROM ai_interaction_receipts WHERE id = $1 AND kind = 'SUMMARY' FOR UPDATE
	`, receiptID).Scan(&practiceID, &locationID, &sourceCallID, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("lock legacy receipt: %w", err)
	}
	authorization, err := m.authorize(ctx, tx, operator, practiceID, locationID, true, audienceOperator)
	if err != nil {
		return err
	}
	if state == string(receiptRetired) {
		return tx.Commit(ctx)
	}
	if state != string(receiptQuarantined) {
		return ErrConflict
	}
	var interactionID string
	err = tx.QueryRow(ctx, `
		SELECT interaction.id::text FROM ai_interactions interaction
		WHERE interaction.practice_id = $1 AND interaction.location_id = $2
			AND interaction.source_call_id = $3 AND interaction.status <> 'IN_PROGRESS'
			AND interaction.ended_at IS NOT NULL
			AND EXISTS (
				SELECT 1 FROM ai_interaction_receipts closeout
				WHERE closeout.interaction_id = interaction.id
					AND closeout.kind = 'CLOSEOUT' AND closeout.state = 'PROJECTED'
			)
		FOR UPDATE OF interaction
	`, practiceID, locationID, sourceCallID).Scan(&interactionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("lock terminal AI Interaction: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ai_interaction_receipts SET state = 'RETIRED', interaction_id = $2
		WHERE id = $1 AND state = 'QUARANTINED'
	`, receiptID, interactionID); err != nil {
		return fmt.Errorf("retire legacy receipt: %w", err)
	}
	if err := m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{
		Action: "ai_interaction.legacy_receipt_retired", ResourceType: "ai_interaction_receipt",
		ResourceID: receiptID, ResourceVersion: 1, OccurredAt: m.now().UTC(),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
