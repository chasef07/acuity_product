package humancalling

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type callLegClientState struct {
	Version   int    `json:"v"`
	CallID    string `json:"call"`
	CallLegID string `json:"call_leg"`
	Role      string `json:"role"`
	Kind      string `json:"kind,omitempty"`
}

func encodeCallLegClientState(
	callID string,
	callLegID string,
	role string,
	kind string,
) string {
	value, _ := json.Marshal(callLegClientState{
		Version:   2,
		CallID:    callID,
		CallLegID: callLegID,
		Role:      role,
		Kind:      kind,
	})
	return base64.StdEncoding.EncodeToString(value)
}

func parseCallLegClientState(value string) (callLegClientState, bool) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) > 512 {
		return callLegClientState{}, false
	}
	var state callLegClientState
	if err := json.Unmarshal(decoded, &state); err != nil ||
		state.Version != 2 || state.CallID == "" || state.CallLegID == "" ||
		state.Role == "" {
		return callLegClientState{}, false
	}
	return state, true
}

func (m *Module) admitHandoff(ctx context.Context, fact ProviderFact) error {
	if m.config.HandoffAdmissionClosed {
		return ErrHandoffAdmissionClosed
	}
	if fact.CallControlID == "" || fact.CallLegID == "" ||
		fact.CallSessionID == "" || fact.ConnectionID == "" ||
		fact.ConnectionID != m.config.CallControlID {
		return ErrInvalidHandoff
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin CallLeg handoff admission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	handoffID, practiceID, locationID, err := m.resolveHandoffForRefer(ctx, tx, fact)
	if err != nil {
		return err
	}
	var callerPhone string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(phone, '')
		FROM human_calling_handoffs
		WHERE id = $1
	`, handoffID).Scan(&callerPhone); err != nil {
		return fmt.Errorf("read handoff Contact Context: %w", err)
	}

	callID := uuid.NewString()
	callerLegID := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_calls (
			id, source_handoff_id, practice_id, location_id, direction,
			entry_point, caller_phone, version, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, 'INBOUND', 'AI_HANDOFF', NULLIF($5, ''), 1, $6, $6)
	`, callID, handoffID, practiceID, locationID, callerPhone, m.now()); err != nil {
		return fmt.Errorf("create admitted Call: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_call_legs (
			id, call_id, role, sequence, state, provider_connection_id,
			provider_call_control_id, provider_call_leg_id,
			provider_call_session_id, created_at, updated_at
		)
		VALUES ($1, $2, 'CALLER', 1, 'RINGING', NULLIF($3, ''), $4, $5, $6, $7, $7)
	`, callerLegID, callID, fact.ConnectionID, fact.CallControlID,
		fact.CallLegID, fact.CallSessionID, fact.OccurredAt); err != nil {
		return fmt.Errorf("create caller CallLeg: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_handoffs
		SET consumed_at = $2
		WHERE id = $1 AND consumed_at IS NULL
	`, handoffID, m.now()); err != nil {
		return fmt.Errorf("consume admitted handoff: %w", err)
	}
	if _, err := m.insertCallLegCommand(
		ctx,
		tx,
		callID,
		callerLegID,
		"",
		"",
		CommandAnswerCaller,
		fact.CallControlID,
		map[string]any{
			"transcription": false,
			"webhook_retries_policies": telnyxWebhookRetryPolicies(
				FactCallAnswered,
				FactCallHangup,
			),
			"client_state": encodeCallLegClientState(
				callID,
				callerLegID,
				"CALLER",
				"answer",
			),
		},
		"",
	); err != nil {
		return err
	}
	if err := appendTimeline(ctx, tx, callID, practiceID, "call.admitted", "",
		fact.EventID, "", opaqueReference(fact.CallLegID), "", fact.OccurredAt); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit CallLeg handoff admission: %w", err)
	}
	return nil
}

func (m *Module) applyCallerAnswered(ctx context.Context, fact ProviderFact) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin caller answer fan-out: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	var callID, callerLegID, practiceID, locationID, terminalOutcome, callerState string
	if err := tx.QueryRow(ctx, `
		SELECT call.id::text, leg.id::text, call.practice_id::text, call.location_id::text,
			COALESCE(call.terminal_outcome, ''), leg.state
		FROM human_calling_call_legs leg
		JOIN human_calling_calls call ON call.id = leg.call_id
		WHERE leg.role = 'CALLER'
			AND leg.provider_call_control_id = $1
			AND leg.provider_call_leg_id = $2
			AND leg.provider_call_session_id = $3
		FOR UPDATE OF call, leg
	`, fact.CallControlID, fact.CallLegID, fact.CallSessionID).Scan(
		&callID, &callerLegID, &practiceID, &locationID, &terminalOutcome, &callerState,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("correlate caller answer CallLeg: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'RECONCILED', sent_at = COALESCE(sent_at, $2),
			last_error_code = NULL, updated_at = $3
		WHERE call_leg_id = $1 AND action = 'ANSWER_CALLER'
			AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
	`, callerLegID, fact.OccurredAt, m.now()); err != nil {
		return fmt.Errorf("reconcile caller Answer: %w", err)
	}
	if terminalOutcome != "" || callerState == "ENDING" ||
		callerState == "ENDED" || callerState == "FAILED" {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET answered_at = COALESCE(answered_at, $2), updated_at = $3
		WHERE id = $1
	`, callerLegID, fact.OccurredAt, m.now()); err != nil {
		return fmt.Errorf("project caller answer: %w", err)
	}

	ringCommandID, err := m.insertCallLegCommand(
		ctx,
		tx,
		callID,
		callerLegID,
		"",
		"",
		CommandStartRingWindow,
		fact.CallControlID,
		map[string]any{
			"audio_url": m.config.RingbackURL,
			"loop":      "1",
			"client_state": encodeCallLegClientState(
				callID,
				callerLegID,
				"CALLER",
				"ring_window",
			),
		},
		"",
	)
	if err != nil {
		return err
	}

	rows, err := tx.Query(ctx, `
		SELECT membership.user_subject, lease.session_id, credential.provider_sip_username
		FROM access_memberships membership
		JOIN access_operational_users operational
			ON operational.user_subject = membership.user_subject
		JOIN human_calling_softphone_leases lease
			ON lease.user_subject = membership.user_subject
		JOIN human_calling_credentials credential
			ON credential.user_subject = membership.user_subject
		WHERE membership.practice_id = $1
			AND membership.role = 'STAFF'
			AND membership.revoked_at IS NULL
			AND (
				membership.location_scope = 'ALL'
				OR EXISTS (
					SELECT 1 FROM access_membership_locations allowed
					WHERE allowed.membership_id = membership.id
						AND allowed.location_id = $2
				)
			)
			AND lease.desired_available
			AND lease.registered
			AND lease.microphone_ready
			AND lease.audio_ready
			AND lease.session_healthy
			AND lease.lease_expires_at > $3
			AND lease.readiness_updated_at > $3 - $4::interval
			AND credential.state = 'ACTIVE'
			AND credential.provider_sip_username IS NOT NULL
			AND NOT EXISTS (
				SELECT 1 FROM human_calling_call_legs occupied
				WHERE occupied.staff_subject = membership.user_subject
					AND (
						occupied.state IN ('BRIDGE_PENDING', 'BRIDGED')
						OR (occupied.state = 'ENDING' AND occupied.answered_at IS NOT NULL)
					)
			)
		ORDER BY membership.user_subject
		FOR UPDATE OF lease
	`, practiceID, locationID, m.now(), m.config.ReadinessGrace.String())
	if err != nil {
		return fmt.Errorf("snapshot eligible Staff: %w", err)
	}
	type staffTarget struct {
		subject     string
		sessionID   string
		sipUsername string
	}
	staff := []staffTarget{}
	for rows.Next() {
		var target staffTarget
		if err := rows.Scan(&target.subject, &target.sessionID, &target.sipUsername); err != nil {
			rows.Close()
			return fmt.Errorf("scan eligible Staff: %w", err)
		}
		staff = append(staff, target)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read eligible Staff: %w", err)
	}
	rows.Close()

	for _, target := range staff {
		legID := uuid.NewString()
		if _, err := tx.Exec(ctx, `
			INSERT INTO human_calling_call_legs (
				id, call_id, role, sequence, staff_subject, staff_session_id,
				state, created_at, updated_at
			)
			VALUES ($1, $2, 'STAFF', 1, $3, $4, 'PENDING', $5, $5)
		`, legID, callID, target.subject, target.sessionID, m.now()); err != nil {
			return fmt.Errorf("create Staff CallLeg: %w", err)
		}
		payload := map[string]any{
			"to":               managedSIPDestination(target.sipUsername, m.config.StaffSIPDomain),
			"connection_id":    m.config.CallControlID,
			"from":             m.config.FromNumber,
			"link_to":          fact.CallControlID,
			"bridge_intent":    true,
			"bridge_on_answer": false,
			"timeout_secs":     int(m.config.RingWindowDuration.Seconds()),
			"webhook_retries_policies": telnyxWebhookRetryPolicies(
				FactCallInitiated,
				FactCallAnswered,
				FactCallHangup,
			),
			"client_state": encodeCallLegClientState(
				callID,
				legID,
				"STAFF",
				"dial",
			),
			"custom_headers": []map[string]string{{
				"name":  "X-Acuity-Media-Token",
				"value": m.staffMediaToken(callID, legID),
			}},
		}
		if _, err := m.insertCallLegCommand(
			ctx, tx, callID, legID, "", target.subject, CommandDialStaff,
			fact.CallControlID, payload, ringCommandID,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET version = version + 1, updated_at = $2
		WHERE id = $1
	`, callID, m.now()); err != nil {
		return fmt.Errorf("advance fan-out Call version: %w", err)
	}
	if err := appendTimeline(ctx, tx, callID, practiceID, "provider.caller.answered", "",
		fact.EventID, "", opaqueReference(fact.CallLegID), "", fact.OccurredAt); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit caller answer fan-out: %w", err)
	}
	return nil
}

func (m *Module) applyStaffInitiated(
	ctx context.Context,
	fact ProviderFact,
	callID string,
) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || state.CallID != callID || state.Role != "STAFF" ||
		fact.CallControlID == "" || fact.CallLegID == "" || fact.CallSessionID == "" {
		return ErrConflict
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin Staff CallLeg projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	var practiceID, locationID, staffSubject, staffSessionID, priorLegState string
	var storedConnectionID, storedControlID, storedLegID, storedSessionID string
	var direction CallDirection
	var terminalOutcome *string
	answerOutcome := observability.StaffAnswerTerminal
	if err := tx.QueryRow(ctx, `
		SELECT practice_id::text, location_id::text, direction, terminal_outcome
		FROM human_calling_calls
		WHERE id = $1
		FOR UPDATE
	`, callID).Scan(&practiceID, &locationID, &direction, &terminalOutcome); err != nil {
		return fmt.Errorf("lock Staff answer Call: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT staff_subject, COALESCE(staff_session_id, ''), state,
			COALESCE(provider_connection_id, ''),
			COALESCE(provider_call_control_id, ''),
			COALESCE(provider_call_leg_id, ''),
			COALESCE(provider_call_session_id, '')
		FROM human_calling_call_legs
		WHERE id = $1 AND call_id = $2 AND role = 'STAFF'
		FOR UPDATE
	`, state.CallLegID, callID).Scan(
		&staffSubject, &staffSessionID, &priorLegState, &storedConnectionID, &storedControlID,
		&storedLegID, &storedSessionID,
	); err != nil {
		return fmt.Errorf("lock Staff CallLeg: %w", err)
	}
	if (storedConnectionID != "" && fact.ConnectionID != storedConnectionID) ||
		(storedControlID != "" && fact.CallControlID != storedControlID) ||
		(storedLegID != "" && fact.CallLegID != storedLegID) ||
		(storedSessionID != "" && fact.CallSessionID != storedSessionID) {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET
			provider_connection_id = COALESCE(provider_connection_id, NULLIF($2, '')),
			provider_call_control_id = COALESCE(provider_call_control_id, $3),
			provider_call_leg_id = COALESCE(provider_call_leg_id, $4),
			provider_call_session_id = COALESCE(provider_call_session_id, NULLIF($5, '')),
			state = CASE WHEN state IN ('PENDING', 'DIALING') THEN 'RINGING' ELSE state END,
			updated_at = $6
		WHERE id = $1
	`, state.CallLegID, fact.ConnectionID, fact.CallControlID, fact.CallLegID,
		fact.CallSessionID, m.now()); err != nil {
		return fmt.Errorf("project Staff CallLeg identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'RECONCILED', sent_at = COALESCE(sent_at, $2),
			last_error_code = NULL, updated_at = $3
		WHERE call_leg_id = $1 AND action IN ('DIAL_STAFF', 'DIAL_OUTBOUND_STAFF')
			AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
	`, state.CallLegID, fact.OccurredAt, m.now()); err != nil {
		return fmt.Errorf("reconcile Staff Dial: %w", err)
	}
	if fact.Type == FactCallInitiated &&
		(terminalOutcome != nil || priorLegState == "ENDING" ||
			priorLegState == "FAILED" || priorLegState == "ENDED") {
		if _, err := m.insertCallLegCommand(
			ctx, tx, callID, state.CallLegID, "", staffSubject,
			CommandHangupLeg, fact.CallControlID,
			map[string]any{"client_state": encodeCallLegClientState(
				callID, state.CallLegID, "STAFF", "late_dial_cleanup",
			)},
			"",
		); err != nil {
			return err
		}
	}

	if fact.Type == FactCallAnswered {
		var leaseEligible bool
		err := tx.QueryRow(ctx, `
			SELECT session_id = $2
				AND lease_expires_at > $3
				AND readiness_updated_at > $3 - $4::interval
				AND registered AND microphone_ready AND audio_ready AND session_healthy
				AND ($5 = 'OUTBOUND' OR desired_available)
			FROM human_calling_softphone_leases
			WHERE user_subject = $1
			FOR UPDATE
		`, staffSubject, staffSessionID, m.now(), m.config.ReadinessGrace.String(),
			direction).Scan(&leaseEligible)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lock Staff occupancy owner: %w", err)
		}
		var staffEligible bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM access_memberships membership
				JOIN access_operational_users operational
					ON operational.user_subject = membership.user_subject
				WHERE membership.practice_id = $1
					AND membership.user_subject = $2
					AND membership.revoked_at IS NULL
					AND (
						membership.location_scope = 'ALL'
						OR EXISTS (
							SELECT 1 FROM access_membership_locations allowed
							WHERE allowed.membership_id = membership.id
								AND allowed.location_id = $3
						)
					)
			)
		`, practiceID, staffSubject, locationID).Scan(&staffEligible); err != nil {
			return fmt.Errorf("revalidate Staff answer authorization: %w", err)
		}
		if priorLegState == "BRIDGE_PENDING" || priorLegState == "BRIDGED" {
			// Reordered duplicate answer evidence must not create another Bridge.
		} else if !staffEligible || !leaseEligible || terminalOutcome != nil ||
			(priorLegState != "PENDING" && priorLegState != "DIALING" &&
				priorLegState != "RINGING") {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_call_legs
				SET state = 'ENDING', answered_at = COALESCE(answered_at, $2),
					ending_at = COALESCE(
						ending_at,
						GREATEST($2, COALESCE(answered_at, $2))
					), updated_at = $3
				WHERE id = $1 AND state NOT IN ('ENDED', 'FAILED')
			`, state.CallLegID, fact.OccurredAt, m.now()); err != nil {
				return fmt.Errorf("end unauthorized Staff answer: %w", err)
			}
			if _, err := m.insertCallLegCommand(
				ctx, tx, callID, state.CallLegID, "", staffSubject,
				CommandHangupLeg, fact.CallControlID,
				map[string]any{"client_state": encodeCallLegClientState(
					callID, state.CallLegID, "STAFF", "cleanup",
				)},
				"",
			); err != nil {
				return err
			}
		} else if direction == CallOutbound {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_call_legs
				SET state = 'BRIDGE_PENDING', answered_at = COALESCE(answered_at, $2),
					bridge_pending_at = COALESCE(bridge_pending_at, $2), updated_at = $3
				WHERE id = $1 AND state IN ('PENDING', 'DIALING', 'RINGING', 'BRIDGE_PENDING')
			`, state.CallLegID, fact.OccurredAt, m.now()); err != nil {
				return fmt.Errorf("occupy outbound Staff CallLeg: %w", err)
			}
			answerOutcome = observability.StaffAnswerOutbound
		} else {
			var callWinner, staffOccupied bool
			if err := tx.QueryRow(ctx, `
			SELECT
				EXISTS (
					SELECT 1 FROM human_calling_call_legs
					WHERE call_id = $1 AND role = 'STAFF'
						AND state IN ('BRIDGE_PENDING', 'BRIDGED')
				),
				EXISTS (
					SELECT 1 FROM human_calling_call_legs
					WHERE staff_subject = $2 AND id <> $3
						AND (
							state IN ('BRIDGE_PENDING', 'BRIDGED')
							OR (state = 'ENDING' AND answered_at IS NOT NULL)
						)
				)
		`, callID, staffSubject, state.CallLegID).Scan(&callWinner, &staffOccupied); err != nil {
				return fmt.Errorf("read Staff answer availability: %w", err)
			}
			if !callWinner && !staffOccupied {
				answerOutcome = observability.StaffAnswerWinner
				var callerLegID, callerControlID string
				if err := tx.QueryRow(ctx, `
				SELECT id::text, provider_call_control_id
				FROM human_calling_call_legs
				WHERE call_id = $1 AND role = 'CALLER'
			`, callID).Scan(&callerLegID, &callerControlID); err != nil {
					return fmt.Errorf("read caller Bridge peer: %w", err)
				}
				if _, err := tx.Exec(ctx, `
					UPDATE human_calling_call_legs
				SET state = 'BRIDGE_PENDING',
					answered_at = COALESCE(answered_at, $2),
					bridge_pending_at = COALESCE(bridge_pending_at, $2),
					updated_at = $3
				WHERE id = $1
				`, state.CallLegID, fact.OccurredAt, m.now()); err != nil {
					return fmt.Errorf("claim provisional Staff winner: %w", err)
				}
				if _, err := tx.Exec(ctx, `
					UPDATE human_calling_softphone_leases
					SET desired_available = false, version = version + 1, updated_at = $3
					WHERE user_subject = $1 AND session_id = $2
				`, staffSubject, staffSessionID, m.now()); err != nil {
					return fmt.Errorf("reserve inbound softphone: %w", err)
				}
				if _, err := m.insertCallLegCommand(
					ctx, tx, callID, state.CallLegID, callerLegID, staffSubject,
					CommandBridge, fact.CallControlID,
					map[string]any{
						"call_control_id":       callerControlID,
						"prevent_double_bridge": true,
						"client_state": encodeCallLegClientState(
							callID, state.CallLegID, "STAFF", "bridge",
						),
					},
					"",
				); err != nil {
					return err
				}
			} else {
				if callWinner {
					answerOutcome = observability.StaffAnswerLostRace
				} else if staffOccupied {
					answerOutcome = observability.StaffAnswerOccupied
				}
				if _, err := tx.Exec(ctx, `
					UPDATE human_calling_call_legs
					SET state = 'ENDING', answered_at = COALESCE(answered_at, $2),
						ending_at = COALESCE(
							ending_at,
							GREATEST($2, COALESCE(answered_at, $2))
						), updated_at = $3
				WHERE id = $1 AND state NOT IN ('ENDED', 'FAILED')
			`, state.CallLegID, fact.OccurredAt, m.now()); err != nil {
					return fmt.Errorf("end losing Staff answer: %w", err)
				}
				if _, err := m.insertCallLegCommand(
					ctx, tx, callID, state.CallLegID, "", staffSubject,
					CommandHangupLeg, fact.CallControlID,
					map[string]any{"client_state": encodeCallLegClientState(
						callID, state.CallLegID, "STAFF", "cleanup",
					)},
					"",
				); err != nil {
					return err
				}
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1, updated_at = $2
		WHERE id = $1
	`, callID, m.now()); err != nil {
		return fmt.Errorf("advance Staff fact Call version: %w", err)
	}
	kind := "provider.staff.initiated"
	if fact.Type == FactCallAnswered {
		kind = "provider.staff.answered"
	}
	if err := appendTimeline(ctx, tx, callID, practiceID, kind, staffSubject,
		fact.EventID, "", opaqueReference(fact.CallLegID), "", fact.OccurredAt); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Staff CallLeg projection: %w", err)
	}
	if fact.Type == FactCallAnswered {
		observability.Record(m.observer, observability.StaffAnswered(answerOutcome))
	}
	return nil
}

func (m *Module) applyRingbackStarted(ctx context.Context, fact ProviderFact) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || state.Kind != "ring_window" {
		return ErrConflict
	}
	return m.recordRingWindowFact(ctx, fact, state, false)
}

func (m *Module) applyRingWindowEnded(ctx context.Context, fact ProviderFact) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || state.Kind != "ring_window" {
		return ErrConflict
	}
	return m.recordRingWindowFact(ctx, fact, state, true)
}

func (m *Module) recordRingWindowFact(
	ctx context.Context,
	fact ProviderFact,
	state callLegClientState,
	ended bool,
) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin ring-window projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var practiceID, locationID, callerControlID, callerProviderLegID, callerSessionID string
	var terminalOutcome *string
	if err := tx.QueryRow(ctx, `
		SELECT call.practice_id::text, call.location_id::text,
			leg.provider_call_control_id, leg.provider_call_leg_id,
			COALESCE(leg.provider_call_session_id, ''), call.terminal_outcome
		FROM human_calling_calls call
		JOIN human_calling_call_legs leg ON leg.call_id = call.id
		WHERE call.id = $1 AND leg.id = $2 AND leg.role = 'CALLER'
		FOR UPDATE OF call, leg
	`, state.CallID, state.CallLegID).Scan(
		&practiceID, &locationID, &callerControlID, &callerProviderLegID,
		&callerSessionID, &terminalOutcome,
	); err != nil {
		return fmt.Errorf("lock ring-window Call: %w", err)
	}
	if fact.CallControlID != callerControlID || fact.CallLegID != callerProviderLegID ||
		fact.CallSessionID != callerSessionID {
		return ErrConflict
	}
	if ended {
		var degradedCallerAudio bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM human_calling_timeline
				WHERE call_id = $1 AND kind = 'caller_audio.degraded'
			)
		`, state.CallID).Scan(&degradedCallerAudio); err != nil {
			return fmt.Errorf("read degraded caller audio: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_commands
			SET state = 'RECONCILED', sent_at = COALESCE(sent_at, $2),
				last_error_code = NULL, updated_at = $3
			WHERE call_leg_id = $1
				AND action IN ('START_RING_WINDOW', 'STOP_RING_WINDOW')
				AND state IN ('PENDING', 'SENDING', 'SENT', 'AMBIGUOUS', 'FAILED')
		`, state.CallLegID, fact.OccurredAt, m.now()); err != nil {
			return fmt.Errorf("reconcile ring-window command: %w", err)
		}
		if err := appendTimeline(ctx, tx, state.CallID, practiceID,
			"ring_window."+sanitizeCode(fact.PlaybackStatus), "", fact.EventID,
			"", opaqueReference(fact.CallLegID), "", fact.OccurredAt); err != nil {
			return err
		}
		if degradedCallerAudio {
			if err := appendTimeline(ctx, tx, state.CallID, practiceID,
				"caller_audio.converged", "", fact.EventID, "",
				opaqueReference(fact.CallLegID), "", fact.OccurredAt); err != nil {
				return err
			}
		}
		if fact.PlaybackStatus == "completed" && terminalOutcome == nil {
			if err := m.maybeStartVoicemailAfterRingCompleted(ctx, tx, state.CallID); err != nil {
				return err
			}
		} else if fact.PlaybackStatus == "cancelled" && terminalOutcome == nil {
			var bridgeInProgress bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM human_calling_call_legs
					WHERE call_id = $1 AND state = 'BRIDGED'
				)
			`, state.CallID).Scan(&bridgeInProgress); err != nil {
				return fmt.Errorf("read cancelled ring-window bridge: %w", err)
			}
			if !bridgeInProgress {
				if err := m.failRoutingCall(ctx, tx, state.CallID, "RING_WINDOW_CANCELLED"); err != nil {
					return err
				}
			}
		} else if fact.PlaybackStatus != "call_hangup" && terminalOutcome == nil {
			if err := m.failRoutingCall(ctx, tx, state.CallID, "RING_WINDOW_FAILED"); err != nil {
				return err
			}
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_commands
			SET state = 'RECONCILED', sent_at = COALESCE(sent_at, $2),
				last_error_code = NULL, updated_at = $3
			WHERE call_leg_id = $1 AND action = 'START_RING_WINDOW'
				AND state IN ('PENDING', 'SENDING', 'SENT', 'AMBIGUOUS', 'FAILED')
		`, state.CallLegID, fact.OccurredAt, m.now()); err != nil {
			return fmt.Errorf("reconcile started ring-window command: %w", err)
		}
		if err := appendTimeline(ctx, tx, state.CallID, practiceID,
			"ring_window.started", "", fact.EventID, "",
			opaqueReference(fact.CallLegID), "", fact.OccurredAt); err != nil {
			return err
		}
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *Module) applyBridge(ctx context.Context, fact ProviderFact) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || state.Role != "STAFF" {
		return ErrConflict
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin Bridge projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var practiceID, winnerSubject, callerLegID, callerControlID, callerState string
	var winnerControlID, winnerProviderLegID, winnerSessionID string
	var answeredAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT call.practice_id::text, winner.staff_subject,
			caller.id::text, caller.provider_call_control_id, caller.state,
			winner.answered_at,
			winner.provider_call_control_id, winner.provider_call_leg_id,
			COALESCE(winner.provider_call_session_id, '')
		FROM human_calling_calls call
		JOIN human_calling_call_legs winner
			ON winner.call_id = call.id AND winner.id = $2
		JOIN human_calling_call_legs caller
			ON caller.call_id = call.id AND caller.role = 'CALLER'
		WHERE call.id = $1
		FOR UPDATE OF call, winner, caller
	`, state.CallID, state.CallLegID).Scan(
		&practiceID, &winnerSubject, &callerLegID, &callerControlID, &callerState,
		&answeredAt,
		&winnerControlID, &winnerProviderLegID, &winnerSessionID,
	); err != nil {
		return fmt.Errorf("lock bridged CallLegs: %w", err)
	}
	if fact.CallControlID != winnerControlID || fact.CallLegID != winnerProviderLegID ||
		fact.CallSessionID != winnerSessionID {
		return ErrConflict
	}
	historicalBridge, err := projectBridgeEvidence(
		ctx, tx, state.CallID, state.CallLegID, fact.OccurredAt, m.now(),
	)
	if err != nil {
		return fmt.Errorf("confirm Staff Bridge: %w", err)
	}
	historicalUpgrade := false
	if historicalBridge {
		historicalUpgrade, err = m.upgradeHistoricalConnectedCall(
			ctx, tx, state.CallID, state.CallLegID, fact.OccurredAt,
		)
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'RECONCILED', sent_at = COALESCE(sent_at, $2), updated_at = $3
		WHERE call_leg_id = $1 AND action = 'BRIDGE'
			AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
	`, state.CallLegID, fact.OccurredAt, m.now()); err != nil {
		return fmt.Errorf("reconcile Bridge command: %w", err)
	}
	if !historicalBridge {
		if _, err := m.insertCallLegCommand(
			ctx, tx, state.CallID, callerLegID, "", "", CommandStopRingWindow,
			callerControlID,
			map[string]any{
				"stop": "all",
				"client_state": encodeCallLegClientState(
					state.CallID, callerLegID, "CALLER", "ring_window",
				),
			},
			"",
		); err != nil {
			return err
		}
	}
	rows, err := tx.Query(ctx, `
		SELECT leg.id::text, leg.call_id::text, leg.staff_subject,
			COALESCE(leg.provider_call_control_id, ''),
			EXISTS (
				SELECT 1 FROM human_calling_provider_commands command
				WHERE command.call_leg_id = leg.id
					AND command.action IN ('DIAL_STAFF', 'DIAL_OUTBOUND_STAFF')
					AND command.state IN ('SENDING', 'SENT', 'AMBIGUOUS')
			)
		FROM human_calling_call_legs leg
		WHERE leg.role = 'STAFF' AND leg.id <> $1
			AND leg.state IN ('PENDING', 'DIALING', 'RINGING')
			AND (leg.call_id = $2 OR leg.staff_subject = $3)
		FOR UPDATE
	`, state.CallLegID, state.CallID, winnerSubject)
	if err != nil {
		return fmt.Errorf("lock losing Staff CallLegs: %w", err)
	}
	type losingLeg struct {
		id, callID, subject, controlID string
		uncertainDial                  bool
	}
	losers := []losingLeg{}
	for rows.Next() {
		var loser losingLeg
		if err := rows.Scan(
			&loser.id, &loser.callID, &loser.subject, &loser.controlID,
			&loser.uncertainDial,
		); err != nil {
			rows.Close()
			return fmt.Errorf("scan losing Staff CallLeg: %w", err)
		}
		losers = append(losers, loser)
	}
	rows.Close()
	for _, loser := range losers {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_commands
			SET state = 'FAILED', last_error_code = 'LOST_BRIDGE_RACE', updated_at = $2
			WHERE call_leg_id = $1
				AND action IN ('DIAL_STAFF', 'DIAL_OUTBOUND_STAFF')
				AND state = 'PENDING'
		`, loser.id, m.now()); err != nil {
			return fmt.Errorf("cancel losing Staff Dial: %w", err)
		}
		if loser.controlID == "" && !loser.uncertainDial {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_call_legs
				SET state = 'FAILED', ending_at = COALESCE(ending_at, $2),
					ended_at = COALESCE(ended_at, $2),
					error_code = 'LOST_BRIDGE_RACE', updated_at = $2
				WHERE id = $1
			`, loser.id, m.now()); err != nil {
				return fmt.Errorf("cancel undialed losing Staff CallLeg: %w", err)
			}
			continue
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_call_legs
			SET state = 'ENDING', ending_at = COALESCE(ending_at, $2), updated_at = $2
			WHERE id = $1
		`, loser.id, m.now()); err != nil {
			return fmt.Errorf("mark losing Staff CallLeg ending: %w", err)
		}
		if loser.controlID == "" {
			// The Dial effect is uncertain. Its client_state remains reconcilable;
			// an exact Hangup is committed as soon as provider identity appears.
			continue
		}
		if _, err := m.insertCallLegCommand(
			ctx, tx, loser.callID, loser.id, "", loser.subject,
			CommandHangupLeg, loser.controlID,
			map[string]any{"client_state": encodeCallLegClientState(
				loser.callID, loser.id, "STAFF", "cleanup",
			)},
			"",
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1, updated_at = $2
		WHERE id = $1
	`, state.CallID, m.now()); err != nil {
		return fmt.Errorf("advance bridged Call version: %w", err)
	}
	if callerState == "BRIDGED" || historicalUpgrade {
		if err := appendTimeline(ctx, tx, state.CallID, practiceID, "call.connected",
			winnerSubject, fact.EventID, "", opaqueReference(fact.CallLegID), "",
			fact.OccurredAt); err != nil {
			return err
		}
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Bridge projection: %w", err)
	}
	observability.Record(m.observer, observability.CallLegBridged(fact.OccurredAt.Sub(answeredAt)))
	return nil
}

func (m *Module) correlateBridgeFact(
	ctx context.Context,
	fact ProviderFact,
) (ProviderFact, error) {
	var callID, callLegID, role, direction string
	if err := m.pool.QueryRow(ctx, `
		SELECT leg.call_id::text, leg.id::text, leg.role, call.direction
		FROM human_calling_call_legs leg
		JOIN human_calling_calls call ON call.id = leg.call_id
		WHERE leg.provider_call_control_id = $1
			AND leg.provider_call_leg_id = $2
			AND COALESCE(leg.provider_call_session_id, '') = $3
	`, fact.CallControlID, fact.CallLegID, fact.CallSessionID).Scan(
		&callID, &callLegID, &role, &direction,
	); err != nil {
		return ProviderFact{}, ErrConflict
	}
	kind := "bridge"
	if direction == string(CallOutbound) && role == "STAFF" {
		kind = "outbound_media"
	}
	fact.ClientState = encodeCallLegClientState(callID, callLegID, role, kind)
	return fact, nil
}

func projectBridgeEvidence(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	callLegID string,
	occurredAt time.Time,
	updatedAt time.Time,
) (bool, error) {
	var state string
	var endingAt, endedAt, callEndedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT leg.state, leg.ending_at, leg.ended_at, call.ended_at
		FROM human_calling_call_legs leg
		JOIN human_calling_calls call ON call.id = leg.call_id
		WHERE call.id = $1 AND leg.id = $2
		FOR UPDATE OF call, leg
	`, callID, callLegID).Scan(&state, &endingAt, &endedAt, &callEndedAt); err != nil {
		return false, err
	}
	historical := state == "ENDING" || state == "ENDED" || state == "FAILED"
	if historical {
		cutoff := endingAt
		if endedAt != nil {
			cutoff = endedAt
		}
		if callEndedAt != nil && (cutoff == nil || callEndedAt.After(*cutoff)) {
			cutoff = callEndedAt
		}
		if cutoff == nil || occurredAt.After(*cutoff) {
			return false, ErrConflict
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs
		SET state = CASE WHEN $4 THEN state ELSE 'BRIDGED' END,
			answered_at = COALESCE(answered_at, $2),
			bridge_pending_at = COALESCE(bridge_pending_at, $2),
			bridged_at = COALESCE(bridged_at, $2),
			updated_at = $3
		WHERE id = $1
	`, callLegID, occurredAt, updatedAt, historical); err != nil {
		return false, err
	}
	return historical, nil
}

func (m *Module) upgradeHistoricalConnectedCall(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	callLegID string,
	bridgedAt time.Time,
) (bool, error) {
	result, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET terminal_outcome = 'ENDED',
			ended_at = COALESCE(
				ended_at,
				(
					SELECT COALESCE(leg.ended_at, leg.ending_at)
					FROM human_calling_call_legs leg
					WHERE leg.id = $2 AND leg.call_id = human_calling_calls.id
				)
			),
			disposition_deadline = COALESCE(
				disposition_deadline,
				$4::timestamptz + $5::interval
			),
			version = version + 1,
			updated_at = $4
		WHERE id = $1
			AND (terminal_outcome IS NULL OR terminal_outcome IN ('ABANDONED', 'UNANSWERED'))
			AND $3 <= COALESCE(
				ended_at,
				(
					SELECT COALESCE(leg.ended_at, leg.ending_at)
					FROM human_calling_call_legs leg
					WHERE leg.id = $2 AND leg.call_id = human_calling_calls.id
				)
			)
	`, callID, callLegID, bridgedAt, m.now(), m.config.DispositionDuration.String())
	if err != nil {
		return false, fmt.Errorf("upgrade historical connected Call: %w", err)
	}
	return result.RowsAffected() != 0, nil
}

func (m *Module) applyCallerBridge(ctx context.Context, fact ProviderFact) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || state.Role != "CALLER" {
		return ErrConflict
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin caller Bridge projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var practiceID, controlID, providerLegID, sessionID, winnerSubject string
	var staffBridged bool
	if err := tx.QueryRow(ctx, `
		SELECT call.practice_id::text, caller.provider_call_control_id,
			caller.provider_call_leg_id, COALESCE(caller.provider_call_session_id, ''),
			EXISTS (
				SELECT 1 FROM human_calling_call_legs winner
				WHERE winner.call_id = call.id AND winner.role = 'STAFF'
					AND winner.state = 'BRIDGED'
			),
			COALESCE((
				SELECT winner.staff_subject FROM human_calling_call_legs winner
				WHERE winner.call_id = call.id AND winner.role = 'STAFF'
					AND winner.state = 'BRIDGED'
				ORDER BY winner.bridged_at, winner.id LIMIT 1
			), '')
		FROM human_calling_calls call
		JOIN human_calling_call_legs caller
			ON caller.call_id = call.id AND caller.id = $2 AND caller.role = 'CALLER'
		WHERE call.id = $1
		FOR UPDATE OF call, caller
	`, state.CallID, state.CallLegID).Scan(
		&practiceID, &controlID, &providerLegID, &sessionID,
		&staffBridged, &winnerSubject,
	); err != nil {
		return fmt.Errorf("lock caller Bridge CallLeg: %w", err)
	}
	if fact.CallControlID != controlID || fact.CallLegID != providerLegID ||
		fact.CallSessionID != sessionID {
		return ErrConflict
	}
	historicalBridge, err := projectBridgeEvidence(
		ctx, tx, state.CallID, state.CallLegID, fact.OccurredAt, m.now(),
	)
	if err != nil {
		return fmt.Errorf("confirm caller Bridge: %w", err)
	}
	historicalUpgrade := false
	if historicalBridge {
		historicalUpgrade, err = m.upgradeHistoricalConnectedCall(
			ctx, tx, state.CallID, state.CallLegID, fact.OccurredAt,
		)
		if err != nil {
			return err
		}
	}
	if staffBridged || historicalUpgrade {
		if err := appendTimeline(ctx, tx, state.CallID, practiceID, "call.connected",
			winnerSubject, fact.EventID, "", opaqueReference(fact.CallLegID), "",
			fact.OccurredAt); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1, updated_at = $2 WHERE id = $1
	`, state.CallID, m.now()); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *Module) applyHangup(ctx context.Context, fact ProviderFact) error {
	state, hasState := parseCallLegClientState(fact.ClientState)
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin CallLeg Hangup projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var callID, legID, practiceID, role, direction, legState, terminalOutcome string
	var storedControlID, storedProviderLegID, storedSessionID string
	var bridgedAt *time.Time
	var callBridged bool
	query := `
		SELECT call.id::text, leg.id::text, call.practice_id::text, leg.role,
			leg.bridged_at, call.direction, leg.state,
			COALESCE(call.terminal_outcome, ''), leg.provider_call_control_id,
			leg.provider_call_leg_id, COALESCE(leg.provider_call_session_id, ''),
			EXISTS (
				SELECT 1 FROM human_calling_call_legs bridged
				WHERE bridged.call_id = call.id AND bridged.bridged_at IS NOT NULL
			)
		FROM human_calling_call_legs leg
		JOIN human_calling_calls call ON call.id = leg.call_id
		WHERE leg.provider_call_control_id = $1 AND leg.provider_call_leg_id = $2
		FOR UPDATE OF call, leg
	`
	args := []any{fact.CallControlID, fact.CallLegID}
	if hasState {
		query = `
			SELECT call.id::text, leg.id::text, call.practice_id::text, leg.role,
				leg.bridged_at, call.direction, leg.state,
				COALESCE(call.terminal_outcome, ''), leg.provider_call_control_id,
				leg.provider_call_leg_id, COALESCE(leg.provider_call_session_id, ''),
				EXISTS (
					SELECT 1 FROM human_calling_call_legs bridged
					WHERE bridged.call_id = call.id AND bridged.bridged_at IS NOT NULL
				)
			FROM human_calling_calls call
			JOIN human_calling_call_legs leg ON leg.call_id = call.id
			WHERE call.id = $1 AND leg.id = $2
			FOR UPDATE OF call, leg
		`
		args = []any{state.CallID, state.CallLegID}
	}
	if err := tx.QueryRow(ctx, query, args...).Scan(
		&callID, &legID, &practiceID, &role, &bridgedAt, &direction, &legState,
		&terminalOutcome, &storedControlID, &storedProviderLegID, &storedSessionID,
		&callBridged,
	); err != nil {
		return fmt.Errorf("correlate hung-up CallLeg: %w", err)
	}
	if fact.CallControlID != storedControlID || fact.CallLegID != storedProviderLegID ||
		fact.CallSessionID != storedSessionID {
		return ErrConflict
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
		SET state = 'ENDED', ending_at = COALESCE(ending_at, $2), ended_at = $2,
			hangup_cause = NULLIF($3, ''), termination_source = NULLIF($4, ''),
			sip_cause = NULLIF($5, ''), call_quality_stats = COALESCE($6, call_quality_stats),
			updated_at = $7
		WHERE id = $1
	`, legID, fact.OccurredAt, fact.HangupCause, fact.TerminationSource,
		fact.SIPCause, quality, m.now()); err != nil {
		return fmt.Errorf("project CallLeg Hangup: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'RECONCILED', sent_at = COALESCE(sent_at, $2), updated_at = $3
		WHERE call_leg_id = $1 AND action = 'HANGUP_LEG'
			AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
	`, legID, fact.OccurredAt, m.now()); err != nil {
		return fmt.Errorf("reconcile CallLeg Hangup: %w", err)
	}
	providerTermination := fact.HangupCause
	if direction == string(CallOutbound) {
		providerTermination = outboundTermination(fact.HangupCause)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET provider_termination = COALESCE(NULLIF($2, ''), provider_termination),
			updated_at = $3
		WHERE id = $1
	`, callID, providerTermination, m.now()); err != nil {
		return fmt.Errorf("record provider Call termination: %w", err)
	}
	if terminalOutcome != "" {
		// Recovery/disposition already owns the terminal outcome.
	} else if role == "CALLER" && !callBridged {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET terminal_outcome = COALESCE(terminal_outcome, 'ABANDONED'),
				ended_at = COALESCE(ended_at, $2), version = version + 1, updated_at = $3
			WHERE id = $1
		`, callID, fact.OccurredAt, m.now()); err != nil {
			return fmt.Errorf("mark caller abandonment: %w", err)
		}
		if err := m.endRemainingCallLegs(ctx, tx, callID); err != nil {
			return err
		}
	} else if callBridged &&
		(role == "CALLER" || bridgedAt != nil || legState == "BRIDGE_PENDING") {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET terminal_outcome = COALESCE(terminal_outcome, 'ENDED'),
				ended_at = COALESCE(ended_at, $2),
				disposition_deadline = COALESCE(disposition_deadline, $4),
				version = version + 1, updated_at = $3
			WHERE id = $1
		`, callID, fact.OccurredAt, m.now(), m.now().Add(m.config.DispositionDuration)); err != nil {
			return fmt.Errorf("end connected Call: %w", err)
		}
		if err := m.endConnectedCallLegs(ctx, tx, callID); err != nil {
			return err
		}
	} else if direction == string(CallOutbound) {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET terminal_outcome = 'UNANSWERED', ended_at = COALESCE(ended_at, $2),
				version = version + 1, updated_at = $3
			WHERE id = $1 AND terminal_outcome IS NULL
		`, callID, fact.OccurredAt, m.now()); err != nil {
			return fmt.Errorf("end unanswered outbound Call: %w", err)
		}
		if err := m.endRemainingCallLegs(ctx, tx, callID); err != nil {
			return err
		}
	} else if role == "STAFF" && legState == "BRIDGE_PENDING" {
		if err := m.maybeStartVoicemailAfterRingCompleted(ctx, tx, callID); err != nil {
			return err
		}
	}
	if err := appendTimeline(ctx, tx, callID, practiceID, "call_leg.ended", "",
		fact.EventID, "", opaqueReference(fact.CallLegID),
		sanitizeCode(fact.HangupCause), fact.OccurredAt); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func outboundTermination(cause string) string {
	switch strings.ToLower(strings.TrimSpace(cause)) {
	case "normal_clearing":
		return "COMPLETED"
	case "no_answer", "no-answer", "timeout":
		return "NO_ANSWER"
	case "busy", "user_busy":
		return "BUSY"
	case "declined", "call_rejected", "rejected":
		return "DECLINED"
	default:
		return "FAILED"
	}
}
