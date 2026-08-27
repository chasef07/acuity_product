package humancalling

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (m *Module) applyStaffTransferTargetFact(
	ctx context.Context,
	fact ProviderFact,
) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || state.Role != "STAFF" || state.Kind != staffTransferTargetKind ||
		fact.CallControlID == "" || fact.CallLegID == "" || fact.CallSessionID == "" {
		return ErrConflict
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin staff transfer target projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	transfer, targetState, storedControl, storedLeg, storedSession, err :=
		m.lockStaffTransferTarget(ctx, tx, state.CallID, state.CallLegID)
	if err != nil {
		return err
	}
	if (storedControl != "" && storedControl != fact.CallControlID) ||
		(storedLeg != "" && storedLeg != fact.CallLegID) ||
		(storedSession != "" && storedSession != fact.CallSessionID) {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET provider_connection_id = COALESCE(provider_connection_id, NULLIF($2, '')),
			provider_call_control_id = COALESCE(provider_call_control_id, $3),
			provider_call_leg_id = COALESCE(provider_call_leg_id, $4),
			provider_call_session_id = COALESCE(provider_call_session_id, $5),
			state = CASE WHEN state IN ('PENDING', 'DIALING') THEN 'RINGING' ELSE state END,
			updated_at = $6
		WHERE id = $1
	`, state.CallLegID, fact.ConnectionID, fact.CallControlID, fact.CallLegID,
		fact.CallSessionID, m.now()); err != nil {
		return fmt.Errorf("bind transfer target provider identity: %w", err)
	}

	terminal := transfer.State != StaffTransferRequested &&
		transfer.State != StaffTransferAccepted
	if terminal || targetState == "ENDING" || targetState == "ENDED" ||
		targetState == "FAILED" {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_commands
			SET state = 'RECONCILED', sent_at = COALESCE(sent_at, $2),
				last_error_code = NULL, updated_at = $3
			WHERE id = $1 AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
		`, transfer.ProviderCommandID, fact.OccurredAt, m.now()); err != nil {
			return fmt.Errorf("reconcile terminal transfer effect: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_call_legs
			SET state = CASE WHEN state IN ('ENDED', 'FAILED') THEN state ELSE 'ENDING' END,
				ending_at = COALESCE(ending_at, $2), updated_at = $2
			WHERE id = $1
		`, state.CallLegID, m.now()); err != nil {
			return err
		}
		if _, err := m.insertCallLegCommand(
			ctx, tx, transfer.CallID, state.CallLegID, "", transfer.RecipientSubject,
			CommandHangupLeg, fact.CallControlID,
			map[string]any{"client_state": encodeCallLegClientState(
				transfer.CallID, state.CallLegID, "STAFF", "transfer_terminal_cleanup",
			)}, "",
		); err != nil {
			return err
		}
	} else if fact.Type == FactCallAnswered {
		eligible, err := m.transferTargetStillEligible(ctx, tx, transfer)
		if err != nil {
			return err
		}
		if !eligible || !fact.OccurredAt.Before(transfer.ExpiresAt) {
			if err := m.failStaffTransferTx(
				ctx, tx, transfer.ID, StaffTransferFailed,
				"TRANSFER_TARGET_NOT_READY", false,
			); err != nil {
				return err
			}
		} else {
			var recipientSession string
			if err := tx.QueryRow(ctx, `
				SELECT recipient_session_id
				FROM human_calling_staff_transfers
				WHERE id = $1
			`, transfer.ID).Scan(&recipientSession); err != nil {
				return fmt.Errorf("read transfer recipient session: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_call_legs
				SET state = 'ANSWERED', answered_at = COALESCE(answered_at, $2),
					updated_at = $3
				WHERE id = $1 AND state IN ('PENDING', 'DIALING', 'RINGING', 'ANSWERED')
			`, transfer.TargetCallLegID, fact.OccurredAt, m.now()); err != nil {
				return fmt.Errorf("accept transfer target occupancy: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_staff_transfers
				SET state = 'ACCEPTED', target_answered_at = COALESCE(target_answered_at, $2),
					updated_at = $3
				WHERE id = $1 AND state IN ('REQUESTED', 'ACCEPTED')
			`, transfer.ID, fact.OccurredAt, m.now()); err != nil {
				return fmt.Errorf("record transfer target answer: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_softphone_leases
				SET desired_available = false, version = version + 1, updated_at = $3
				WHERE user_subject = $1 AND session_id = $2
			`, transfer.RecipientSubject, recipientSession,
				m.now()); err != nil {
				return fmt.Errorf("reserve transfer target softphone: %w", err)
			}
		}
	}
	if fact.Type == FactCallBridged && !terminal {
		if err := m.recordStaffTransferBridge(ctx, tx, transfer.ID, fact.OccurredAt); err != nil {
			return err
		}
	}
	if !terminal {
		if _, err := m.completeStaffTransferIfReady(ctx, tx, transfer.ID); err != nil {
			return err
		}
	}
	if err := m.advanceStaffTransferProjection(
		ctx, tx, transfer.CallID, transfer.PracticeID, fact,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *Module) applyStaffTransferBridge(
	ctx context.Context,
	fact ProviderFact,
) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || (state.Kind != staffTransferSourceKind &&
		state.Kind != staffTransferPeerKind) {
		return ErrConflict
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin staff transfer bridge projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var transferID, callID, practiceID string
	err = tx.QueryRow(ctx, `
		SELECT transfer.id::text, transfer.call_id::text, transfer.practice_id::text
		FROM human_calling_staff_transfers transfer
		WHERE transfer.call_id = $1
			AND transfer.state IN ('REQUESTED', 'ACCEPTED')
			AND ($2 = transfer.customer_leg_id OR $2 = transfer.target_staff_leg_id)
		FOR UPDATE
	`, state.CallID, state.CallLegID).Scan(&transferID, &callID, &practiceID)
	if err != nil {
		return errRelatedFactPending
	}
	if err := m.recordStaffTransferBridge(ctx, tx, transferID, fact.OccurredAt); err != nil {
		return err
	}
	if _, err := m.completeStaffTransferIfReady(ctx, tx, transferID); err != nil {
		return err
	}
	if err := m.advanceStaffTransferProjection(ctx, tx, callID, practiceID, fact); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *Module) recordStaffTransferBridge(
	ctx context.Context,
	tx pgx.Tx,
	transferID string,
	occurredAt time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_staff_transfers
		SET bridge_observed_at = COALESCE(bridge_observed_at, $2), updated_at = $3
		WHERE id = $1 AND state IN ('REQUESTED', 'ACCEPTED')
	`, transferID, occurredAt, m.now()); err != nil {
		return fmt.Errorf("record transfer bridge evidence: %w", err)
	}
	return nil
}

func (m *Module) completeStaffTransferIfReady(
	ctx context.Context,
	tx pgx.Tx,
	transferID string,
) (bool, error) {
	var transfer StaffTransfer
	var sourceState, sourceControl, targetState string
	var targetAnsweredAt, bridgeObservedAt *time.Time
	err := tx.QueryRow(ctx, `
		SELECT transfer.id::text, transfer.call_id::text,
			transfer.practice_id::text, transfer.location_id::text,
			transfer.source_staff_leg_id::text, transfer.target_staff_leg_id::text,
			transfer.customer_leg_id::text, transfer.provider_command_id::text,
			transfer.requested_by_subject, transfer.recipient_subject,
			transfer.state, transfer.target_answered_at, transfer.bridge_observed_at,
			source.state, COALESCE(source.provider_call_control_id, ''), target.state
		FROM human_calling_staff_transfers transfer
		JOIN human_calling_call_legs source ON source.id = transfer.source_staff_leg_id
		JOIN human_calling_call_legs target ON target.id = transfer.target_staff_leg_id
		WHERE transfer.id = $1
		FOR UPDATE OF transfer, source, target
	`, transferID).Scan(
		&transfer.ID, &transfer.CallID, &transfer.PracticeID, &transfer.LocationID,
		&transfer.SourceCallLegID, &transfer.TargetCallLegID,
		&transfer.CustomerCallLegID, &transfer.ProviderCommandID,
		&transfer.RequestedBySubject, &transfer.RecipientSubject, &transfer.State,
		&targetAnsweredAt, &bridgeObservedAt, &sourceState, &sourceControl, &targetState,
	)
	if err != nil {
		return false, fmt.Errorf("lock transfer completion: %w", err)
	}
	if transfer.State == StaffTransferCompleted {
		return false, nil
	}
	if transfer.State != StaffTransferAccepted || targetAnsweredAt == nil ||
		bridgeObservedAt == nil || targetState != "ANSWERED" ||
		(sourceState != "BRIDGED" && sourceState != "ENDING" && sourceState != "ENDED") {
		return false, nil
	}
	completedAt := *bridgeObservedAt
	if targetAnsweredAt.After(completedAt) {
		completedAt = *targetAnsweredAt
	}
	// The transfer's answer + bridge evidence proves the old Staff leg is no
	// longer the Call owner. Terminalizing it here releases occupancy while the
	// exact provider Hangup remains durable cleanup.
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET state = 'ENDED', ending_at = COALESCE(ending_at, $2),
			ended_at = COALESCE(ended_at, $2), error_code = 'TRANSFERRED',
			updated_at = $3
		WHERE id = $1 AND state IN ('BRIDGED', 'ENDING')
	`, transfer.SourceCallLegID, completedAt, m.now()); err != nil {
		return false, fmt.Errorf("terminalize transferred source CallLeg: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET state = 'BRIDGED', bridge_pending_at = COALESCE(bridge_pending_at, $2),
			bridged_at = COALESCE(bridged_at, $3), updated_at = $4
		WHERE id = $1 AND state = 'ANSWERED'
	`, transfer.TargetCallLegID, *targetAnsweredAt, *bridgeObservedAt, m.now()); err != nil {
		return false, fmt.Errorf("promote transfer target owner: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_staff_transfers
		SET state = 'COMPLETED', completed_at = $2, updated_at = $3
		WHERE id = $1 AND state = 'ACCEPTED'
	`, transfer.ID, completedAt, m.now()); err != nil {
		return false, fmt.Errorf("complete staff transfer: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'RECONCILED', sent_at = COALESCE(sent_at, $2),
			last_error_code = NULL, updated_at = $3
		WHERE id = $1 AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
	`, transfer.ProviderCommandID, completedAt, m.now()); err != nil {
		return false, fmt.Errorf("reconcile completed transfer command: %w", err)
	}
	if sourceControl != "" {
		if _, err := m.insertCallLegCommand(
			ctx, tx, transfer.CallID, transfer.SourceCallLegID, "",
			transfer.RequestedBySubject, CommandHangupLeg, sourceControl,
			map[string]any{"client_state": encodeCallLegClientState(
				transfer.CallID, transfer.SourceCallLegID, "STAFF", "transferred_cleanup",
			)}, "",
		); err != nil {
			return false, err
		}
	}
	if err := appendTimeline(ctx, tx, transfer.CallID, transfer.PracticeID,
		"staff_transfer.completed", transfer.RecipientSubject, "",
		transfer.ProviderCommandID, opaqueReference(transfer.ID), "", completedAt); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Module) failStaffTransferTx(
	ctx context.Context,
	tx pgx.Tx,
	transferID string,
	terminalState StaffTransferState,
	errorCode string,
	restoreAvailability bool,
) error {
	var transfer StaffTransfer
	var sourceState, targetState, targetControl, recipientSession, commandState string
	var targetAnsweredAt *time.Time
	err := tx.QueryRow(ctx, `
		SELECT transfer.id::text, transfer.call_id::text,
			transfer.practice_id::text, transfer.location_id::text,
			transfer.source_staff_leg_id::text, transfer.target_staff_leg_id::text,
			transfer.customer_leg_id::text, transfer.provider_command_id::text,
			transfer.requested_by_subject, transfer.recipient_subject, transfer.state,
			transfer.target_answered_at, transfer.recipient_session_id,
			source.state, target.state, COALESCE(target.provider_call_control_id, ''),
			command.state
		FROM human_calling_staff_transfers transfer
		JOIN human_calling_call_legs source ON source.id = transfer.source_staff_leg_id
		JOIN human_calling_call_legs target ON target.id = transfer.target_staff_leg_id
		JOIN human_calling_provider_commands command ON command.id = transfer.provider_command_id
		WHERE transfer.id = $1
		FOR UPDATE OF transfer, source, target
	`, transferID).Scan(
		&transfer.ID, &transfer.CallID, &transfer.PracticeID, &transfer.LocationID,
		&transfer.SourceCallLegID, &transfer.TargetCallLegID,
		&transfer.CustomerCallLegID, &transfer.ProviderCommandID,
		&transfer.RequestedBySubject, &transfer.RecipientSubject, &transfer.State,
		&targetAnsweredAt, &recipientSession, &sourceState, &targetState, &targetControl,
		&commandState,
	)
	if err != nil {
		return fmt.Errorf("lock failed staff transfer: %w", err)
	}
	if transfer.State != StaffTransferRequested && transfer.State != StaffTransferAccepted {
		return nil
	}
	now := m.now()
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_staff_transfers
		SET state = $2, failure_code = NULLIF($3, ''), updated_at = $4
		WHERE id = $1 AND state IN ('REQUESTED', 'ACCEPTED')
	`, transfer.ID, terminalState, errorCode, now); err != nil {
		return fmt.Errorf("fail staff transfer: %w", err)
	}
	settledCommandState := "FAILED"
	if targetControl != "" {
		settledCommandState = "RECONCILED"
	} else if commandState == "SENDING" || commandState == "SENT" ||
		commandState == "AMBIGUOUS" {
		settledCommandState = "AMBIGUOUS"
	}
	if err := tx.QueryRow(ctx, `
		UPDATE human_calling_provider_commands
		SET state = $2, last_error_code = NULLIF($3, ''), updated_at = $4
		WHERE id = $1 AND state <> 'RECONCILED'
		RETURNING state
	`, transfer.ProviderCommandID, settledCommandState, errorCode, now).Scan(&commandState); err != nil &&
		!errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("settle failed transfer command: %w", err)
	}
	uncertainTarget := targetControl == "" &&
		(targetState == "DIALING" || targetState == "RINGING" ||
			targetState == "ANSWERED" || targetAnsweredAt != nil)
	targetSettledAt := now
	if targetAnsweredAt != nil && targetAnsweredAt.After(targetSettledAt) {
		targetSettledAt = *targetAnsweredAt
	}
	if targetControl != "" || uncertainTarget {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_call_legs
			SET state = CASE WHEN state IN ('ENDED', 'FAILED') THEN state ELSE 'ENDING' END,
				ending_at = COALESCE(ending_at, $2), error_code = COALESCE(error_code, $3),
				updated_at = $2
			WHERE id = $1
		`, transfer.TargetCallLegID, targetSettledAt, errorCode); err != nil {
			return err
		}
		if targetControl != "" {
			if _, err := m.insertCallLegCommand(
				ctx, tx, transfer.CallID, transfer.TargetCallLegID, "",
				transfer.RecipientSubject, CommandHangupLeg, targetControl,
				map[string]any{"client_state": encodeCallLegClientState(
					transfer.CallID, transfer.TargetCallLegID, "STAFF", "transfer_failed_cleanup",
				)}, "",
			); err != nil {
				return err
			}
		}
	} else if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET state = 'FAILED', ending_at = COALESCE(ending_at, $2),
			ended_at = COALESCE(ended_at, $2), error_code = $3, updated_at = $2
		WHERE id = $1 AND state NOT IN ('ENDED', 'FAILED')
	`, transfer.TargetCallLegID, targetSettledAt, errorCode); err != nil {
		return err
	}
	if restoreAvailability {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_softphone_leases
			SET desired_available = true, version = version + 1, updated_at = $3
			WHERE user_subject = $1 AND session_id = $2
				AND registered AND microphone_ready AND audio_ready AND session_healthy
				AND lease_expires_at > $3
		`, transfer.RecipientSubject, recipientSession, now); err != nil {
			return fmt.Errorf("restore transfer target availability: %w", err)
		}
	}
	if sourceState == "ENDING" || sourceState == "ENDED" || sourceState == "FAILED" {
		if err := m.terminateCallAfterFailedTransfer(ctx, tx, transfer.CallID, now); err != nil {
			return err
		}
	}
	if err := appendTimeline(ctx, tx, transfer.CallID, transfer.PracticeID,
		"staff_transfer."+stringsForTimeline(terminalState), "", "",
		transfer.ProviderCommandID, opaqueReference(transfer.ID), errorCode, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1, updated_at = $2
		WHERE id = $1
	`, transfer.CallID, now); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, transfer.PracticeID); err != nil {
		return err
	}
	return nil
}

func stringsForTimeline(state StaffTransferState) string {
	switch state {
	case StaffTransferCanceled:
		return "canceled"
	case StaffTransferExpired:
		return "expired"
	case StaffTransferDeclined:
		return "declined"
	default:
		return "failed"
	}
}

func (m *Module) terminateCallAfterFailedTransfer(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	endedAt time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET terminal_outcome = COALESCE(terminal_outcome, 'ENDED'),
			ended_at = COALESCE(ended_at, $2),
			disposition_deadline = COALESCE(disposition_deadline, $3),
			updated_at = $2
		WHERE id = $1
	`, callID, endedAt, endedAt.Add(m.config.DispositionDuration)); err != nil {
		return fmt.Errorf("end Call after failed transfer with absent source: %w", err)
	}
	return m.endConnectedCallLegs(ctx, tx, callID)
}

func (m *Module) lockStaffTransferTarget(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	targetLegID string,
) (StaffTransfer, string, string, string, string, error) {
	var transferID, targetState, controlID, providerLegID, sessionID string
	if err := tx.QueryRow(ctx, `
		SELECT transfer.id::text, target.state,
			COALESCE(target.provider_call_control_id, ''),
			COALESCE(target.provider_call_leg_id, ''),
			COALESCE(target.provider_call_session_id, '')
		FROM human_calling_staff_transfers transfer
		JOIN human_calling_calls call ON call.id = transfer.call_id
		JOIN human_calling_call_legs target ON target.id = transfer.target_staff_leg_id
		WHERE transfer.call_id = $1 AND transfer.target_staff_leg_id = $2
		FOR UPDATE OF call, transfer, target
	`, callID, targetLegID).Scan(
		&transferID, &targetState, &controlID, &providerLegID, &sessionID,
	); err != nil {
		return StaffTransfer{}, "", "", "", "", errRelatedFactPending
	}
	transfer, err := readStaffTransfer(ctx, tx, transferID)
	return transfer, targetState, controlID, providerLegID, sessionID, err
}

func (m *Module) transferTargetStillEligible(
	ctx context.Context,
	tx pgx.Tx,
	transfer StaffTransfer,
) (bool, error) {
	var eligible bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM human_calling_staff_transfers current
			JOIN human_calling_softphone_leases lease
				ON lease.user_subject = current.recipient_subject
			JOIN access_memberships membership
				ON membership.practice_id = current.practice_id
				AND membership.user_subject = current.recipient_subject
			WHERE current.id = $1 AND current.state IN ('REQUESTED', 'ACCEPTED')
				AND lease.session_id = current.recipient_session_id
				AND lease.registered
				AND lease.microphone_ready AND lease.audio_ready AND lease.session_healthy
				AND lease.lease_expires_at > $2
				AND membership.role = 'STAFF' AND membership.revoked_at IS NULL
				AND (membership.location_scope = 'ALL' OR EXISTS (
					SELECT 1 FROM access_membership_locations allowed
					WHERE allowed.membership_id = membership.id
						AND allowed.location_id = current.location_id
				))
				AND NOT EXISTS (
					SELECT 1 FROM human_calling_call_legs occupied
					WHERE occupied.staff_subject = current.recipient_subject
						AND occupied.id <> current.target_staff_leg_id
						AND (occupied.state IN ('ANSWERED', 'BRIDGE_PENDING', 'BRIDGED')
							OR (occupied.state = 'ENDING' AND occupied.answered_at IS NOT NULL))
				)
		)
	`, transfer.ID, m.now()).Scan(&eligible); err != nil {
		return false, fmt.Errorf("revalidate transfer target: %w", err)
	}
	return eligible, nil
}

func (m *Module) advanceStaffTransferProjection(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	practiceID string,
	fact ProviderFact,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1, updated_at = $2
		WHERE id = $1
	`, callID, m.now()); err != nil {
		return err
	}
	kind := "staff_transfer.provider." + string(fact.Type)
	if err := appendTimeline(ctx, tx, callID, practiceID, kind, "", fact.EventID,
		"", opaqueReference(fact.CallLegID), "", fact.OccurredAt); err != nil {
		return err
	}
	_, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID)
	return err
}

func (m *Module) handleStaffTransferHangupTx(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	legID string,
	role string,
	occurredAt time.Time,
) (bool, error) {
	if role != "STAFF" {
		return false, nil
	}
	var transferID, state, sourceLegID, targetLegID string
	err := tx.QueryRow(ctx, `
		SELECT id::text, state, source_staff_leg_id::text, target_staff_leg_id::text
		FROM human_calling_staff_transfers
		WHERE call_id = $1
			AND ($2 = source_staff_leg_id OR $2 = target_staff_leg_id)
		ORDER BY created_at DESC, id DESC
		LIMIT 1
		FOR UPDATE
	`, callID, legID).Scan(&transferID, &state, &sourceLegID, &targetLegID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read transfer Hangup role: %w", err)
	}
	if legID == targetLegID && state == string(StaffTransferCompleted) {
		return false, nil
	}
	if legID == sourceLegID && state == string(StaffTransferCompleted) {
		// Provider cleanup for the old source cannot end the transferred Call.
		return true, nil
	}
	if state != string(StaffTransferRequested) && state != string(StaffTransferAccepted) {
		return true, nil
	}
	if legID == sourceLegID {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_staff_transfers
			SET source_ended_at = COALESCE(source_ended_at, $2), updated_at = $3
			WHERE id = $1
		`, transferID, occurredAt, m.now()); err != nil {
			return false, fmt.Errorf("record source Hangup during transfer: %w", err)
		}
		return true, nil
	}
	if err := m.failStaffTransferTx(
		ctx, tx, transferID, StaffTransferFailed, "TRANSFER_TARGET_HANGUP", true,
	); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Module) failRecipientTransfersForReadinessLoss(
	ctx context.Context,
	tx pgx.Tx,
	recipientSubject string,
	recipientSession string,
) error {
	rows, err := tx.Query(ctx, `
		SELECT transfer.id::text
		FROM human_calling_staff_transfers transfer
		WHERE transfer.recipient_subject = $1
			AND transfer.recipient_session_id = $2
			AND transfer.state IN ('REQUESTED', 'ACCEPTED')
		ORDER BY transfer.created_at, transfer.id
		FOR UPDATE
	`, recipientSubject, recipientSession)
	if err != nil {
		return fmt.Errorf("lock transfers after readiness loss: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		if err := m.failStaffTransferTx(
			ctx, tx, id, StaffTransferFailed, "TRANSFER_TARGET_NOT_READY", false,
		); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) lockActiveRecipientTransfers(
	ctx context.Context,
	tx pgx.Tx,
	recipientSubject string,
) error {
	rows, err := tx.Query(ctx, `
		SELECT id
		FROM human_calling_staff_transfers
		WHERE recipient_subject = $1 AND state IN ('REQUESTED', 'ACCEPTED')
		ORDER BY created_at, id
		FOR UPDATE
	`, recipientSubject)
	if err != nil {
		return fmt.Errorf("lock active recipient transfers: %w", err)
	}
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate active recipient transfers: %w", err)
	}
	rows.Close()
	return nil
}

func (m *Module) failCallTransfersTx(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	errorCode string,
) error {
	rows, err := tx.Query(ctx, `
		SELECT id::text FROM human_calling_staff_transfers
		WHERE call_id = $1 AND state IN ('REQUESTED', 'ACCEPTED')
		ORDER BY created_at, id FOR UPDATE
	`, callID)
	if err != nil {
		return fmt.Errorf("lock terminal Call transfers: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		if err := m.failStaffTransferTx(
			ctx, tx, id, StaffTransferCanceled, errorCode, true,
		); err != nil {
			return err
		}
	}
	return nil
}
