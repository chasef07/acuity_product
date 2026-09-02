package interaction

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

// RecoverSourceClock repairs one explicitly selected historical receipt whose
// only source conflict is startedAt. It preserves its original payload and
// fingerprint, applies the normal lifecycle rules, and audits the operator.
// Retired SUMMARY receipts are immutable history and cannot use this path.
func (m *Module) RecoverSourceClock(ctx context.Context, operator access.Identity, receiptID string) (Interaction, error) {
	if m.database == nil || m.access == nil || receiptID == "" {
		return Interaction{}, ErrInvalidInput
	}
	var receipt acceptedReceipt
	var raw []byte
	if err := m.database.QueryRow(ctx, `
		SELECT id::text, service_subject, practice_id::text, location_id::text,
			source_call_id, state, payload
		FROM ai_interaction_receipts WHERE id = $1
	`, receiptID).Scan(&receipt.ID, &receipt.ServiceSubject, &receipt.PracticeID,
		&receipt.LocationID, &receipt.SourceCallID, &receipt.State, &raw); err != nil {
		return Interaction{}, fmt.Errorf("read source clock recovery receipt: %w", err)
	}
	var payload storedReceiptPayload
	if json.Unmarshal(raw, &payload) != nil {
		return Interaction{}, ErrInvalidInput
	}
	command := IngestCommand{
		Service: access.ServiceIdentity{Subject: receipt.ServiceSubject},
		Kind:    payload.Kind, OfficeKey: payload.OfficeKey, SourceCallID: payload.SourceCallID,
		CallerPhone: payload.CallerPhone, OfficePhone: payload.OfficePhone,
		StartedAt: payload.StartedAt, EndedAt: payload.EndedAt, Status: payload.Status,
		Summary: payload.Summary, Transcript: payload.Transcript,
		Appointment: payload.Appointment, CloseoutPayload: payload.CloseoutPayload,
	}
	normalizeCommand(&command)
	stage := messageLifecycleStage(command.Kind)
	if stage == 0 || !validCommand(command) || command.SourceCallID != receipt.SourceCallID {
		return Interaction{}, ErrInvalidInput
	}
	current, _, err := m.projectReceiptWithRecovery(ctx, receipt, command, stage, m.now().UTC(), &operator)
	return current, err
}

// RetireLegacySummary preserves the original payload, fingerprint and error,
// and links it to an outcome already established by a supported CLOSEOUT.
func (m *Module) RetireLegacySummary(ctx context.Context, operator access.Identity, receiptID string) error {
	if m.database == nil || m.access == nil || receiptID == "" {
		return ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID, locationID, sourceCallID, state string
	if err := tx.QueryRow(ctx, `
		SELECT practice_id::text, location_id::text, source_call_id, state
		FROM ai_interaction_receipts WHERE id = $1 AND kind = 'SUMMARY' FOR UPDATE
	`, receiptID).Scan(&practiceID, &locationID, &sourceCallID, &state); err != nil {
		return ErrConflict
	}
	authorization, err := m.access.LockMutationAuthorization(ctx, tx, operator, practiceID, locationID)
	if err != nil || !authorization.PlatformOperator {
		return access.ErrDenied
	}
	if state == string(receiptRetired) {
		return tx.Commit(ctx)
	}
	if state != string(receiptQuarantined) {
		return ErrConflict
	}
	var interactionID string
	if err := tx.QueryRow(ctx, `
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
	`, practiceID, locationID, sourceCallID).Scan(&interactionID); err != nil {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ai_interaction_receipts SET state = 'RETIRED', interaction_id = $2
		WHERE id = $1 AND state = 'QUARANTINED'
	`, receiptID, interactionID); err != nil {
		return err
	}
	if err := m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{
		Action: "ai_interaction.legacy_receipt_retired", ResourceType: "ai_interaction_receipt",
		ResourceID: receiptID, ResourceVersion: 1, OccurredAt: m.now().UTC(),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
