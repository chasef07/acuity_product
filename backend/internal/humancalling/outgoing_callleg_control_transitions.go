package humancalling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type endedCallLegState struct {
	callID           string
	practiceID       string
	role             string
	direction        string
	legState         string
	terminalOutcome  string
	bridgedAt        *time.Time
	callBridged      bool
	voicemailStarted bool
}

func (m *Module) finishEndedCallLeg(
	ctx context.Context,
	tx pgx.Tx,
	callLegID string,
	fact ProviderFact,
) error {
	var ended endedCallLegState
	if err := tx.QueryRow(ctx, `
			SELECT call.id::text, call.practice_id::text, leg.role,
				call.direction, leg.state, COALESCE(call.terminal_outcome, ''),
				leg.bridged_at,
			EXISTS (
				SELECT 1 FROM human_calling_call_legs bridged
				WHERE bridged.call_id = call.id AND bridged.bridged_at IS NOT NULL
			),
			EXISTS (
				SELECT 1 FROM human_calling_provider_commands voicemail
				WHERE voicemail.call_id = call.id
					AND voicemail.action IN ('SPEAK_VOICEMAIL', 'START_VOICEMAIL_RECORDING')
					AND voicemail.state IN ('PENDING', 'SENDING', 'SENT', 'AMBIGUOUS', 'RECONCILED')
			)
		FROM human_calling_calls call
		JOIN human_calling_call_legs leg ON leg.call_id = call.id
		WHERE leg.id = $1
	`, callLegID).Scan(
		&ended.callID,
		&ended.practiceID,
		&ended.role,
		&ended.direction,
		&ended.legState,
		&ended.terminalOutcome,
		&ended.bridgedAt,
		&ended.callBridged,
		&ended.voicemailStarted,
	); err != nil {
		return fmt.Errorf("read ended CallLeg state: %w", err)
	}
	quality, err := json.Marshal(fact.CallQualityStats)
	if err != nil {
		return fmt.Errorf("encode Call quality stats: %w", err)
	}
	if fact.CallQualityStats == nil {
		quality = nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET state = 'ENDED',
			ending_at = COALESCE(
				ending_at, GREATEST($2, COALESCE(answered_at, $2))
			),
			ended_at = GREATEST(
				$2, COALESCE(ending_at, $2), COALESCE(answered_at, $2),
				COALESCE(ended_at, $2)
			),
			hangup_cause = COALESCE(NULLIF($3, ''), hangup_cause),
			termination_source = COALESCE(NULLIF($4, ''), termination_source),
			sip_cause = COALESCE(NULLIF($5, ''), sip_cause),
			call_quality_stats = COALESCE($6, call_quality_stats), updated_at = $7
		WHERE id = $1
	`, callLegID, fact.OccurredAt, fact.HangupCause, fact.TerminationSource,
		fact.SIPCause, quality, m.now()); err != nil {
		return fmt.Errorf("finish ended CallLeg: %w", err)
	}
	providerTermination := fact.HangupCause
	if ended.direction == string(CallOutbound) {
		providerTermination = outboundTermination(fact.HangupCause)
	}
	authoritativeTermination := ended.role == "CALLER" ||
		(ended.direction == string(CallInbound) && ended.bridgedAt != nil) ||
		(ended.direction == string(CallOutbound) && ended.role == "DESTINATION")
	if authoritativeTermination {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET provider_termination = COALESCE(NULLIF($2, ''), provider_termination),
				updated_at = $3
			WHERE id = $1
		`, ended.callID, providerTermination, m.now()); err != nil {
			return fmt.Errorf("record provider Call termination: %w", err)
		}
	}
	if ended.terminalOutcome != "" {
		// Recovery/disposition already owns the terminal outcome.
	} else if ended.role == "CALLER" && !ended.callBridged {
		if ended.direction == string(CallInbound) && ended.voicemailStarted {
			if _, err := m.ensureRecoveryOutcome(
				ctx, tx, ended.callID, RecoveryMissedCall, "MISSED", fact,
			); err != nil {
				return fmt.Errorf("create voicemail caller recovery: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_calls
				SET terminal_outcome = COALESCE(terminal_outcome, 'ABANDONED'),
					ended_at = COALESCE(ended_at, $2), version = version + 1, updated_at = $3
				WHERE id = $1
			`, ended.callID, fact.OccurredAt, m.now()); err != nil {
				return fmt.Errorf("mark caller abandonment: %w", err)
			}
		}
		if err := m.endRemainingCallLegs(ctx, tx, ended.callID); err != nil {
			return err
		}
	} else if ended.callBridged &&
		(ended.role == "CALLER" || ended.bridgedAt != nil || ended.legState == "BRIDGE_PENDING") {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET terminal_outcome = COALESCE(terminal_outcome, 'ENDED'),
				ended_at = COALESCE(ended_at, $2),
				disposition_deadline = COALESCE(disposition_deadline, $4),
				version = version + 1, updated_at = $3
			WHERE id = $1
		`, ended.callID, fact.OccurredAt, m.now(), m.now().Add(m.config.DispositionDuration)); err != nil {
			return fmt.Errorf("end connected Call: %w", err)
		}
		if err := m.endConnectedCallLegs(ctx, tx, ended.callID); err != nil {
			return err
		}
	} else if ended.direction == string(CallOutbound) {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET terminal_outcome = 'UNANSWERED', ended_at = COALESCE(ended_at, $2),
				version = version + 1, updated_at = $3
			WHERE id = $1 AND terminal_outcome IS NULL
		`, ended.callID, fact.OccurredAt, m.now()); err != nil {
			return fmt.Errorf("end unanswered outbound Call: %w", err)
		}
		if err := m.endRemainingCallLegs(ctx, tx, ended.callID); err != nil {
			return err
		}
	} else if ended.role == "STAFF" && ended.legState == "BRIDGE_PENDING" {
		if err := m.maybeStartVoicemailAfterRingCompleted(ctx, tx, ended.callID); err != nil {
			return err
		}
	}
	if err := appendTimeline(ctx, tx, ended.callID, ended.practiceID, "call_leg.ended", "",
		fact.EventID, "", opaqueReference(fact.CallLegID),
		sanitizeCode(fact.HangupCause), fact.OccurredAt); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, ended.practiceID); err != nil {
		return err
	}
	return nil
}

func (m *Module) startCallLegVoicemail(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	callerLegID string,
	practiceID string,
	locationID string,
	callerControlID string,
) error {
	var alreadyStarted bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM human_calling_provider_commands
			WHERE call_id = $1
				AND action IN ('SPEAK_VOICEMAIL', 'START_VOICEMAIL_RECORDING')
				AND state IN ('PENDING', 'SENDING', 'SENT', 'AMBIGUOUS', 'RECONCILED')
		)
	`, callID).Scan(&alreadyStarted); err != nil {
		return fmt.Errorf("read existing voicemail command: %w", err)
	}
	if alreadyStarted {
		return nil
	}
	if err := m.endRemainingCallLegs(ctx, tx, callID); err != nil {
		return err
	}
	var greeting string
	if err := tx.QueryRow(ctx, `
		SELECT voicemail_greeting
		FROM human_calling_location_voice_numbers
		WHERE practice_id = $1 AND location_id = $2 AND enabled
		ORDER BY created_at, id
		LIMIT 1
	`, practiceID, locationID).Scan(&greeting); errors.Is(err, pgx.ErrNoRows) {
		greeting = defaultVoicemailGreeting
	} else if err != nil {
		return fmt.Errorf("load Location voicemail greeting: %w", err)
	}
	_, err := m.insertCallLegCommand(
		ctx, tx, callID, callerLegID, "", "", CommandSpeakVoicemail,
		callerControlID,
		map[string]any{
			"payload":  greeting,
			"voice":    "female",
			"language": "en-US",
			"client_state": encodeCallLegClientState(
				callID, callerLegID, "CALLER", "voicemail_greeting",
			),
		},
		"",
	)
	return err
}

func (m *Module) maybeStartVoicemailAfterRingCompleted(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
) error {
	var callerLegID, practiceID, locationID, callerControlID string
	var eligible bool
	if err := tx.QueryRow(ctx, `
		SELECT caller.id::text, call.practice_id::text, call.location_id::text,
			caller.provider_call_control_id,
			call.terminal_outcome IS NULL
			AND EXISTS (
				SELECT 1 FROM human_calling_timeline timeline
				WHERE timeline.call_id = call.id
					AND timeline.kind = 'ring_window.COMPLETED'
			)
			AND NOT EXISTS (
				SELECT 1 FROM human_calling_call_legs winner
				WHERE winner.call_id = call.id AND winner.role = 'STAFF'
					AND winner.state IN ('BRIDGE_PENDING', 'BRIDGED')
			)
			AND NOT EXISTS (
				SELECT 1 FROM human_calling_provider_commands bridge
				WHERE bridge.call_id = call.id AND bridge.action = 'BRIDGE'
					AND bridge.state IN ('SENDING', 'SENT', 'AMBIGUOUS')
			)
		FROM human_calling_calls call
		JOIN human_calling_call_legs caller
			ON caller.call_id = call.id AND caller.role = 'CALLER'
		WHERE call.id = $1
		FOR UPDATE OF call, caller
	`, callID).Scan(
		&callerLegID, &practiceID, &locationID, &callerControlID, &eligible,
	); err != nil {
		return fmt.Errorf("read completed ring-window transition: %w", err)
	}
	if !eligible {
		return nil
	}
	return m.startCallLegVoicemail(
		ctx, tx, callID, callerLegID, practiceID, locationID, callerControlID,
	)
}

func (m *Module) endRemainingCallLegs(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
) error {
	var includeCaller bool
	if err := tx.QueryRow(ctx, `
		SELECT direction = 'OUTBOUND' FROM human_calling_calls WHERE id = $1
	`, callID).Scan(&includeCaller); err != nil {
		return fmt.Errorf("read Call direction for cleanup: %w", err)
	}
	return m.endCallLegs(ctx, tx, callID, includeCaller)
}

func (m *Module) endConnectedCallLegs(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
) error {
	return m.endCallLegs(ctx, tx, callID, true)
}

func (m *Module) endCallLegs(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	includeCaller bool,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'FAILED', last_error_code = 'CALL_TERMINATED', updated_at = $2
		WHERE call_id = $1 AND state = 'PENDING' AND action <> 'HANGUP_LEG'
	`, callID, m.now()); err != nil {
		return fmt.Errorf("cancel unsent Call commands: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, role, COALESCE(staff_subject, ''),
			COALESCE(provider_call_control_id, ''),
			provider_connection_id IS NULL
				AND provider_call_control_id IS NULL
				AND provider_call_leg_id IS NULL
				AND provider_call_session_id IS NULL
		FROM human_calling_call_legs
		WHERE call_id = $1 AND ($2 OR role <> 'CALLER')
			AND state NOT IN ('ENDED', 'FAILED', 'ENDING')
		FOR UPDATE
	`, callID, includeCaller)
	if err != nil {
		return fmt.Errorf("lock remaining CallLegs: %w", err)
	}
	type remainingLeg struct {
		id, role, subject, controlID string
		identityless                 bool
	}
	remaining := []remainingLeg{}
	for rows.Next() {
		var leg remainingLeg
		if err := rows.Scan(
			&leg.id, &leg.role, &leg.subject, &leg.controlID, &leg.identityless,
		); err != nil {
			rows.Close()
			return err
		}
		remaining = append(remaining, leg)
	}
	rows.Close()
	for _, leg := range remaining {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_commands
			SET state = 'FAILED', last_error_code = 'CALL_TERMINATED', updated_at = $2
			WHERE call_leg_id = $1 AND state = 'PENDING'
		`, leg.id, m.now()); err != nil {
			return fmt.Errorf("cancel unsent CallLeg commands: %w", err)
		}
		if leg.controlID == "" {
			errorCode := "CALL_TERMINATED"
			if leg.role == "CALLER" && leg.identityless {
				errorCode = "CALL_TERMINATED_BEFORE_PROVIDER_START"
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_call_legs
				SET state = 'FAILED',
					ending_at = COALESCE(
						ending_at,
						GREATEST($2, COALESCE(answered_at, $2))
					),
					ended_at = COALESCE(
						ended_at,
						GREATEST($2, COALESCE(answered_at, $2), COALESCE(ending_at, $2))
					),
					error_code = COALESCE(error_code, $3), updated_at = $2
				WHERE id = $1
			`, leg.id, m.now(), errorCode); err != nil {
				return fmt.Errorf("fail undialed CallLeg: %w", err)
			}
			continue
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_call_legs
			SET state = 'ENDING', ending_at = COALESCE(
				ending_at,
				GREATEST($2, COALESCE(answered_at, $2))
			), updated_at = $2
			WHERE id = $1
		`, leg.id, m.now()); err != nil {
			return err
		}
		if _, err := m.insertCallLegCommand(
			ctx, tx, callID, leg.id, "", leg.subject, CommandHangupLeg,
			leg.controlID,
			map[string]any{"client_state": encodeCallLegClientState(
				callID, leg.id, leg.role, "cleanup",
			)},
			"",
		); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) failRoutingCall(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	errorCode string,
) error {
	var callerLegID, callerControlID string
	if err := tx.QueryRow(ctx, `
		SELECT id::text, COALESCE(provider_call_control_id, '')
		FROM human_calling_call_legs
		WHERE call_id = $1 AND role = 'CALLER'
		FOR UPDATE
	`, callID).Scan(&callerLegID, &callerControlID); err != nil {
		return fmt.Errorf("lock routing-failure caller: %w", err)
	}
	if _, err := m.ensureRecoveryOutcome(ctx, tx, callID, RecoveryMissedCall,
		"ROUTING_FAILED", ProviderFact{OccurredAt: m.now()}); err != nil {
		return fmt.Errorf("create routing-failure recovery: %w", err)
	}
	if err := m.endRemainingCallLegs(ctx, tx, callID); err != nil {
		return err
	}
	if callerControlID != "" {
		if _, err := m.insertCallLegCommand(
			ctx, tx, callID, callerLegID, "", "", CommandHangupLeg,
			callerControlID,
			map[string]any{"client_state": encodeCallLegClientState(
				callID, callerLegID, "CALLER", "routing_failure",
			)},
			"",
		); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET state = CASE WHEN state IN ('ENDED', 'FAILED') THEN state ELSE 'FAILED' END,
			ending_at = COALESCE(
				ending_at,
				GREATEST($3, COALESCE(answered_at, $3))
			),
			ended_at = COALESCE(
				ended_at,
				GREATEST($3, COALESCE(answered_at, $3), COALESCE(ending_at, $3))
			),
			error_code = COALESCE(error_code, $2), updated_at = $3
		WHERE call_id = $1 AND role = 'CALLER'
	`, callID, errorCode, m.now())
	return err
}
