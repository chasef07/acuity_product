package humancalling

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

const terminalNeverStartedErrorCode = "CALL_TERMINATED_BEFORE_PROVIDER_START"

const providerObservationConflictErrorCode = "PROVIDER_OBSERVATION_CONFLICT"

const terminalNeverStartedCallLegQuery = `
	SELECT call.id::text, call.practice_id::text, leg.id::text
	FROM human_calling_calls call
	JOIN human_calling_call_legs leg ON leg.call_id = call.id
	WHERE call.terminal_outcome IS NOT NULL
		AND leg.state = 'PENDING'
		AND leg.provider_connection_id IS NULL
		AND leg.provider_call_control_id IS NULL
		AND leg.provider_call_leg_id IS NULL
		AND leg.provider_call_session_id IS NULL
		AND NOT EXISTS (
			SELECT 1
			FROM human_calling_provider_commands active
			WHERE active.call_leg_id = leg.id
				AND active.state IN ('SENDING', 'SENT', 'AMBIGUOUS')
		)
		AND leg.updated_at <= $1::timestamptz - interval '60 seconds'
	ORDER BY leg.updated_at, leg.id
	FOR UPDATE OF call, leg SKIP LOCKED
	LIMIT 1
`

const staleCallLegCandidateQuery = `
	SELECT call.id::text, call.practice_id::text, leg.id::text,
		leg.role, call.direction, leg.state,
		COALESCE(call.terminal_outcome, ''),
		COALESCE(leg.provider_connection_id, command.payload->>'connection_id', ''),
		COALESCE(leg.provider_call_control_id, ''),
		COALESCE(leg.provider_call_leg_id, ''),
		COALESCE(leg.provider_call_session_id, ''),
		COALESCE(command.id::text, ''), COALESCE(command.action, ''),
		COALESCE(command.payload->>'client_state', ''), leg.updated_at,
		COALESCE(command.created_at, leg.updated_at),
		COALESCE(call.ended_at, leg.ended_at, leg.updated_at)
	FROM human_calling_calls call
	JOIN human_calling_call_legs leg ON leg.call_id = call.id
	LEFT JOIN LATERAL (
		SELECT pending.id, pending.action, pending.payload, pending.created_at
		FROM (
			SELECT active.id, active.action, active.target_id,
				active.payload, active.created_at
			FROM human_calling_provider_commands active
			WHERE active.call_leg_id = leg.id
				AND active.state IN ('SENDING', 'SENT', 'AMBIGUOUS')
			UNION ALL
			SELECT failed.id, failed.action, failed.target_id,
				failed.payload, failed.created_at
			FROM human_calling_timeline degraded
			JOIN human_calling_provider_commands failed
				ON failed.id = degraded.provider_command_id
			WHERE degraded.call_id = call.id
				AND degraded.kind = 'caller_audio.degraded'
				AND failed.call_leg_id = leg.id
				AND failed.action = 'STOP_RING_WINDOW'
				AND failed.state = 'FAILED'
				AND NOT EXISTS (
					SELECT 1 FROM human_calling_timeline converged
					WHERE converged.call_id = call.id
						AND converged.kind = 'caller_audio.converged'
						AND converged.provider_command_id = failed.id
				)
		) pending
		WHERE (
			call.terminal_outcome IS NULL
			OR pending.action = 'HANGUP_LEG'
			OR (
				pending.action = 'STOP_RING_WINDOW'
				AND leg.role = 'CALLER'
				AND leg.state IN ('ENDED', 'FAILED')
				AND leg.provider_connection_id IS NOT NULL
				AND leg.provider_call_control_id IS NOT NULL
				AND leg.provider_call_leg_id IS NOT NULL
				AND leg.provider_call_session_id IS NOT NULL
				AND pending.target_id = leg.provider_call_control_id
				AND pending.payload->>'stop' = 'all'
				AND COALESCE(pending.payload->>'client_state', '') <> ''
			)
		)
		ORDER BY pending.created_at, pending.id
		LIMIT 1
	) command ON true
	WHERE (
			call.terminal_outcome IS NULL
			OR leg.state = 'ENDING'
			OR command.id IS NOT NULL
		)
		AND (leg.state NOT IN ('ENDED', 'FAILED') OR command.id IS NOT NULL)
		AND (
			(leg.provider_call_control_id IS NOT NULL AND leg.provider_call_leg_id IS NOT NULL)
			OR command.id IS NOT NULL
		)
		AND leg.updated_at <= $1::timestamptz - interval '60 seconds'
	ORDER BY leg.updated_at, leg.id
	FOR UPDATE OF call, leg SKIP LOCKED
	LIMIT 1
`

func (m *Module) ProcessNextCommand(ctx context.Context) (bool, error) {
	command, ok, err := m.ClaimNextCommand(ctx)
	if err != nil || !ok {
		return ok, err
	}
	return true, command(ctx)
}

// ClaimNextCommand commits ownership before returning the provider effect.
// The returned effect must be invoked at most once and always outside a
// database transaction.
func (m *Module) ClaimNextCommand(
	ctx context.Context,
) (func(context.Context) error, bool, error) {
	commandID, ok, err := m.claimNextCallLegCommand(ctx)
	if err != nil || !ok {
		return nil, ok, err
	}
	return func(executeContext context.Context) error {
		_, executeErr := m.executeCallLegCommand(executeContext, commandID)
		return executeErr
	}, true, nil
}

func (m *Module) RecoverInterruptedCommands(ctx context.Context) error {
	now := m.now()
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin interrupted provider command recovery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT call.id::text
		FROM human_calling_calls call
		WHERE EXISTS (
			SELECT 1 FROM human_calling_provider_commands command
			WHERE command.call_id = call.id AND command.state = 'SENDING'
				AND command.updated_at <= $1::timestamptz - interval '30 seconds'
		)
		ORDER BY call.id
		FOR UPDATE OF call
	`, now)
	if err != nil {
		return fmt.Errorf("lock Calls for interrupted provider command recovery: %w", err)
	}
	for rows.Next() {
		var callID string
		if err := rows.Scan(&callID); err != nil {
			rows.Close()
			return fmt.Errorf("scan interrupted provider command Call: %w", err)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate interrupted provider command Calls: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands command
		SET state = CASE
				WHEN command.action IN (
					'DIAL_OUTBOUND_STAFF', 'DIAL_OUTBOUND_DESTINATION', 'BRIDGE'
				) AND EXISTS (
					SELECT 1
					FROM human_calling_calls call
					JOIN human_calling_timeline timeline ON timeline.call_id = call.id
					WHERE call.id = command.call_id AND call.direction = 'OUTBOUND'
						AND timeline.kind = 'call.hangup.requested'
				) THEN 'AMBIGUOUS'
				WHEN created_at > $1::timestamptz - interval '55 seconds' THEN 'PENDING'
				ELSE 'AMBIGUOUS'
			END,
			last_error_code = CASE
				WHEN command.action IN (
					'DIAL_OUTBOUND_STAFF', 'DIAL_OUTBOUND_DESTINATION', 'BRIDGE'
				) AND EXISTS (
					SELECT 1
					FROM human_calling_calls call
					JOIN human_calling_timeline timeline ON timeline.call_id = call.id
					WHERE call.id = command.call_id AND call.direction = 'OUTBOUND'
						AND timeline.kind = 'call.hangup.requested'
				) THEN 'PROVIDER_EFFECT_UNCERTAIN'
				WHEN created_at > $1::timestamptz - interval '55 seconds' THEN 'WORKER_INTERRUPTED'
				ELSE 'PROVIDER_EFFECT_UNCERTAIN'
			END,
			next_attempt_at = $1, updated_at = $1
		WHERE state = 'SENDING' AND updated_at <= $1::timestamptz - interval '30 seconds'
	`, now); err != nil {
		return fmt.Errorf("recover interrupted provider commands: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit interrupted provider command recovery: %w", err)
	}
	return nil
}

func (m *Module) reconcileNeverStartedTerminalCallLeg(
	ctx context.Context,
) (bool, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin terminal never-started CallLeg cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var callID, practiceID, legID string
	if err := tx.QueryRow(ctx, terminalNeverStartedCallLegQuery, m.now()).Scan(
		&callID, &practiceID, &legID,
	); errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	} else if err != nil {
		return false, fmt.Errorf("claim terminal never-started CallLeg: %w", err)
	}

	cleanedAt := m.now()
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET state = 'FAILED', ending_at = COALESCE(ending_at, $2),
			ended_at = COALESCE(ended_at, $2), error_code = $3, updated_at = $2
		WHERE id = $1
	`, legID, cleanedAt, terminalNeverStartedErrorCode); err != nil {
		return false, fmt.Errorf("fail terminal never-started CallLeg: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'FAILED', last_error_code = $2, updated_at = $3
		WHERE call_leg_id = $1 AND state = 'PENDING'
	`, legID, terminalNeverStartedErrorCode, cleanedAt); err != nil {
		return false, fmt.Errorf("cancel terminal never-started CallLeg commands: %w", err)
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"call_leg.failed",
		"",
		"",
		"",
		opaqueReference(legID),
		terminalNeverStartedErrorCode,
		cleanedAt,
	); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET version = version + 1, updated_at = $2
		WHERE id = $1
	`, callID, cleanedAt); err != nil {
		return false, fmt.Errorf("advance terminal Call cleanup version: %w", err)
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit terminal never-started CallLeg cleanup: %w", err)
	}
	return true, nil
}

func (m *Module) ReconcileStaleCalls(ctx context.Context) (int, error) {
	expired, err := m.expireUnconfirmedOutboundMedia(ctx)
	if err != nil {
		return 0, err
	}
	if expired {
		return 1, nil
	}
	cleaned, err := m.reconcileNeverStartedTerminalCallLeg(ctx)
	if err != nil {
		return 0, err
	}
	if cleaned {
		return 1, nil
	}
	provider, ok := m.provider.(CallStateProvider)
	if !ok {
		return 0, nil
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin stale CallLeg reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var callID, practiceID, legID, role, direction, legState, terminalOutcome string
	var connectionID string
	var controlID, providerLegID, sessionID string
	var commandID, providerClientState string
	var commandAction CommandAction
	var observationSince, commandCreatedAt, terminalAt time.Time
	err = tx.QueryRow(ctx, staleCallLegCandidateQuery, m.now()).Scan(
		&callID, &practiceID, &legID, &role, &direction, &legState,
		&terminalOutcome, &connectionID,
		&controlID, &providerLegID, &sessionID, &commandID, &commandAction,
		&providerClientState, &observationSince, &commandCreatedAt, &terminalAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, tx.Commit(ctx)
	}
	if err != nil {
		return 0, fmt.Errorf("claim stale CallLeg: %w", err)
	}
	checkedAt := m.now()
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1
	`, legID, checkedAt); err != nil {
		return 0, fmt.Errorf("mark stale CallLeg check: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit stale CallLeg claim: %w", err)
	}
	if commandAction == CommandStopRingWindow && terminalOutcome != "" &&
		(legState == "ENDED" || legState == "FAILED") {
		state, valid := parseCallLegClientState(providerClientState)
		if !valid || state.CallID != callID || state.CallLegID != legID ||
			state.Role != "CALLER" || state.Kind != "ring_window" {
			return 0, nil
		}
		terminalized, err := m.terminalizeStopRingWindow(
			ctx,
			commandID,
			callID,
			practiceID,
			legID,
			commandCreatedAt,
			terminalAt,
			providerClientState,
		)
		if err != nil {
			return 1, err
		}
		if terminalized {
			return 1, nil
		}
		return 0, nil
	}
	clientState := encodeCallLegClientState(callID, legID, role, "reconciled")
	if providerClientState == "" {
		providerClientState = clientState
	}
	observation, err := provider.ObserveCall(
		ctx,
		connectionID,
		controlID,
		providerLegID,
		providerClientState,
		observationSince.Add(-time.Second),
	)
	if err != nil {
		return 1, err
	}
	if observation.CallControlID != "" {
		controlID = observation.CallControlID
	}
	if observation.CallLegID != "" {
		providerLegID = observation.CallLegID
	}
	if observation.CallSessionID != "" {
		sessionID = observation.CallSessionID
	}
	sort.SliceStable(observation.Events, func(left, right int) bool {
		return observation.Events[left].OccurredAt.Before(observation.Events[right].OccurredAt)
	})
	observedInitiation := false
	observedHangup := false
	for _, event := range observation.Events {
		if event.Type == FactCallInitiated && role == "CALLER" {
			continue
		}
		if event.Type == FactCallInitiated {
			observedInitiation = true
		}
		if event.Type == FactCallHangup {
			observedHangup = true
		}
		eventSessionID := event.CallSessionID
		if eventSessionID == "" {
			eventSessionID = sessionID
		}
		factClientState := event.ClientState
		if factClientState == "" {
			factClientState = clientState
		}
		if event.Type == FactCallBridged &&
			event.ClientState == "" &&
			(role == "DESTINATION" ||
				(role == "STAFF" && direction == string(CallInbound))) {
			factClientState = encodeCallLegClientState(callID, legID, role, "bridge")
		}
		fact := event
		if fact.ConnectionID == "" {
			fact.ConnectionID = connectionID
		}
		if fact.CallControlID == "" {
			fact.CallControlID = controlID
		}
		fact.CallSessionID = eventSessionID
		fact.ClientState = factClientState
		if fact.Type == FactCallHangup && fact.HangupCause == "" {
			fact.HangupCause = "PROVIDER_RECONCILED"
			fact.TerminationSource = "RECONCILER"
		}
		if err := m.ApplyProviderFact(ctx, fact); err != nil {
			if errors.Is(err, ErrConflict) {
				if quarantineErr := m.quarantineStaleCallLeg(
					ctx, callID, practiceID, legID, commandID,
				); quarantineErr != nil {
					return 1, quarantineErr
				}
				return 1, nil
			}
			return 1, err
		}
	}
	if commandID != "" {
		var unresolved bool
		if err := m.database.QueryRow(ctx, `
			SELECT state IN ('SENDING', 'SENT', 'AMBIGUOUS')
			FROM human_calling_provider_commands WHERE id = $1
		`, commandID).Scan(&unresolved); err != nil {
			return 1, fmt.Errorf("read observed provider command state: %w", err)
		}
		if !unresolved {
			return 1, nil
		}
	}
	if observation.Active && !observedInitiation &&
		(commandAction == CommandDialStaff ||
			commandAction == CommandDialOutboundStaff ||
			commandAction == CommandDialOutboundDestination) {
		fact := ProviderFact{
			EventID:       "reconcile-active-" + legID + "-" + fmt.Sprint(checkedAt.UnixNano()),
			Type:          FactCallInitiated,
			OccurredAt:    checkedAt,
			ConnectionID:  connectionID,
			CallControlID: controlID,
			CallLegID:     providerLegID,
			CallSessionID: sessionID,
			ClientState:   clientState,
		}
		if err := m.ApplyProviderFact(ctx, fact); err != nil {
			return 1, err
		}
		return 1, nil
	}
	if commandID == "" && observedHangup {
		return 1, nil
	}
	if !observation.Active && commandID != "" && commandAction != CommandHangupLeg {
		if err := m.rejectUnobservedCommand(
			ctx, commandID, legID, commandAction, "PROVIDER_EFFECT_ABSENT",
		); err != nil {
			return 1, err
		}
		return 1, nil
	}
	if observation.Active {
		if commandID != "" && commandAction == CommandStopRingWindow {
			if err := m.markUnobservedStopRingWindowAmbiguous(
				ctx, commandID, legID, commandCreatedAt, checkedAt,
			); err != nil {
				return 1, err
			}
			return 1, nil
		}
		if commandID != "" && commandAction == CommandStartRingWindow {
			// An active Call and an absent event do not prove a finite playback
			// never started. Preserve the effect as ambiguous until a positive
			// playback fact or terminal provider state can converge it.
			if err := m.markUnobservedCommandAmbiguous(
				ctx, commandID, commandAction, commandCreatedAt, checkedAt,
			); err != nil {
				return 1, err
			}
			return 1, nil
		}
		if commandID != "" &&
			!commandCreatedAt.After(checkedAt.Add(-safeProviderRetryWindow)) {
			if err := m.rejectUnobservedCommand(
				ctx, commandID, legID, commandAction,
				string(commandAction)+"_EVENT_ABSENT",
			); err != nil {
				return 1, err
			}
		}
		return 1, nil
	}
	if controlID == "" || providerLegID == "" {
		if commandID == "" {
			return 1, nil
		}
		if err := m.rejectUnobservedCommand(
			ctx, commandID, legID, commandAction, "PROVIDER_EFFECT_ABSENT",
		); err != nil {
			return 1, err
		}
		return 1, nil
	}
	// A bridged CallLeg needs explicit hangup evidence or committed Hangup intent.
	if legState == "BRIDGED" && commandID == "" {
		return 1, nil
	}
	fact := ProviderFact{
		EventID:           "reconcile-absent-" + legID + "-" + fmt.Sprint(checkedAt.UnixNano()),
		Type:              FactCallHangup,
		OccurredAt:        checkedAt,
		CallControlID:     controlID,
		CallLegID:         providerLegID,
		CallSessionID:     sessionID,
		ClientState:       encodeCallLegClientState(callID, legID, role, "reconciled_absent"),
		HangupCause:       "CALL_DOES_NOT_EXIST",
		TerminationSource: "RECONCILER",
	}
	if err := m.ApplyProviderFact(ctx, fact); err != nil {
		return 1, err
	}
	return 1, nil
}

func (m *Module) markUnobservedStopRingWindowAmbiguous(
	ctx context.Context,
	commandID string,
	callLegID string,
	commandCreatedAt time.Time,
	observedAt time.Time,
) error {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin unobserved Stop ring-window result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockCallThenCallLegForCommandMutation(ctx, tx, callLegID); err != nil {
		return err
	}
	errorCode := string(CommandStopRingWindow) + "_EVENT_ABSENT"
	tag, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'AMBIGUOUS', last_error_code = $2, updated_at = $3
		WHERE id = $1 AND call_leg_id = $4 AND action = 'STOP_RING_WINDOW'
			AND state IN ('SENDING', 'SENT')
	`, commandID, errorCode, observedAt, callLegID)
	if err != nil {
		return fmt.Errorf("mark unobserved Stop ring-window ambiguous: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if err := m.recordDegradedCallerAudio(
		ctx, tx, commandID, callLegID, errorCode,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit unobserved Stop ring-window result: %w", err)
	}
	m.recordProviderCommand(
		ProviderCommand{Action: CommandStopRingWindow, createdAt: commandCreatedAt},
		"AMBIGUOUS",
		observedAt,
		0,
	)
	return nil
}

func (m *Module) quarantineStaleCallLeg(
	ctx context.Context,
	callID string,
	practiceID string,
	callLegID string,
	commandID string,
) error {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin stale CallLeg quarantine: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var terminal bool
	if err := tx.QueryRow(ctx, `
		SELECT terminal_outcome IS NOT NULL
		FROM human_calling_calls
		WHERE id = $1
		FOR UPDATE
	`, callID).Scan(&terminal); err != nil {
		return fmt.Errorf("lock stale Call quarantine: %w", err)
	}
	quarantinedAt := m.now()
	if !terminal {
		if err := m.failRoutingCall(
			ctx, tx, callID, providerObservationConflictErrorCode,
		); err != nil {
			return fmt.Errorf("fail contradictory stale Call: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET state = 'FAILED',
			ending_at = COALESCE(ending_at, $2),
			ended_at = COALESCE(ended_at, $2),
			error_code = $3, updated_at = $2
		WHERE id = $1 AND call_id = $4
	`, callLegID, quarantinedAt, providerObservationConflictErrorCode, callID); err != nil {
		return fmt.Errorf("quarantine contradictory stale CallLeg: %w", err)
	}
	if commandID != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_commands
			SET state = 'FAILED', last_error_code = $2, updated_at = $3
			WHERE id = $1 AND call_leg_id = $4
				AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
		`, commandID, providerObservationConflictErrorCode, quarantinedAt, callLegID); err != nil {
			return fmt.Errorf("quarantine contradictory stale command: %w", err)
		}
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"call_leg.reconciliation_quarantined",
		"",
		"",
		commandID,
		opaqueReference(callLegID),
		providerObservationConflictErrorCode,
		quarantinedAt,
	); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit stale CallLeg quarantine: %w", err)
	}
	return nil
}

func (m *Module) terminalizeStopRingWindow(
	ctx context.Context,
	commandID string,
	callID string,
	practiceID string,
	callLegID string,
	commandCreatedAt time.Time,
	terminalAt time.Time,
	providerClientState string,
) (bool, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin terminal ring-window reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockCallThenCallLegForCommandMutation(ctx, tx, callLegID); err != nil {
		return false, err
	}
	reconciledAt := m.now()
	tag, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands command
		SET state = 'RECONCILED', last_error_code = NULL, updated_at = $2
		FROM human_calling_calls call, human_calling_call_legs caller
		WHERE command.id = $1 AND command.action = 'STOP_RING_WINDOW'
			AND command.state IN ('SENDING', 'SENT', 'AMBIGUOUS', 'FAILED')
			AND command.call_id = call.id AND call.id = $3
			AND command.call_leg_id = caller.id AND caller.id = $4
			AND call.terminal_outcome IS NOT NULL
			AND caller.role = 'CALLER' AND caller.state IN ('ENDED', 'FAILED')
			AND caller.provider_connection_id IS NOT NULL
			AND caller.provider_call_control_id IS NOT NULL
			AND caller.provider_call_leg_id IS NOT NULL
			AND caller.provider_call_session_id IS NOT NULL
			AND command.target_id = caller.provider_call_control_id
			AND command.payload->>'stop' = 'all'
			AND command.payload->>'client_state' = $5
	`, commandID, reconciledAt, callID, callLegID, providerClientState)
	if err != nil {
		return false, fmt.Errorf("terminalize ring-window command: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, tx.Commit(ctx)
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"ring_window.terminalized",
		"",
		"",
		commandID,
		opaqueReference(callLegID),
		"CALL_TERMINAL",
		terminalAt,
	); err != nil {
		return false, err
	}
	var degraded bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM human_calling_timeline
			WHERE call_id = $1 AND kind = 'caller_audio.degraded'
				AND provider_command_id = $2
		)
	`, callID, commandID).Scan(&degraded); err != nil {
		return false, fmt.Errorf("read terminal degraded caller audio: %w", err)
	}
	if degraded {
		if err := appendTimeline(
			ctx,
			tx,
			callID,
			practiceID,
			"caller_audio.converged",
			"",
			"",
			commandID,
			opaqueReference(callLegID),
			"CALL_TERMINAL",
			terminalAt,
		); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit terminal ring-window reconciliation: %w", err)
	}
	m.recordProviderCommand(
		ProviderCommand{Action: CommandStopRingWindow, createdAt: commandCreatedAt},
		"RECONCILED",
		reconciledAt,
		0,
	)
	return true, nil
}

func (m *Module) expireUnconfirmedOutboundMedia(ctx context.Context) (bool, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin outbound media expiry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var callID, practiceID, staffLegID, subject string
	err = tx.QueryRow(ctx, `
		SELECT call.id::text, call.practice_id::text, staff.id::text,
			COALESCE(staff.staff_subject, '')
		FROM human_calling_calls call
		JOIN human_calling_call_legs staff
			ON staff.call_id = call.id AND staff.role = 'STAFF'
		WHERE call.direction = 'OUTBOUND'
			AND call.terminal_outcome IS NULL
			AND staff.state = 'BRIDGE_PENDING'
			AND staff.bridge_pending_at <= $1::timestamptz - $2::interval
			AND NOT EXISTS (
				SELECT 1 FROM human_calling_call_legs destination
				WHERE destination.call_id = call.id
					AND destination.role = 'DESTINATION'
			)
		ORDER BY staff.bridge_pending_at, staff.id
		FOR UPDATE OF call, staff SKIP LOCKED
		LIMIT 1
	`, m.now(), m.config.RingWindowDuration.String()).Scan(
		&callID, &practiceID, &staffLegID, &subject,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, fmt.Errorf("claim expired outbound media: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET terminal_outcome = 'UNANSWERED',
			provider_termination = 'MEDIA_READINESS_FAILED',
			ended_at = COALESCE(ended_at, $2),
			version = version + 1, updated_at = $2
		WHERE id = $1 AND terminal_outcome IS NULL
	`, callID, m.now()); err != nil {
		return false, fmt.Errorf("end expired outbound media Call: %w", err)
	}
	if err := m.endRemainingCallLegs(ctx, tx, callID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET state = 'FAILED', ended_at = COALESCE(ended_at, $2),
			error_code = 'MEDIA_READINESS_FAILED', updated_at = $2
		WHERE id = $1
	`, staffLegID, m.now()); err != nil {
		return false, fmt.Errorf("fail expired outbound media CallLeg: %w", err)
	}
	if err := appendTimeline(ctx, tx, callID, practiceID,
		"outbound.media_readiness_timeout", subject, "", "",
		opaqueReference(staffLegID), "MEDIA_READINESS_FAILED", m.now()); err != nil {
		return false, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit outbound media expiry: %w", err)
	}
	return true, nil
}

func (m *Module) markUnobservedCommandAmbiguous(
	ctx context.Context,
	commandID string,
	action CommandAction,
	commandCreatedAt time.Time,
	observedAt time.Time,
) error {
	tag, err := m.database.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'AMBIGUOUS', last_error_code = $2, updated_at = $3
		WHERE id = $1 AND state IN ('SENDING', 'SENT')
	`, commandID, string(action)+"_EVENT_ABSENT", observedAt)
	if err != nil {
		return fmt.Errorf("mark unobserved provider command ambiguous: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	m.recordProviderCommand(
		ProviderCommand{Action: action, createdAt: commandCreatedAt},
		"AMBIGUOUS",
		observedAt,
		0,
	)
	return nil
}

func (m *Module) rejectUnobservedCommand(
	ctx context.Context,
	commandID string,
	callLegID string,
	action CommandAction,
	errorCode string,
) error {
	observedAt := m.now()
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin provider observation result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockCallThenCallLegForCommandMutation(ctx, tx, callLegID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'FAILED', last_error_code = $2, updated_at = $3
		WHERE id = $1 AND call_leg_id = $4 AND action = $5
			AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
	`, commandID, errorCode, m.now(), callLegID, action)
	if err != nil {
		return fmt.Errorf("reject unobserved provider command: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	var commandCreatedAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT created_at FROM human_calling_provider_commands WHERE id = $1
	`, commandID).Scan(&commandCreatedAt); err != nil {
		return fmt.Errorf("read rejected provider command timing: %w", err)
	}
	switch action {
	case CommandDialStaff, CommandDialOutboundStaff, CommandDialOutboundDestination:
		err = m.failDialCallLeg(ctx, tx, callLegID, action, errorCode)
	case CommandBridge:
		err = m.failBridgeCallLeg(ctx, tx, callLegID, errorCode)
	case CommandAnswerCaller,
		CommandSpeakVoicemail, CommandStartVoicemailRecording:
		var callID string
		if queryErr := tx.QueryRow(ctx, `
			SELECT call_id::text FROM human_calling_call_legs WHERE id = $1
		`, callLegID).Scan(&callID); queryErr != nil {
			return queryErr
		}
		err = m.failRoutingCall(ctx, tx, callID, errorCode)
	case CommandStartRingWindow, CommandStopRingWindow:
		err = m.recordDegradedCallerAudio(
			ctx, tx, commandID, callLegID, errorCode,
		)
	case CommandHangupLeg:
		var callID, role, subject, targetID string
		if queryErr := tx.QueryRow(ctx, `
			SELECT command.call_id::text, leg.role,
				COALESCE(leg.staff_subject, ''), command.target_id
			FROM human_calling_provider_commands command
			JOIN human_calling_call_legs leg ON leg.id = command.call_leg_id
			WHERE command.id = $1 AND leg.id = $2
		`, commandID, callLegID).Scan(
			&callID, &role, &subject, &targetID,
		); queryErr != nil {
			return queryErr
		}
		_, err = m.insertCallLegCommand(
			ctx, tx, callID, callLegID, "", subject, CommandHangupLeg, targetID,
			map[string]any{"client_state": encodeCallLegClientState(
				callID, callLegID, role, "cleanup_retry",
			)},
			"",
		)
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	m.recordProviderCommand(
		ProviderCommand{Action: action, createdAt: commandCreatedAt},
		"FAILED",
		observedAt,
		0,
	)
	return nil
}

func (m *Module) recordDegradedCallerAudio(
	ctx context.Context,
	tx pgx.Tx,
	commandID string,
	callLegID string,
	errorCode string,
) error {
	var callID, practiceID string
	if err := tx.QueryRow(ctx, `
		SELECT call.id::text, call.practice_id::text
		FROM human_calling_calls call
		JOIN human_calling_call_legs leg ON leg.call_id = call.id
		WHERE leg.id = $1
	`, callLegID).Scan(&callID, &practiceID); err != nil {
		return fmt.Errorf("read degraded caller audio Call: %w", err)
	}
	return appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"caller_audio.degraded",
		"",
		"",
		commandID,
		opaqueReference(callLegID),
		errorCode,
		m.now(),
	)
}
