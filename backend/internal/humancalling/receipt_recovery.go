package humancalling

import (
	"context"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

// RecoverQuarantinedRingtone applies one verified terminal ringtone receipt.
// The exact original bridge and hangup commands must correlate; the Call must
// already be terminal with no active work. No provider effect is requested.
func (m *Module) RecoverQuarantinedRingtone(
	ctx context.Context,
	command RequeueQuarantinedReceiptCommand,
) error {
	if m.database == nil || m.access == nil || command.PracticeID == "" ||
		command.ReceiptReference == "" || command.EventID != "" {
		return ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locationID string
	if err := tx.QueryRow(ctx, `
		SELECT id::text FROM access_locations
		WHERE practice_id = $1 ORDER BY id LIMIT 1
	`, command.PracticeID).Scan(&locationID); err != nil {
		return ErrDenied
	}
	authorization, err := m.access.LockMutationAuthorization(ctx, tx, command.Identity, command.PracticeID, locationID)
	if err != nil || !authorization.PlatformOperator {
		return ErrDenied
	}
	eventID, err := m.resolveQuarantinedReceiptReference(ctx, tx, command.PracticeID, command.ReceiptReference)
	if err != nil {
		return err
	}
	var raw []byte
	var callID string
	var attempts int64
	if err := tx.QueryRow(ctx, `
		SELECT raw_body, call_id::text, projection_attempts
		FROM human_calling_provider_receipts
		WHERE event_id = $1 AND state = 'QUARANTINED'
			AND event_type = 'call.playback.ended'
			AND projection_error_code = 'PROJECTION_APPLY_FACT_CONFLICT'
		FOR UPDATE
	`, eventID).Scan(&raw, &callID, &attempts); err != nil {
		return ErrConflict
	}
	fact, supported, err := normalizeTelnyxFact(raw)
	state, hasState := parseCallLegClientState(fact.ClientState)
	if err != nil || !supported || !hasState || fact.EventID != eventID || state.CallID != callID ||
		fact.Type != FactPlaybackEnded || fact.PlaybackStatus != "call_hangup" || state.Role != "STAFF" ||
		(state.Kind != "cleanup" && state.Kind != callLegClientStateStaffHangup && state.Kind != "outbound_media") {
		return ErrConflict
	}
	var terminal bool
	if err := tx.QueryRow(ctx, `
		SELECT terminal_outcome IS NOT NULL AND ended_at IS NOT NULL
		FROM human_calling_calls WHERE id = $1 AND practice_id = $2 FOR UPDATE
	`, callID, command.PracticeID).Scan(&terminal); err != nil || !terminal {
		return ErrConflict
	}
	var active bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM human_calling_call_legs
			WHERE call_id = $1 AND state NOT IN ('ENDED', 'FAILED')
		) OR EXISTS (
			SELECT 1 FROM human_calling_provider_commands
			WHERE call_id = $1 AND state IN ('PENDING', 'SENDING', 'AMBIGUOUS')
		) OR EXISTS (
			SELECT 1 FROM human_calling_provider_receipts
			WHERE call_id = $1 AND state IN ('PENDING', 'PROCESSING')
		)
	`, callID).Scan(&active); err != nil || active {
		return ErrConflict
	}
	if err := requireOutboundRingtoneFact(ctx, tx, fact, state); err != nil {
		return err
	}
	if _, err := claimProviderFact(ctx, tx, fact, m.now()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_receipts
		SET state = 'APPLIED', projection_error_code = NULL,
			quarantined_at = NULL, projected_at = $2
		WHERE event_id = $1 AND state = 'QUARANTINED'
	`, eventID, m.now()); err != nil {
		return err
	}
	if err := m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{
		Action: "provider_receipt.recovered", ResourceType: "provider_receipt", ResourceID: eventID,
		ResourceVersion: attempts, OccurredAt: m.now(),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
