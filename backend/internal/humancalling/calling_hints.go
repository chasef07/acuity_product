package humancalling

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const callingHintChannel = "acuity_calling_hints"

func (m *Module) recordCallingChange(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
) error {
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	return publishCallingHint(ctx, tx, practiceID)
}

func publishCallingHint(ctx context.Context, tx pgx.Tx, practiceID string) error {
	practiceID = strings.TrimSpace(practiceID)
	if tx == nil || practiceID == "" {
		return ErrInvalidInput
	}
	if _, err := tx.Exec(ctx, `
		SELECT pg_notify($1, json_build_object('practiceId', $2::text)::text)
	`, callingHintChannel, practiceID); err != nil {
		return fmt.Errorf("publish calling hint: %w", err)
	}
	return nil
}

func publishCallLegCallingHint(ctx context.Context, tx pgx.Tx, callLegID string) error {
	if _, err := tx.Exec(ctx, `
		SELECT pg_notify($1, json_build_object('practiceId', call.practice_id::text)::text)
		FROM human_calling_call_legs leg
		JOIN human_calling_calls call ON call.id = leg.call_id
		WHERE leg.id = $2
	`, callingHintChannel, callLegID); err != nil {
		return fmt.Errorf("publish CallLeg calling hint: %w", err)
	}
	return nil
}
