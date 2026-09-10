package humancalling

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// endedOrphanReceipt only retires lifecycle evidence for an unowned, ended
// provider leg in one fully terminal Product Call session. It never attaches
// the receipt by session or applies it to a different leg.
func (m *Module) endedOrphanReceipt(ctx context.Context, fact ProviderFact) (bool, error) {
	if (fact.Type != FactCallAnswered && fact.Type != FactCallHangup) ||
		fact.ClientState != "" || fact.CallControlID == "" || fact.CallLegID == "" || fact.CallSessionID == "" {
		return false, nil
	}
	provider, ok := m.provider.(CallEndStateProvider)
	if !ok {
		return false, nil
	}
	callID, err := m.terminalOrphanSession(ctx, fact)
	if err != nil || callID == "" {
		return false, err
	}
	ended, err := provider.IsCallEnded(ctx, fact.CallControlID, fact.CallLegID, fact.CallSessionID)
	if err != nil || !ended {
		return false, err
	}
	// Revalidate Product state after the provider read; a newly attached leg or
	// additional Call must retain its own projection path.
	currentCallID, err := m.terminalOrphanSession(ctx, fact)
	return err == nil && currentCallID == callID, err
}

func (m *Module) terminalOrphanSession(ctx context.Context, fact ProviderFact) (string, error) {
	var callID string
	err := m.database.QueryRow(ctx, `
		WITH session_calls AS (
			SELECT DISTINCT call_id FROM human_calling_call_legs
			WHERE provider_call_session_id = $1
		)
		SELECT call.id::text FROM human_calling_calls call
		WHERE call.id IN (SELECT call_id FROM session_calls)
			AND (SELECT count(*) FROM session_calls) = 1
			AND call.terminal_outcome IS NOT NULL AND call.ended_at IS NOT NULL
			AND NOT EXISTS (
				SELECT 1 FROM human_calling_call_legs leg
				WHERE leg.provider_call_control_id = $2 OR leg.provider_call_leg_id = $3
			)
			AND NOT EXISTS (
				SELECT 1 FROM human_calling_call_legs leg WHERE leg.call_id = call.id
				AND (leg.state NOT IN ('ENDED', 'FAILED') OR leg.ended_at IS NULL)
			)
			AND NOT EXISTS (
				SELECT 1 FROM human_calling_provider_commands command WHERE command.call_id = call.id
				AND command.state NOT IN ('RECONCILED', 'FAILED')
			)
	`, fact.CallSessionID, fact.CallControlID, fact.CallLegID).Scan(&callID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("classify terminal orphan session: %w", err)
	}
	return callID, nil
}
