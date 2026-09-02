package humancalling

import (
	"context"
	"errors"
	"fmt"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

func (m *Module) AcquireSoftphone(
	ctx context.Context,
	identity access.Identity,
	sessionID string,
	takeover bool,
) (SoftphoneState, error) {
	if sessionID == "" || identity.Subject == "" || !identity.EmailVerified {
		return SoftphoneState{}, ErrDenied
	}
	discovery, err := m.access.DiscoverActor(ctx, identity)
	if err != nil {
		if errors.Is(err, access.ErrDenied) {
			return SoftphoneState{}, ErrDenied
		}
		return SoftphoneState{}, fmt.Errorf("discover softphone access: %w", err)
	}
	if !hasOperationalCallingAccess(discovery) {
		return SoftphoneState{}, ErrDenied
	}
	now := m.now()
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SoftphoneState{}, fmt.Errorf("begin softphone lease acquisition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	practiceIDs, err := m.access.LockOperationalActor(ctx, tx, identity)
	if err != nil {
		return SoftphoneState{}, callingLookupError(err, ErrDenied)
	}
	rows, err := tx.Query(ctx, `
		SELECT leg.state
		FROM human_calling_calls call
		JOIN human_calling_call_legs leg ON leg.call_id = call.id
		JOIN access_operational_scopes operational_scope
			ON operational_scope.practice_id = call.practice_id
			AND operational_scope.user_subject = leg.staff_subject
		WHERE leg.role = 'STAFF' AND leg.staff_subject = $1
			AND (
				call.direction = 'OUTBOUND'
				OR operational_scope.role = 'STAFF'
				OR operational_scope.role IS NULL
			)
			AND (
				operational_scope.location_scope = 'ALL'
				OR EXISTS (
					SELECT 1 FROM access_membership_locations allowed
					WHERE allowed.membership_id = operational_scope.membership_id
						AND allowed.location_id = call.location_id
				)
			)
			AND leg.state NOT IN ('ENDED', 'FAILED')
		ORDER BY call.id
		FOR UPDATE OF call
	`, identity.Subject)
	if err != nil {
		return SoftphoneState{}, fmt.Errorf("lock active Calls for lease acquisition: %w", err)
	}
	connectedCall := false
	for rows.Next() {
		var legState string
		if err := rows.Scan(&legState); err != nil {
			rows.Close()
			return SoftphoneState{}, fmt.Errorf("scan active Call lock: %w", err)
		}
		if legState == "BRIDGE_PENDING" || legState == "BRIDGED" {
			connectedCall = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return SoftphoneState{}, fmt.Errorf("iterate active Call locks: %w", err)
	}
	rows.Close()
	if err := m.lockActiveRecipientTransfers(ctx, tx, identity.Subject); err != nil {
		return SoftphoneState{}, err
	}

	var previousSessionID string
	err = tx.QueryRow(ctx, `
		SELECT session_id FROM human_calling_softphone_leases
		WHERE user_subject = $1 FOR UPDATE
	`, identity.Subject).Scan(&previousSessionID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return SoftphoneState{}, fmt.Errorf("lock existing softphone lease: %w", err)
	}
	if connectedCall && previousSessionID != "" && previousSessionID != sessionID {
		if err := tx.Commit(ctx); err != nil {
			return SoftphoneState{}, fmt.Errorf("commit connected Call lease refusal: %w", err)
		}
		var state SoftphoneState
		if err := m.loadCurrentCallCapacity(ctx, identity.Subject, &state); err != nil {
			return SoftphoneState{}, err
		}
		return state, nil
	}
	if previousSessionID != "" && previousSessionID != sessionID {
		if err := m.failRecipientTransfersForReadinessLoss(
			ctx, tx, identity.Subject, previousSessionID,
		); err != nil {
			return SoftphoneState{}, err
		}
	}
	var state SoftphoneState
	err = tx.QueryRow(ctx, `
		INSERT INTO human_calling_softphone_leases (
			user_subject, session_id, lease_expires_at, readiness_updated_at
		)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_subject) DO UPDATE SET
			session_id = EXCLUDED.session_id,
			lease_expires_at = EXCLUDED.lease_expires_at,
			desired_available = false,
			registered = false,
			microphone_ready = false,
			audio_ready = false,
			session_healthy = false,
			readiness_updated_at = EXCLUDED.readiness_updated_at,
			version = human_calling_softphone_leases.version + 1,
			updated_at = EXCLUDED.readiness_updated_at
		WHERE human_calling_softphone_leases.session_id = EXCLUDED.session_id
			OR human_calling_softphone_leases.lease_expires_at <= EXCLUDED.readiness_updated_at
			OR $5
		RETURNING session_id, lease_expires_at
	`, identity.Subject, sessionID, now.Add(m.config.LeaseDuration), now,
		takeover).Scan(&state.SessionID, &state.LeaseExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return SoftphoneState{}, fmt.Errorf("commit losing lease acquisition: %w", err)
		}
		if err := m.loadCurrentCallCapacity(ctx, identity.Subject, &state); err != nil {
			return SoftphoneState{}, err
		}
		return state, nil
	}
	if err != nil {
		return SoftphoneState{}, fmt.Errorf("acquire softphone lease: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs leg
		SET staff_session_id = $2, updated_at = $3
		FROM human_calling_calls call, access_operational_scopes operational_scope
		WHERE leg.call_id = call.id
			AND operational_scope.practice_id = call.practice_id
			AND operational_scope.user_subject = leg.staff_subject
			AND leg.role = 'STAFF' AND leg.staff_subject = $1
			AND (
				call.direction = 'OUTBOUND'
				OR operational_scope.role = 'STAFF'
				OR operational_scope.role IS NULL
			)
			AND (
				operational_scope.location_scope = 'ALL'
				OR EXISTS (
					SELECT 1 FROM access_membership_locations allowed
					WHERE allowed.membership_id = operational_scope.membership_id
						AND allowed.location_id = call.location_id
				)
			)
			AND leg.state NOT IN ('ENDED', 'FAILED')
			AND leg.staff_session_id IS DISTINCT FROM $2
	`, identity.Subject, sessionID, now); err != nil {
		return SoftphoneState{}, fmt.Errorf("transfer Staff CallLeg ownership: %w", err)
	}
	if previousSessionID != "" && previousSessionID != sessionID {
		for _, practiceID := range practiceIDs {
			if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
				return SoftphoneState{}, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SoftphoneState{}, fmt.Errorf("commit softphone lease acquisition: %w", err)
	}
	state.Owner = true
	if err := m.loadCurrentCallCapacity(ctx, identity.Subject, &state); err != nil {
		return SoftphoneState{}, err
	}
	return state, nil
}

func (m *Module) SetReadiness(
	ctx context.Context,
	command ReadinessCommand,
) (SoftphoneState, error) {
	if command.Identity.Subject == "" || command.SessionID == "" {
		return SoftphoneState{}, ErrDenied
	}
	now := m.now()
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SoftphoneState{}, fmt.Errorf("begin calling readiness: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := m.access.LockOperationalActor(ctx, tx, command.Identity); err != nil {
		return SoftphoneState{}, callingLookupError(err, ErrDenied)
	}
	rows, err := tx.Query(ctx, `
		SELECT call.id
		FROM human_calling_calls call
		JOIN human_calling_call_legs leg ON leg.call_id = call.id
		WHERE leg.role = 'STAFF' AND leg.staff_subject = $1
			AND leg.state NOT IN ('ENDED', 'FAILED')
		ORDER BY call.id
		FOR UPDATE OF call
	`, command.Identity.Subject)
	if err != nil {
		return SoftphoneState{}, fmt.Errorf("lock occupied Calls for readiness: %w", err)
	}
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return SoftphoneState{}, fmt.Errorf("iterate occupied Call locks: %w", err)
	}
	rows.Close()
	if err := m.lockActiveRecipientTransfers(
		ctx, tx, command.Identity.Subject,
	); err != nil {
		return SoftphoneState{}, err
	}
	var ownsLease bool
	err = tx.QueryRow(ctx, `
		SELECT session_id = $2 AND lease_expires_at > $3
		FROM human_calling_softphone_leases
		WHERE user_subject = $1
		FOR UPDATE
	`, command.Identity.Subject, command.SessionID, now).Scan(&ownsLease)
	if errors.Is(err, pgx.ErrNoRows) {
		return SoftphoneState{}, ErrDenied
	}
	if err != nil {
		return SoftphoneState{}, fmt.Errorf("lock calling readiness lease: %w", err)
	}
	if !ownsLease {
		return SoftphoneState{}, ErrDenied
	}

	// Staff occupancy remains global even when access to its Practice is revoked:
	// the database permits only one occupied Staff Call at a time. The scoped
	// projection below withholds the revoked Call ID without inventing capacity
	// for a second provider Call.
	var state SoftphoneState
	err = tx.QueryRow(ctx, `
		UPDATE human_calling_softphone_leases SET
			registered = $3,
			microphone_ready = $4,
			audio_ready = $5,
			session_healthy = $6,
			desired_available = CASE WHEN EXISTS (
				SELECT 1
				FROM human_calling_call_legs leg
				JOIN human_calling_calls call ON call.id = leg.call_id
				WHERE leg.staff_subject = $1 AND leg.role = 'STAFF'
					AND (leg.state IN ('ANSWERED', 'BRIDGE_PENDING', 'BRIDGED')
						OR (leg.state = 'ENDING' AND leg.answered_at IS NOT NULL)
						OR (call.direction = 'OUTBOUND'
							AND call.terminal_outcome IS NULL
							AND leg.state IN ('PENDING', 'DIALING', 'RINGING', 'ENDING')))
			) THEN false ELSE $7 END,
			readiness_updated_at = $8,
			lease_expires_at = $8 + $9::interval,
			version = version + 1,
			updated_at = $8
		WHERE user_subject = $1 AND session_id = $2 AND lease_expires_at > $8
		RETURNING session_id, lease_expires_at,
			(desired_available AND registered AND microphone_ready
				AND audio_ready AND session_healthy)
	`, command.Identity.Subject, command.SessionID, command.Registered,
		command.MicrophoneReady, command.AudioReady, command.SessionHealthy,
		command.Available, now, m.config.LeaseDuration.String()).Scan(
		&state.SessionID, &state.LeaseExpiresAt, &state.Available,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SoftphoneState{}, ErrDenied
	}
	if err != nil {
		return SoftphoneState{}, fmt.Errorf("update calling readiness: %w", err)
	}
	if !command.Registered || !command.MicrophoneReady || !command.AudioReady ||
		!command.SessionHealthy {
		if err := m.failRecipientTransfersForReadinessLoss(
			ctx, tx, command.Identity.Subject, command.SessionID,
		); err != nil {
			return SoftphoneState{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SoftphoneState{}, fmt.Errorf("commit calling readiness: %w", err)
	}
	state.Owner = true
	if err := m.loadCurrentCallCapacity(ctx, command.Identity.Subject, &state); err != nil {
		return SoftphoneState{}, err
	}
	return state, nil
}

func (m *Module) loadCurrentCallCapacity(
	ctx context.Context,
	subject string,
	state *SoftphoneState,
) error {
	var globallyOccupied bool
	if err := m.database.QueryRow(ctx, `
		WITH occupied AS (
			SELECT call.id, call.practice_id, call.location_id, call.direction,
				leg.staff_session_id, leg.state, leg.answered_at, leg.updated_at
			FROM human_calling_calls call
			JOIN human_calling_call_legs leg ON leg.call_id = call.id
			WHERE leg.role = 'STAFF' AND leg.staff_subject = $1
				AND (
					leg.state IN ('ANSWERED', 'BRIDGE_PENDING', 'BRIDGED')
					OR (leg.state = 'ENDING' AND leg.answered_at IS NOT NULL)
					OR (
						call.direction = 'OUTBOUND'
						AND call.terminal_outcome IS NULL
						AND leg.state IN ('PENDING', 'DIALING', 'RINGING', 'ENDING')
					)
				)
		)
		SELECT COALESCE((
			SELECT occupied.id::text
			FROM occupied
			JOIN access_operational_scopes operational_scope
				ON operational_scope.practice_id = occupied.practice_id
				AND operational_scope.user_subject = $1
			WHERE (
					occupied.direction = 'OUTBOUND'
					OR operational_scope.role = 'STAFF'
					OR operational_scope.role IS NULL
				)
				AND (
					operational_scope.location_scope = 'ALL'
					OR EXISTS (
						SELECT 1 FROM access_membership_locations allowed
						WHERE allowed.membership_id = operational_scope.membership_id
							AND allowed.location_id = occupied.location_id
					)
				)
				AND (
					occupied.direction <> 'OUTBOUND'
					OR occupied.state IN ('ANSWERED', 'BRIDGE_PENDING', 'BRIDGED')
					OR (occupied.state = 'ENDING' AND occupied.answered_at IS NOT NULL)
					OR occupied.staff_session_id = $2
				)
			ORDER BY occupied.updated_at DESC, occupied.id LIMIT 1
		), ''), EXISTS (SELECT 1 FROM occupied)
	`, subject, state.SessionID).Scan(&state.ActiveCallID, &globallyOccupied); err != nil {
		return fmt.Errorf("read occupied Call: %w", err)
	}
	if globallyOccupied {
		state.Available = false
	}
	if err := m.database.QueryRow(ctx, `
		SELECT COALESCE((
			SELECT call.id::text
			FROM human_calling_calls call
			JOIN human_calling_call_legs leg ON leg.call_id = call.id
			JOIN access_operational_scopes operational_scope
				ON operational_scope.practice_id = call.practice_id
				AND operational_scope.user_subject = leg.staff_subject
			WHERE leg.role = 'STAFF' AND leg.staff_subject = $1
				AND (
					call.direction = 'OUTBOUND'
					OR operational_scope.role = 'STAFF'
					OR operational_scope.role IS NULL
				)
				AND (
					operational_scope.location_scope = 'ALL'
					OR EXISTS (
						SELECT 1 FROM access_membership_locations allowed
						WHERE allowed.membership_id = operational_scope.membership_id
							AND allowed.location_id = call.location_id
					)
				)
				AND leg.bridged_at IS NOT NULL
				AND call.terminal_outcome = 'ENDED'
				AND call.disposition_at IS NULL
			ORDER BY call.updated_at DESC, call.id LIMIT 1
		), '')
	`, subject).Scan(&state.PendingOutcomeCallID); err != nil {
		return fmt.Errorf("read pending Call outcome: %w", err)
	}
	return nil
}
