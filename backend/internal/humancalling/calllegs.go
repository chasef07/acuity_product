package humancalling

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	case CommandAnswerCaller, CommandStartRingWindow,
		CommandSpeakVoicemail, CommandStartVoicemailRecording:
		var callID string
		if queryErr := tx.QueryRow(ctx, `
			SELECT call_id::text FROM human_calling_call_legs WHERE id = $1
		`, callLegID).Scan(&callID); queryErr != nil {
			return queryErr
		}
		err = m.failRoutingCall(ctx, tx, callID, errorCode)
	case CommandStopRingWindow:
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
	return m.endCallLegs(ctx, tx, callID, false)
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
			COALESCE(provider_call_control_id, '')
		FROM human_calling_call_legs
		WHERE call_id = $1 AND ($2 OR role <> 'CALLER')
			AND state NOT IN ('ENDED', 'FAILED', 'ENDING')
		FOR UPDATE
	`, callID, includeCaller)
	if err != nil {
		return fmt.Errorf("lock remaining CallLegs: %w", err)
	}
	type remainingLeg struct{ id, role, subject, controlID string }
	remaining := []remainingLeg{}
	for rows.Next() {
		var leg remainingLeg
		if err := rows.Scan(&leg.id, &leg.role, &leg.subject, &leg.controlID); err != nil {
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
					error_code = COALESCE(error_code, 'CALL_TERMINATED'), updated_at = $2
				WHERE id = $1
			`, leg.id, m.now()); err != nil {
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

func isUniqueConstraint(err error, constraint string) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505" &&
		postgresError.ConstraintName == constraint
}
