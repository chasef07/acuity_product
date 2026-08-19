package humancalling

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/jackc/pgx/v5"
)

type RecordingAudioState string

const (
	RecordingProcessing  RecordingAudioState = "PROCESSING"
	RecordingReady       RecordingAudioState = "READY"
	RecordingUnavailable RecordingAudioState = "UNAVAILABLE"
	RecordingExpired     RecordingAudioState = "EXPIRED"
	RecordingDeleted     RecordingAudioState = "DELETED"
)

const (
	recordingReconciliationMaximumAttempts = 10
	recordingReconciliationExhaustedCode   = "RECONCILIATION_RETRY_EXHAUSTED"
)

type CallRecording struct {
	AudioState      RecordingAudioState
	DurationSeconds int64
}

func (m *Module) applyConnectedCallRecordingSaved(
	ctx context.Context,
	fact ProviderFact,
) error {
	state, ok := connectedRecordingSavedCandidateState(fact)
	if !ok || !fact.RecordingEndedAt.After(fact.RecordingStartedAt) {
		return ErrConflict
	}
	if fact.RecordingID == "" {
		var alreadyApplied bool
		if err := m.database.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM human_calling_projected_facts WHERE event_id = $1
			)
		`, fact.EventID).Scan(&alreadyApplied); err != nil {
			return fmt.Errorf("check connected recording callback replay: %w", err)
		}
		if alreadyApplied {
			return nil
		}
		provider, ok := m.provider.(RecordingStateProvider)
		if !ok {
			return fmt.Errorf("%w: provider cannot resolve recording", ErrAmbiguousEffect)
		}
		recording, err := provider.ResolveRecording(ctx, fact.CallLegID, fact.CallSessionID)
		if err != nil {
			return err
		}
		fact.RecordingID = recording.ID
		fact.CallControlID = recording.CallControlID
		fact.RecordingStartedAt = recording.StartedAt
		fact.RecordingEndedAt = recording.EndedAt
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin connected recording save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	practiceID, bridged, err := m.requireExactConnectedRecordingLeg(ctx, tx, state, fact)
	if err != nil {
		return err
	}
	if !bridged {
		return ErrConflict
	}
	var audioState, providerRecordingID string
	var retentionDays int
	if err := tx.QueryRow(ctx, `
		SELECT audio_state, retention_days, COALESCE(provider_recording_id, '')
		FROM human_calling_call_recordings
		WHERE call_id = $1 FOR UPDATE
	`, state.CallID).Scan(&audioState, &retentionDays, &providerRecordingID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("lock connected recording: %w", err)
	}
	if providerRecordingID != "" && providerRecordingID != fact.RecordingID {
		return ErrConflict
	}
	if audioState == string(RecordingReady) || audioState == string(RecordingDeleted) {
		return tx.Commit(ctx)
	}
	duration := fact.RecordingEndedAt.Sub(fact.RecordingStartedAt)
	contentExpiresAt := fact.RecordingEndedAt.Add(
		time.Duration(retentionDays) * 24 * time.Hour,
	)
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_recordings SET
			audio_state = 'READY', provider_recording_id = $2,
			recording_started_at = $3, recording_ended_at = $4,
			content_expires_at = $5, duration_millis = $6,
			last_error_code = NULL, reconciliation_claimed_at = NULL,
			next_reconciliation_attempt_at = NULL,
			reconciliation_error_code = NULL, updated_at = $7
		WHERE call_id = $1
	`, state.CallID, fact.RecordingID, fact.RecordingStartedAt,
		fact.RecordingEndedAt, contentExpiresAt, duration.Milliseconds(), m.now()); err != nil {
		return fmt.Errorf("commit connected recording evidence: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1, updated_at = $2
		WHERE id = $1
	`, state.CallID, m.now()); err != nil {
		return err
	}
	if err := appendTimeline(ctx, tx, state.CallID, practiceID,
		"call.recording.ready", "", fact.EventID, "",
		opaqueReference(fact.RecordingID), "", fact.OccurredAt); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *Module) applyConnectedCallRecordingError(
	ctx context.Context,
	fact ProviderFact,
) error {
	state, ok := connectedRecordingState(fact)
	if !ok {
		return ErrConflict
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin connected recording failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	practiceID, _, err := m.requireExactConnectedRecordingLeg(ctx, tx, state, fact)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `
		UPDATE human_calling_call_recordings SET
			audio_state = 'UNAVAILABLE', provider_recording_id = NULL,
			recording_started_at = NULL, recording_ended_at = NULL,
			duration_millis = NULL, last_error_code = 'RECORDING_FAILED',
			reconciliation_claimed_at = NULL,
			next_reconciliation_attempt_at = NULL,
			reconciliation_error_code = NULL, updated_at = $2
		WHERE call_id = $1 AND audio_state IN ('PROCESSING', 'UNAVAILABLE')
	`, state.CallID, m.now())
	if err != nil {
		return fmt.Errorf("commit connected recording failure: %w", err)
	}
	if result.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1, updated_at = $2
		WHERE id = $1
	`, state.CallID, m.now()); err != nil {
		return err
	}
	if err := appendTimeline(ctx, tx, state.CallID, practiceID,
		"call.recording.unavailable", "", fact.EventID, "",
		opaqueReference(fact.CallLegID), "RECORDING_FAILED", fact.OccurredAt); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func connectedRecordingState(fact ProviderFact) (callLegClientState, bool) {
	state, ok := parseCallLegClientState(fact.ClientState)
	connectedKind := state.Kind == "bridge" ||
		state.Kind == callLegClientStateStaffHangup
	return state, ok && connectedKind &&
		(state.Role == "STAFF" || state.Role == "DESTINATION")
}

func connectedRecordingSavedCandidateState(fact ProviderFact) (callLegClientState, bool) {
	state, ok := parseCallLegClientState(fact.ClientState)
	return state, ok && (state.Role == "STAFF" || state.Role == "DESTINATION")
}

func (m *Module) ProcessNextRecordingReconciliation(
	ctx context.Context,
) (bool, error) {
	provider, ok := m.provider.(RecordingStateProvider)
	if !ok {
		return false, nil
	}
	now := m.now()
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin recording reconciliation claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var callID, callLegID, role string
	var providerCallLegID, callSessionID string
	var attempts int
	err = tx.QueryRow(ctx, `
		SELECT recording.call_id::text, bridge.call_leg_id::text, bridge.role,
			bridge.provider_call_leg_id,
			bridge.provider_call_session_id, recording.reconciliation_attempts + 1
		FROM human_calling_call_recordings recording
		JOIN human_calling_calls call ON call.id = recording.call_id
		JOIN LATERAL (
			SELECT leg.id AS call_leg_id, leg.role,
				leg.provider_call_control_id, leg.provider_call_leg_id,
				leg.provider_call_session_id
			FROM human_calling_provider_commands command
			JOIN human_calling_call_legs leg ON leg.id = command.call_leg_id
			WHERE command.call_id = recording.call_id
				AND command.action = 'BRIDGE'
				AND command.state IN ('SENT', 'AMBIGUOUS', 'RECONCILED')
				AND leg.role IN ('STAFF', 'DESTINATION')
				AND leg.provider_call_control_id IS NOT NULL
				AND leg.provider_call_leg_id IS NOT NULL
				AND leg.provider_call_session_id IS NOT NULL
			ORDER BY command.created_at DESC, command.id DESC
			LIMIT 1
		) bridge ON true
		WHERE recording.audio_state = 'PROCESSING'
			AND call.ended_at IS NOT NULL
			AND call.ended_at <= $1::timestamptz - interval '2 minutes'
			AND (
				recording.next_reconciliation_attempt_at IS NULL
				OR recording.next_reconciliation_attempt_at <= $1
			)
			AND (
				recording.reconciliation_claimed_at IS NULL
				OR recording.reconciliation_claimed_at <= $1::timestamptz - interval '2 minutes'
			)
		ORDER BY call.ended_at, recording.call_id
		FOR UPDATE OF recording SKIP LOCKED
		LIMIT 1
	`, now).Scan(
		&callID, &callLegID, &role, &providerCallLegID, &callSessionID, &attempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, fmt.Errorf("claim stale recording: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_recordings
		SET reconciliation_attempts = $2, reconciliation_claimed_at = $3,
			next_reconciliation_attempt_at = NULL,
			reconciliation_error_code = NULL, updated_at = $3
		WHERE call_id = $1
	`, callID, attempts, now); err != nil {
		return false, fmt.Errorf("mark recording reconciliation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit recording reconciliation claim: %w", err)
	}

	recording, err := provider.ResolveRecording(ctx, providerCallLegID, callSessionID)
	if err != nil {
		if errors.Is(err, ErrProviderRecordingFailed) {
			failedAt := now
			var failure *providerRecordingFailure
			if errors.As(err, &failure) && !failure.OccurredAt.IsZero() {
				failedAt = failure.OccurredAt
			}
			fact := ProviderFact{
				EventID: "recording-error-reconciliation-" + opaqueReference(
					providerCallLegID+"\x00"+callSessionID+"\x00"+
						failedAt.UTC().Format(time.RFC3339Nano),
				),
				Type:          FactRecordingError,
				OccurredAt:    failedAt,
				CallLegID:     providerCallLegID,
				CallSessionID: callSessionID,
				ClientState:   encodeCallLegClientState(callID, callLegID, role, "bridge"),
			}
			if applyErr := m.applyConnectedCallRecordingError(ctx, fact); applyErr != nil {
				return m.failRecordingReconciliation(
					ctx, callID, now, attempts, applyErr,
				)
			}
			m.recordRecordingMaintenance(
				observability.RecordingReconciliation,
				observability.RecordingMaintenanceUnavailable,
				attempts,
			)
			return true, nil
		}
		return m.failRecordingReconciliation(ctx, callID, now, attempts, err)
	}
	fact := ProviderFact{
		EventID: "recording-reconciliation-" + opaqueReference(recording.ID),
		Type:    FactRecordingSaved, OccurredAt: recording.EndedAt,
		CallControlID: recording.CallControlID, CallLegID: recording.CallLegID,
		CallSessionID: recording.CallSessionID,
		ClientState:   encodeCallLegClientState(callID, callLegID, role, "bridge"),
		RecordingID:   recording.ID, RecordingStartedAt: recording.StartedAt,
		RecordingEndedAt: recording.EndedAt,
	}
	if err := m.applyConnectedCallRecordingSaved(ctx, fact); err != nil {
		return m.failRecordingReconciliation(ctx, callID, now, attempts, err)
	}
	m.recordRecordingMaintenance(
		observability.RecordingReconciliation,
		observability.RecordingMaintenanceSucceeded,
		attempts,
	)
	return true, nil
}

func (m *Module) failRecordingReconciliation(
	ctx context.Context,
	callID string,
	claimedAt time.Time,
	attempts int,
	reconciliationErr error,
) (bool, error) {
	if attempts >= recordingReconciliationMaximumAttempts {
		processed, err := m.exhaustRecordingReconciliation(ctx, callID, claimedAt)
		outcome := observability.RecordingMaintenanceExhausted
		if err != nil {
			outcome = observability.RecordingMaintenanceFailed
		}
		m.recordRecordingMaintenance(
			observability.RecordingReconciliation,
			outcome,
			attempts,
		)
		return processed, err
	}
	retryAt := claimedAt.Add(recordingMaintenanceBackoff(attempts))
	result, err := m.database.Exec(ctx, `
		UPDATE human_calling_call_recordings
		SET reconciliation_claimed_at = NULL,
			next_reconciliation_attempt_at = $3,
			reconciliation_error_code = $4, updated_at = $2
		WHERE call_id = $1 AND audio_state = 'PROCESSING'
			AND reconciliation_claimed_at = $2
	`, callID, claimedAt, retryAt, safeProviderErrorCode(reconciliationErr))
	if err != nil {
		m.recordRecordingMaintenance(
			observability.RecordingReconciliation,
			observability.RecordingMaintenanceFailed,
			attempts,
		)
		return true, fmt.Errorf("record recording reconciliation failure: %w", err)
	}
	if result.RowsAffected() == 0 {
		return true, nil
	}
	m.recordRecordingMaintenance(
		observability.RecordingReconciliation,
		observability.RecordingMaintenanceRetry,
		attempts,
	)
	return true, fmt.Errorf("reconcile provider recording: %w", reconciliationErr)
}

func (m *Module) exhaustRecordingReconciliation(
	ctx context.Context,
	callID string,
	claimedAt time.Time,
) (bool, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return true, fmt.Errorf("begin recording reconciliation exhaustion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID string
	err = tx.QueryRow(ctx, `
		UPDATE human_calling_call_recordings SET
			audio_state = 'UNAVAILABLE', provider_recording_id = NULL,
			recording_started_at = NULL, recording_ended_at = NULL,
			content_expires_at = NULL, duration_millis = NULL,
			last_error_code = $3, reconciliation_claimed_at = NULL,
			next_reconciliation_attempt_at = NULL,
			reconciliation_error_code = NULL, updated_at = $2
		WHERE call_id = $1 AND audio_state = 'PROCESSING'
			AND reconciliation_claimed_at = $2
		RETURNING practice_id::text
	`, callID, claimedAt, recordingReconciliationExhaustedCode).Scan(&practiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, tx.Commit(ctx)
	}
	if err != nil {
		return true, fmt.Errorf("exhaust recording reconciliation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1, updated_at = $2
		WHERE id = $1
	`, callID, claimedAt); err != nil {
		return true, err
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"call.recording.unavailable",
		"",
		"",
		"",
		opaqueReference(callID),
		recordingReconciliationExhaustedCode,
		claimedAt,
	); err != nil {
		return true, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return true, err
	}
	if err := tx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit recording reconciliation exhaustion: %w", err)
	}
	return true, nil
}

func (m *Module) ProcessNextRecordingRetention(
	ctx context.Context,
) (bool, error) {
	provider, ok := m.provider.(RecordingDeletionProvider)
	if !ok {
		return false, nil
	}
	now := m.now()
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin recording retention claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var callID, practiceID, recordingID string
	var attempts int
	err = tx.QueryRow(ctx, `
		SELECT recording.call_id::text, recording.practice_id::text,
			recording.provider_recording_id, recording.deletion_attempts + 1
		FROM human_calling_call_recordings recording
		WHERE recording.audio_state = 'READY'
			AND recording.provider_recording_id IS NOT NULL
			AND recording.content_expires_at <= $1
			AND (
				recording.next_deletion_attempt_at IS NULL
				OR recording.next_deletion_attempt_at <= $1
			)
			AND (
				recording.deletion_claimed_at IS NULL
				OR recording.deletion_claimed_at <= $1 - interval '2 minutes'
			)
		ORDER BY recording.content_expires_at, recording.call_id
		FOR UPDATE OF recording SKIP LOCKED
		LIMIT 1
	`, now).Scan(&callID, &practiceID, &recordingID, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, fmt.Errorf("claim expired recording: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_recordings
		SET deletion_attempts = $2, deletion_claimed_at = $3,
			next_deletion_attempt_at = NULL, deletion_error_code = NULL,
			updated_at = $3
		WHERE call_id = $1
	`, callID, attempts, now); err != nil {
		return false, fmt.Errorf("mark expired recording deletion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit recording retention claim: %w", err)
	}

	if err := provider.DeleteRecording(ctx, recordingID); err != nil {
		retryAt := now.Add(recordingMaintenanceBackoff(attempts))
		if _, updateErr := m.database.Exec(ctx, `
			UPDATE human_calling_call_recordings
			SET deletion_claimed_at = NULL, next_deletion_attempt_at = $3,
				deletion_error_code = $4, updated_at = $2
			WHERE call_id = $1 AND deletion_claimed_at = $2
		`, callID, now, retryAt, safeProviderErrorCode(err)); updateErr != nil {
			m.recordRecordingMaintenance(
				observability.RecordingRetention,
				observability.RecordingMaintenanceFailed,
				attempts,
			)
			return true, fmt.Errorf("record recording deletion failure: %w", updateErr)
		}
		m.recordRecordingMaintenance(
			observability.RecordingRetention,
			observability.RecordingMaintenanceRetry,
			attempts,
		)
		return true, fmt.Errorf("delete expired provider recording: %w", err)
	}

	completion, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return true, fmt.Errorf("begin recording retention completion: %w", err)
	}
	defer func() { _ = completion.Rollback(ctx) }()
	result, err := completion.Exec(ctx, `
		UPDATE human_calling_call_recordings
		SET audio_state = 'DELETED', content_deleted_at = $3,
			deletion_claimed_at = NULL, next_deletion_attempt_at = NULL,
			deletion_error_code = NULL, updated_at = $3
		WHERE call_id = $1 AND provider_recording_id = $2
			AND audio_state = 'READY' AND deletion_claimed_at = $3
	`, callID, recordingID, now)
	if err != nil {
		return true, fmt.Errorf("complete recording retention: %w", err)
	}
	if result.RowsAffected() == 0 {
		return true, ErrConflict
	}
	if _, err := completion.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1, updated_at = $2
		WHERE id = $1
	`, callID, now); err != nil {
		return true, err
	}
	if err := appendTimeline(ctx, completion, callID, practiceID,
		"call.recording.content_deleted", "", "", "",
		opaqueReference(recordingID), "", now); err != nil {
		return true, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, completion, practiceID); err != nil {
		return true, err
	}
	if err := completion.Commit(ctx); err != nil {
		m.recordRecordingMaintenance(
			observability.RecordingRetention,
			observability.RecordingMaintenanceFailed,
			attempts,
		)
		return true, err
	}
	m.recordRecordingMaintenance(
		observability.RecordingRetention,
		observability.RecordingMaintenanceSucceeded,
		attempts,
	)
	return true, nil
}

func recordingMaintenanceBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := time.Minute << min(attempts-1, 10)
	if delay > 24*time.Hour {
		return 24 * time.Hour
	}
	return delay
}

func (m *Module) requireExactConnectedRecordingLeg(
	ctx context.Context,
	tx pgx.Tx,
	state callLegClientState,
	fact ProviderFact,
) (string, bool, error) {
	var practiceID, controlID, legID, sessionID string
	var bridged bool
	if err := tx.QueryRow(ctx, `
		SELECT call.practice_id::text, leg.provider_call_control_id,
			leg.provider_call_leg_id, COALESCE(leg.provider_call_session_id, ''),
			leg.bridged_at IS NOT NULL
		FROM human_calling_calls call
		JOIN human_calling_call_legs leg
			ON leg.call_id = call.id AND leg.id = $2 AND leg.role = $3
		WHERE call.id = $1
		FOR UPDATE OF call, leg
	`, state.CallID, state.CallLegID, state.Role).Scan(
		&practiceID, &controlID, &legID, &sessionID, &bridged,
	); err != nil {
		return "", false, fmt.Errorf("lock connected recording CallLeg: %w", err)
	}
	if (fact.CallControlID != "" && fact.CallControlID != controlID) ||
		fact.CallLegID != legID || fact.CallSessionID != sessionID {
		return "", false, ErrConflict
	}
	return practiceID, bridged, nil
}
