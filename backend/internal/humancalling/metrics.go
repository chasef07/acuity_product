package humancalling

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/observability"
)

func (m *Module) recordPlayback(kind PlaybackKind, err error, duration time.Duration) {
	outcome := observability.RecordingPlaybackSucceeded
	if errors.Is(err, ErrDenied) {
		outcome = observability.RecordingPlaybackDenied
	}
	var unavailable *RecordingUnavailableError
	if errors.As(err, &unavailable) {
		switch unavailable.Reason {
		case RecordingNotFound:
			outcome = observability.RecordingPlaybackNotFound
		case RecordingProviderAuth:
			outcome = observability.RecordingPlaybackProviderAuth
		case RecordingRateLimited:
			outcome = observability.RecordingPlaybackRateLimited
		case RecordingProviderTimeout:
			outcome = observability.RecordingPlaybackTimeout
		case RecordingInvalidResponse:
			outcome = observability.RecordingPlaybackInvalidResponse
		case RecordingURLExpired:
			outcome = observability.RecordingPlaybackURLExpired
		default:
			outcome = observability.RecordingPlaybackUnavailable
		}
	} else if err != nil && !errors.Is(err, ErrDenied) {
		outcome = observability.RecordingPlaybackUnavailable
	}
	event := observability.VoicemailPlayback(outcome, duration)
	if kind == PlaybackCallRecording {
		event = observability.CallRecordingPlayback(outcome, duration)
	}
	observability.Record(m.observer, event)
}

func (m *Module) recordRecordingMaintenance(
	operation observability.RecordingMaintenanceOperation,
	outcome observability.RecordingMaintenanceOutcome,
	attempt int,
) {
	observability.Record(
		m.observer,
		observability.RecordingMaintenance(operation, outcome, attempt),
	)
}

func (m *Module) ReportReceiptQueue(ctx context.Context) error {
	now := m.now()
	var depth int64
	var projectionRetryDepth int64
	var relatedFactDepth int64
	var quarantinedDepth int64
	var oldest *time.Time
	if err := m.database.QueryRow(ctx, `
		SELECT
			pending.depth,
			pending.oldest,
			pending.projection_retry_depth,
			pending.related_fact_depth,
			quarantined.depth
		FROM (
			SELECT count(*) AS depth, min(received_at) AS oldest,
				count(*) FILTER (
					WHERE projection_error_code LIKE 'PROJECTION\_%'
				) AS projection_retry_depth,
				count(*) FILTER (
					WHERE projection_error_code IN (
						'WAITING_FOR_RELATED_FACT',
						'WAITING_FOR_RELATED_FACT_SLOW_RETRY'
					)
				) AS related_fact_depth
			FROM human_calling_provider_receipts
			WHERE state IN ('PENDING', 'PROCESSING')
		) pending
		CROSS JOIN (
			SELECT count(*) AS depth
			FROM human_calling_provider_receipts
			WHERE state = 'QUARANTINED'
		) quarantined
	`).Scan(
		&depth,
		&oldest,
		&projectionRetryDepth,
		&relatedFactDepth,
		&quarantinedDepth,
	); err != nil {
		return fmt.Errorf("read provider receipt queue: %w", err)
	}
	oldestAge := time.Duration(0)
	if oldest != nil {
		oldestAge = now.Sub(*oldest)
	}
	observability.Record(
		m.observer,
		observability.ReceiptQueue(
			depth,
			oldestAge,
			projectionRetryDepth,
			relatedFactDepth,
			quarantinedDepth,
		),
	)
	var staffOccupancy, unresolvedHangups int64
	var oldestStaffOccupancy, oldestHangup *time.Time
	if err := m.database.QueryRow(ctx, `
		SELECT
			occupancy.depth,
			occupancy.oldest,
			hangup.depth,
			hangup.oldest
		FROM (
			SELECT count(*) AS depth,
				min(COALESCE(
					leg.ending_at,
					leg.bridged_at,
					leg.bridge_pending_at,
					leg.updated_at
				)) AS oldest
			FROM human_calling_calls call
			JOIN human_calling_call_legs leg ON leg.call_id = call.id
			WHERE call.terminal_outcome IS NOT NULL
				AND leg.role = 'STAFF'
				AND (
					leg.state IN ('BRIDGE_PENDING', 'BRIDGED')
					OR (leg.state = 'ENDING' AND leg.answered_at IS NOT NULL)
				)
				AND COALESCE(
					leg.ending_at,
					leg.bridged_at,
					leg.bridge_pending_at,
					leg.updated_at
				) <= $1::timestamptz - interval '60 seconds'
		) occupancy
		CROSS JOIN (
			SELECT count(*) AS depth, min(command.created_at) AS oldest
			FROM human_calling_calls call
			JOIN human_calling_provider_commands command ON command.call_id = call.id
			WHERE call.terminal_outcome IS NOT NULL
				AND command.action = 'HANGUP_LEG'
				AND command.state IN ('PENDING', 'SENDING', 'SENT', 'AMBIGUOUS')
				AND command.created_at <= $1::timestamptz - interval '60 seconds'
		) hangup
	`, now).Scan(
		&staffOccupancy,
		&oldestStaffOccupancy,
		&unresolvedHangups,
		&oldestHangup,
	); err != nil {
		return fmt.Errorf("read terminal Call cleanup invariant: %w", err)
	}
	oldestStaffOccupancyAge := time.Duration(0)
	if oldestStaffOccupancy != nil {
		oldestStaffOccupancyAge = now.Sub(*oldestStaffOccupancy)
	}
	oldestHangupAge := time.Duration(0)
	if oldestHangup != nil {
		oldestHangupAge = now.Sub(*oldestHangup)
	}
	observability.Record(
		m.observer,
		observability.TerminalCleanup(
			staffOccupancy,
			oldestStaffOccupancyAge,
			unresolvedHangups,
			oldestHangupAge,
		),
	)
	var reconciliationDepth, reconciliationRetryDepth int64
	var retentionDepth, retentionRetryDepth, unavailableDepth int64
	var oldestReconciliation, oldestRetention *time.Time
	if err := m.database.QueryRow(ctx, `
		SELECT
			reconciliation.depth,
			reconciliation.oldest,
			reconciliation.retry_depth,
			retention.depth,
			retention.oldest,
			retention.retry_depth,
			unavailable.depth
		FROM (
			SELECT count(*) AS depth, min(call.ended_at) AS oldest,
				count(*) FILTER (
					WHERE recording.reconciliation_attempts > 0
				) AS retry_depth
			FROM human_calling_call_recordings recording
			JOIN human_calling_calls call ON call.id = recording.call_id
			WHERE recording.audio_state = 'PROCESSING'
				AND call.ended_at IS NOT NULL
		) reconciliation
		CROSS JOIN (
			SELECT count(*) AS depth, min(content_expires_at) AS oldest,
				count(*) FILTER (
					WHERE deletion_attempts > 0
				) AS retry_depth
			FROM human_calling_call_recordings
			WHERE audio_state = 'READY' AND content_expires_at <= $1
		) retention
		CROSS JOIN (
			SELECT count(*) AS depth
			FROM human_calling_call_recordings
			WHERE audio_state = 'UNAVAILABLE'
		) unavailable
	`, now).Scan(
		&reconciliationDepth,
		&oldestReconciliation,
		&reconciliationRetryDepth,
		&retentionDepth,
		&oldestRetention,
		&retentionRetryDepth,
		&unavailableDepth,
	); err != nil {
		return fmt.Errorf("read recording maintenance queue: %w", err)
	}
	oldestReconciliationAge := time.Duration(0)
	if oldestReconciliation != nil {
		oldestReconciliationAge = now.Sub(*oldestReconciliation)
	}
	oldestRetentionAge := time.Duration(0)
	if oldestRetention != nil {
		oldestRetentionAge = now.Sub(*oldestRetention)
	}
	observability.Record(
		m.observer,
		observability.RecordingQueue(
			reconciliationDepth,
			oldestReconciliationAge,
			reconciliationRetryDepth,
			retentionDepth,
			oldestRetentionAge,
			retentionRetryDepth,
			unavailableDepth,
		),
	)
	return nil
}

func (m *Module) recordReceiptProcessed(
	state ReceiptState,
	errorCode string,
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
	if state == ReceiptPending && strings.HasPrefix(errorCode, "WAITING_FOR_RELATED_FACT") {
		outcome = observability.ReceiptRelatedFact
	}
	if state == ReceiptFailed && errorCode == "TERMINAL_OR_OBSOLETE_PROVIDER_FACT" {
		outcome = observability.ReceiptObsolete
	}
	observability.Record(
		m.observer,
		observability.ReceiptProcessed(
			outcome,
			startedAt.Sub(receivedAt),
			completedAt.Sub(startedAt),
		),
	)
}

func (m *Module) recordProviderCommand(
	command ProviderCommand,
	state string,
	claimedAt time.Time,
	duration time.Duration,
) {
	action := observability.CommandAction(command.Action)
	switch command.Action {
	case CommandAnswerCaller:
		action = observability.CommandAnswerCaller
	case CommandStartRingWindow:
		action = observability.CommandStartRingWindow
	case CommandDialStaff:
		action = observability.CommandDialStaff
	case CommandBridge:
		action = observability.CommandBridge
	case CommandStopRingWindow:
		action = observability.CommandStopRingWindow
	case CommandHangupLeg:
		action = observability.CommandHangupLeg
	case CommandSpeakVoicemail:
		action = observability.CommandSpeakVoicemail
	case CommandStartVoicemailRecording:
		action = observability.CommandStartVoicemailRecording
	case CommandDialOutboundStaff:
		action = observability.CommandDialOutboundStaff
	case CommandDialOutboundDestination:
		action = observability.CommandDialOutboundDestination
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
			claimedAt.Sub(command.createdAt),
			duration,
		),
	)
}
