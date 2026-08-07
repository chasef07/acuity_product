package humancalling

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

func (m *Module) ProcessNextCommand(ctx context.Context) (bool, error) {
	commandID, ok, err := m.claimNextCallLegCommand(ctx)
	if err != nil || !ok {
		return ok, err
	}
	_, err = m.executeCallLegCommand(ctx, commandID)
	return true, err
}

func (m *Module) RecoverInterruptedCommands(ctx context.Context) error {
	now := m.now()
	if _, err := m.pool.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = CASE
				WHEN created_at > $1::timestamptz - interval '55 seconds' THEN 'PENDING'
				ELSE 'AMBIGUOUS'
			END,
			last_error_code = CASE
				WHEN created_at > $1::timestamptz - interval '55 seconds' THEN 'WORKER_INTERRUPTED'
				ELSE 'PROVIDER_EFFECT_UNCERTAIN'
			END,
			next_attempt_at = $1, updated_at = $1
		WHERE state = 'SENDING' AND updated_at <= $1::timestamptz - interval '30 seconds'
	`, now); err != nil {
		return fmt.Errorf("recover interrupted provider commands: %w", err)
	}
	return nil
}

func (m *Module) ReconcileStaleCalls(ctx context.Context) (int, error) {
	provider, ok := m.provider.(CallStateProvider)
	if !ok {
		return 0, nil
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin stale CallLeg reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var callID, legID, role, direction, connectionID string
	var controlID, providerLegID, sessionID string
	var commandID, providerClientState string
	var commandAction CommandAction
	var observationSince, commandCreatedAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT call.id::text, leg.id::text, leg.role, call.direction,
			COALESCE(leg.provider_connection_id, command.payload->>'connection_id', ''),
			COALESCE(leg.provider_call_control_id, ''),
			COALESCE(leg.provider_call_leg_id, ''),
			COALESCE(leg.provider_call_session_id, ''),
			COALESCE(command.id::text, ''), COALESCE(command.action, ''),
			COALESCE(command.payload->>'client_state', ''),
			leg.updated_at, COALESCE(command.created_at, leg.updated_at)
		FROM human_calling_calls call
		JOIN human_calling_call_legs leg ON leg.call_id = call.id
		LEFT JOIN LATERAL (
			SELECT pending.id, pending.action, pending.payload, pending.created_at
			FROM human_calling_provider_commands pending
			WHERE pending.call_leg_id = leg.id
				AND pending.state IN ('SENDING', 'SENT', 'AMBIGUOUS')
			ORDER BY pending.created_at, pending.id
			LIMIT 1
		) command ON true
		WHERE call.terminal_outcome IS NULL
			AND (leg.state NOT IN ('ENDED', 'FAILED') OR command.id IS NOT NULL)
			AND (
				(leg.provider_call_control_id IS NOT NULL AND leg.provider_call_leg_id IS NOT NULL)
				OR command.id IS NOT NULL
			)
			AND leg.updated_at <= $1::timestamptz - interval '60 seconds'
		ORDER BY leg.updated_at, leg.id
		FOR UPDATE OF call, leg SKIP LOCKED
		LIMIT 1
	`, m.now()).Scan(
		&callID, &legID, &role, &direction, &connectionID,
		&controlID, &providerLegID, &sessionID, &commandID, &commandAction,
		&providerClientState,
		&observationSince, &commandCreatedAt,
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
			return 1, err
		}
	}
	if commandID != "" {
		var unresolved bool
		if err := m.pool.QueryRow(ctx, `
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

func (m *Module) markUnobservedCommandAmbiguous(
	ctx context.Context,
	commandID string,
	action CommandAction,
	commandCreatedAt time.Time,
	observedAt time.Time,
) error {
	tag, err := m.pool.Exec(ctx, `
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
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin provider observation result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
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
