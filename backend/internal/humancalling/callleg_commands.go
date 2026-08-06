package humancalling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (m *Module) processCommand(
	ctx context.Context,
	commandID string,
) (ProviderResult, error) {
	if err := m.claimCallLegCommand(ctx, commandID); err != nil {
		return ProviderResult{}, err
	}
	return m.executeCallLegCommand(ctx, commandID)
}

func (m *Module) claimNextCallLegCommand(ctx context.Context) (string, bool, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", false, fmt.Errorf("begin provider command claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var commandID string
	err = tx.QueryRow(ctx, `
		SELECT command.id::text
		FROM human_calling_provider_commands command
		WHERE command.state = 'PENDING'
			AND command.next_attempt_at <= $1
			AND command.action <> 'CREATE_JWT'
			AND (
				command.depends_on_command_id IS NULL
				OR EXISTS (
					SELECT 1 FROM human_calling_provider_commands dependency
					WHERE dependency.id = command.depends_on_command_id
						AND dependency.state IN ('SENT', 'RECONCILED')
				)
			)
		ORDER BY command.created_at, command.id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, m.now()).Scan(&commandID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("select provider command: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'SENDING', attempts = attempts + 1, updated_at = $2
		WHERE id = $1
	`, commandID, m.now()); err != nil {
		return "", false, fmt.Errorf("claim provider command: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("commit provider command claim: %w", err)
	}
	return commandID, true, nil
}

func (m *Module) claimCallLegCommand(ctx context.Context, commandID string) error {
	tag, err := m.pool.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'SENDING', attempts = attempts + 1, updated_at = $2
		WHERE id = $1 AND state = 'PENDING'
	`, commandID, m.now())
	if err != nil {
		return fmt.Errorf("claim committed provider command: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (m *Module) executeCallLegCommand(
	ctx context.Context,
	commandID string,
) (ProviderResult, error) {
	var command ProviderCommand
	var encoded []byte
	if err := m.pool.QueryRow(ctx, `
		SELECT id::text, COALESCE(call_leg_id::text, ''),
			COALESCE(peer_call_leg_id::text, ''), action,
			COALESCE(target_id, ''), payload, created_at
		FROM human_calling_provider_commands
		WHERE id = $1 AND state = 'SENDING'
	`, commandID).Scan(
		&command.ID,
		&command.CallLegID,
		&command.PeerCallLegID,
		&command.Action,
		&command.TargetID,
		&encoded,
		&command.createdAt,
	); err != nil {
		return ProviderResult{}, fmt.Errorf("load provider command: %w", err)
	}
	if err := json.Unmarshal(encoded, &command.Payload); err != nil {
		return ProviderResult{}, fmt.Errorf("decode provider command: %w", err)
	}
	if m.provider == nil {
		return ProviderResult{}, fmt.Errorf("provider is unavailable")
	}
	claimedAt := m.now()
	result, executeErr := m.provider.Execute(ctx, command)
	if err := m.finishCallLegCommand(ctx, command, result, executeErr); err != nil {
		return ProviderResult{}, err
	}
	state, _ := m.providerCommandResult(command, executeErr)
	if state != "PENDING" {
		m.recordProviderCommand(
			command,
			state,
			claimedAt,
			m.now().Sub(claimedAt),
		)
	}
	return result, executeErr
}

func (m *Module) providerCommandResult(
	command ProviderCommand,
	executeErr error,
) (string, string) {
	if executeErr == nil {
		return "SENT", ""
	}
	errorCode := safeProviderErrorCode(executeErr)
	if errors.Is(executeErr, ErrDefinitiveProviderFailure) ||
		(errors.Is(executeErr, ErrProviderTargetAbsent) && command.Action != CommandHangupLeg) {
		return "FAILED", errorCode
	}
	if errors.Is(executeErr, ErrProviderTargetAbsent) && command.Action == CommandHangupLeg {
		return "SENT", ""
	}
	if m.now().Sub(command.createdAt) < safeProviderRetryWindow {
		return "PENDING", errorCode
	}
	return "AMBIGUOUS", errorCode
}

func (m *Module) finishCallLegCommand(
	ctx context.Context,
	command ProviderCommand,
	result ProviderResult,
	executeErr error,
) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin provider command result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	state, errorCode := m.providerCommandResult(command, executeErr)
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = $2, last_error_code = NULLIF($3, ''),
			sent_at = CASE WHEN $2 = 'SENT' THEN COALESCE(sent_at, $4) ELSE sent_at END,
			next_attempt_at = CASE WHEN $2 = 'PENDING' THEN $4 + interval '5 seconds' ELSE next_attempt_at END,
			updated_at = $4
		WHERE id = $1 AND state = 'SENDING'
	`, command.ID, state, errorCode, m.now()); err != nil {
		return fmt.Errorf("record provider command result: %w", err)
	}
	if command.Action == CommandCreateCredential && executeErr == nil {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_credentials
			SET provider_credential_id = $2, provider_sip_username = $3,
				state = 'ACTIVE', last_error_code = NULL, updated_at = $4
			WHERE user_subject = (
				SELECT user_subject FROM human_calling_provider_commands WHERE id = $1
			)
		`, command.ID, result.CredentialID, result.SIPUsername, m.now()); err != nil {
			return fmt.Errorf("activate Staff credential: %w", err)
		}
	}
	if command.Action == CommandDisableCredential && executeErr == nil {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_credentials
			SET state = 'DISABLED', last_error_code = NULL, updated_at = $2
			WHERE user_subject = (
				SELECT user_subject FROM human_calling_provider_commands WHERE id = $1
			)
		`, command.ID, m.now()); err != nil {
			return fmt.Errorf("disable Staff credential: %w", err)
		}
	}
	if command.CallLegID != "" && executeErr == nil &&
		(command.Action == CommandDialStaff || command.Action == CommandDialOutboundStaff ||
			command.Action == CommandDialOutboundDestination) {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_call_legs
			SET state = CASE WHEN state = 'PENDING' THEN 'DIALING' ELSE state END,
				provider_call_control_id = COALESCE(provider_call_control_id, NULLIF($2, '')),
				provider_call_leg_id = COALESCE(provider_call_leg_id, NULLIF($3, '')),
				updated_at = $4
			WHERE id = $1
		`, command.CallLegID, result.CallControlID, result.CallLegID, m.now()); err != nil {
			return fmt.Errorf("record accepted Staff Dial: %w", err)
		}
		var callID, role, subject, legState, terminal string
		if err := tx.QueryRow(ctx, `
			SELECT leg.call_id::text, leg.role, COALESCE(leg.staff_subject, ''),
				leg.state,
				COALESCE(call.terminal_outcome, '')
			FROM human_calling_call_legs leg
			JOIN human_calling_calls call ON call.id = leg.call_id
			WHERE leg.id = $1
			FOR UPDATE OF call, leg
		`, command.CallLegID).Scan(
			&callID, &role, &subject, &legState, &terminal,
		); err != nil {
			return fmt.Errorf("read accepted Dial Call: %w", err)
		}
		if (terminal != "" || legState == "ENDING") && result.CallControlID != "" {
			if _, err := m.insertCallLegCommand(
				ctx, tx, callID, command.CallLegID, "", subject, CommandHangupLeg,
				result.CallControlID,
				map[string]any{"client_state": encodeCallLegClientState(
					callID, command.CallLegID, role, "late_dial_cleanup",
				)},
				"",
			); err != nil {
				return err
			}
		}
	}
	if command.CallLegID != "" && state == "FAILED" {
		switch command.Action {
		case CommandDialStaff, CommandDialOutboundStaff, CommandDialOutboundDestination:
			if err := m.failDialCallLeg(
				ctx, tx, command.CallLegID, command.Action, errorCode,
			); err != nil {
				return err
			}
		case CommandBridge:
			if err := m.failBridgeCallLeg(
				ctx, tx, command.CallLegID, errorCode,
			); err != nil {
				return err
			}
		case CommandAnswerCaller, CommandStartRingWindow,
			CommandSpeakVoicemail, CommandStartVoicemailRecording:
			var callID string
			if err := tx.QueryRow(ctx, `SELECT call_id::text FROM human_calling_call_legs WHERE id = $1`,
				command.CallLegID).Scan(&callID); err != nil {
				return err
			}
			if err := m.failRoutingCall(ctx, tx, callID, errorCode); err != nil {
				return err
			}
		}
	}
	if command.CallLegID != "" && command.Action == CommandStopRingWindow &&
		(state == "FAILED" || state == "AMBIGUOUS") {
		if err := m.recordDegradedCallerAudio(
			ctx, tx, command.ID, command.CallLegID, errorCode,
		); err != nil {
			return err
		}
	}
	if command.CallLegID != "" && command.Action == CommandHangupLeg && state == "SENT" &&
		errors.Is(executeErr, ErrProviderTargetAbsent) {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_call_legs
			SET state = 'ENDED', ending_at = COALESCE(ending_at, $2),
				ended_at = COALESCE(ended_at, $2), updated_at = $2
			WHERE id = $1 AND state NOT IN ('ENDED', 'FAILED')
		`, command.CallLegID, m.now()); err != nil {
			return fmt.Errorf("converge absent Hangup target: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (m *Module) failDialCallLeg(
	ctx context.Context,
	tx pgx.Tx,
	callLegID string,
	action CommandAction,
	errorCode string,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET state = 'FAILED', ended_at = $2, error_code = $3, updated_at = $2
		WHERE id = $1 AND state NOT IN ('ENDED', 'FAILED')
	`, callLegID, m.now(), errorCode); err != nil {
		return fmt.Errorf("fail rejected Dial CallLeg: %w", err)
	}
	if action == CommandDialStaff || action == CommandDialOutboundStaff {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_softphone_leases lease
			SET desired_available = false, session_healthy = false,
				version = version + 1, updated_at = $2
			FROM human_calling_call_legs leg
			WHERE leg.id = $1 AND lease.user_subject = leg.staff_subject
		`, callLegID, m.now()); err != nil {
			return fmt.Errorf("degrade failed Staff Dial readiness: %w", err)
		}
	}
	if action == CommandDialStaff {
		return nil
	}
	var callID string
	if err := tx.QueryRow(ctx, `
		SELECT call_id::text FROM human_calling_call_legs WHERE id = $1
	`, callLegID).Scan(&callID); err != nil {
		return fmt.Errorf("read rejected outbound Dial Call: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET terminal_outcome = 'UNANSWERED', ended_at = COALESCE(ended_at, $2),
			version = version + 1, updated_at = $2
		WHERE id = $1 AND terminal_outcome IS NULL
	`, callID, m.now()); err != nil {
		return fmt.Errorf("end rejected outbound Dial: %w", err)
	}
	return m.endRemainingCallLegs(ctx, tx, callID)
}

func (m *Module) failBridgeCallLeg(
	ctx context.Context,
	tx pgx.Tx,
	callLegID string,
	errorCode string,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET state = 'ENDING', ending_at = COALESCE(
				ending_at,
				GREATEST($2, COALESCE(answered_at, $2))
			),
			error_code = $3, updated_at = $2
		WHERE id = $1 AND state = 'BRIDGE_PENDING'
	`, callLegID, m.now(), errorCode); err != nil {
		return fmt.Errorf("release rejected Bridge: %w", err)
	}
	var callID, role, subject, controlID string
	if err := tx.QueryRow(ctx, `
		SELECT call_id::text, role, COALESCE(staff_subject, ''), provider_call_control_id
		FROM human_calling_call_legs WHERE id = $1
	`, callLegID).Scan(&callID, &role, &subject, &controlID); err != nil {
		return fmt.Errorf("read rejected Bridge CallLeg: %w", err)
	}
	if _, err := m.insertCallLegCommand(
		ctx, tx, callID, callLegID, "", subject, CommandHangupLeg, controlID,
		map[string]any{"client_state": encodeCallLegClientState(
			callID, callLegID, role, "cleanup",
		)},
		"",
	); err != nil {
		return err
	}
	if role != "DESTINATION" {
		return m.maybeStartVoicemailAfterRingCompleted(ctx, tx, callID)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET terminal_outcome = 'UNANSWERED', ended_at = COALESCE(ended_at, $2),
			version = version + 1, updated_at = $2
		WHERE id = $1 AND terminal_outcome IS NULL
	`, callID, m.now()); err != nil {
		return fmt.Errorf("end rejected outbound Bridge: %w", err)
	}
	return m.endRemainingCallLegs(ctx, tx, callID)
}

func (m *Module) insertCallLegCommand(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	callLegID string,
	peerCallLegID string,
	userSubject string,
	action CommandAction,
	targetID string,
	payload map[string]any,
	dependencyID string,
) (string, error) {
	if action == CommandHangupLeg {
		var existingID string
		err := tx.QueryRow(ctx, `
			SELECT id::text FROM human_calling_provider_commands
			WHERE call_leg_id = $1 AND action = 'HANGUP_LEG'
				AND target_id = $2
				AND state IN ('PENDING', 'SENDING', 'SENT', 'AMBIGUOUS', 'RECONCILED')
			ORDER BY created_at, id
			LIMIT 1
		`, callLegID, targetID).Scan(&existingID)
		if err == nil {
			return existingID, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("read existing Hangup command: %w", err)
		}
	}
	commandID := uuid.NewString()
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode %s command: %w", action, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			id, call_id, call_leg_id, peer_call_leg_id, user_subject,
			action, target_id, payload, depends_on_command_id, next_attempt_at
		)
		VALUES (
			$1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid,
			NULLIF($4, '')::uuid, NULLIF($5, ''), $6, NULLIF($7, ''),
			$8, NULLIF($9, '')::uuid, $10
		)
	`, commandID, callID, callLegID, peerCallLegID, userSubject, action,
		targetID, encoded, dependencyID, m.now()); err != nil {
		return "", fmt.Errorf("commit %s command: %w", action, err)
	}
	return commandID, nil
}
