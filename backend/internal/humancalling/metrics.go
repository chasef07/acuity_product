package humancalling

import (
	"context"
	"fmt"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/observability"
)

func (m *Module) ReportReceiptQueue(ctx context.Context) error {
	now := m.now()
	var depth int64
	var oldest *time.Time
	if err := m.pool.QueryRow(ctx, `
		SELECT count(*), min(received_at)
		FROM human_calling_provider_receipts
		WHERE state IN ('PENDING', 'PROCESSING')
	`).Scan(&depth, &oldest); err != nil {
		return fmt.Errorf("read provider receipt queue: %w", err)
	}
	oldestAge := time.Duration(0)
	if oldest != nil {
		oldestAge = now.Sub(*oldest)
	}
	observability.Record(
		m.observer,
		observability.ReceiptQueue(depth, oldestAge),
	)
	return nil
}

func (m *Module) recordReceiptProcessed(
	state ReceiptState,
	receivedAt, startedAt, completedAt time.Time,
) {
	outcome := observability.ReceiptRetry
	switch state {
	case ReceiptApplied:
		outcome = observability.ReceiptApplied
	case ReceiptUnknown:
		outcome = observability.ReceiptUnknown
	case ReceiptFailed:
		outcome = observability.ReceiptFailed
	case ReceiptQuarantined:
		outcome = observability.ReceiptQuarantined
	}
	observability.Record(
		m.observer,
		observability.ReceiptProcessed(
			outcome,
			completedAt.Sub(receivedAt),
			completedAt.Sub(startedAt),
		),
	)
}

func (m *Module) recordProviderCommand(
	command ProviderCommand,
	state string,
	startedAt time.Time,
	duration time.Duration,
) {
	action := observability.CommandAction(command.Action)
	switch command.Action {
	case CommandAnswerCaller:
		action = observability.CommandAnswerCaller
	case CommandStartRingback:
		action = observability.CommandStartRingback
	case CommandDialStaff:
		action = observability.CommandDialStaff
	case CommandHangup:
		action = observability.CommandHangup
	case CommandStartRecording:
		action = observability.CommandStartRecording
	case CommandCreateCredential:
		action = observability.CommandCreateCredential
	case CommandDisableCredential:
		action = observability.CommandDisableCredential
	case CommandCreateJWT:
		action = observability.CommandCreateJWT
	}
	outcome := observability.CommandRejected
	switch state {
	case "SENT":
		outcome = observability.CommandSent
	case "AMBIGUOUS":
		outcome = observability.CommandAmbiguous
	case "RECONCILED":
		outcome = observability.CommandReconciled
	case "OBSOLETE":
		outcome = observability.CommandObsolete
	}
	observability.Record(
		m.observer,
		observability.ProviderCommandCompleted(
			action,
			outcome,
			startedAt.Sub(command.createdAt),
			duration,
		),
	)
}
