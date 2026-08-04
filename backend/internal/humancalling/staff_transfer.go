package humancalling

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type StaffTransferState string

const (
	StaffTransferRequested StaffTransferState = "REQUESTED"
	StaffTransferAccepted  StaffTransferState = "ACCEPTED"
	StaffTransferCompleted StaffTransferState = "COMPLETED"
	StaffTransferDeclined  StaffTransferState = "DECLINED"
	StaffTransferCancelled StaffTransferState = "CANCELLED"
	StaffTransferExpired   StaffTransferState = "EXPIRED"
	StaffTransferFailed    StaffTransferState = "FAILED"
)

type TransferCandidate struct {
	Subject string
	Email   string
}

type StaffTransfer struct {
	ID                 string
	CallID             string
	PracticeID         string
	LocationID         string
	LocationName       string
	Phone              string
	DisplayName        string
	RequestedBySubject string
	RequestedByEmail   string
	RecipientSubject   string
	RecipientEmail     string
	HandoffNote        string
	State              StaffTransferState
	ExpiresAt          time.Time
	CreatedAt          time.Time
	CompletedAt        *time.Time
}

type RequestStaffTransferCommand struct {
	Identity         access.Identity
	CallID           string
	SessionID        string
	RecipientSubject string
	HandoffNote      string
}

type RespondStaffTransferCommand struct {
	Identity   access.Identity
	TransferID string
	SessionID  string
}

func (m *Module) ListTransferCandidates(
	ctx context.Context,
	identity access.Identity,
	callID string,
) ([]TransferCandidate, error) {
	callID = strings.TrimSpace(callID)
	if m.pool == nil || m.access == nil || callID == "" {
		return nil, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin transfer candidate lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID, locationID, winner string
	var state CallState
	if err := tx.QueryRow(ctx, `
		SELECT practice_id::text, location_id::text, state, COALESCE(winner_subject, '')
		FROM human_calling_calls
		WHERE id = $1
	`, callID).Scan(&practiceID, &locationID, &state, &winner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDenied
		}
		return nil, fmt.Errorf("read transfer Call: %w", err)
	}
	if state != CallConnected || winner != identity.Subject {
		return nil, ErrConflict
	}
	authorization, err := m.access.LockReadAuthorization(ctx, tx, identity, practiceID, locationID)
	if err != nil || len(authorization.Locations) != 1 {
		return nil, ErrDenied
	}
	candidates, err := transferCandidates(ctx, tx, practiceID, locationID, identity.Subject, m.now())
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transfer candidate lookup: %w", err)
	}
	return candidates, nil
}

func (m *Module) RequestStaffTransfer(
	ctx context.Context,
	command RequestStaffTransferCommand,
) (StaffTransfer, error) {
	command.CallID = strings.TrimSpace(command.CallID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.RecipientSubject = strings.TrimSpace(command.RecipientSubject)
	command.HandoffNote = strings.TrimSpace(command.HandoffNote)
	if m.pool == nil || m.access == nil || command.CallID == "" ||
		command.SessionID == "" || command.RecipientSubject == "" ||
		len(command.HandoffNote) > 500 {
		return StaffTransfer{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return StaffTransfer{}, fmt.Errorf("begin staff transfer: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID, locationID, winner, claimantSession string
	var state CallState
	if err := tx.QueryRow(ctx, `
		SELECT practice_id::text, location_id::text, state,
			COALESCE(winner_subject, ''), COALESCE(claimant_session_id, '')
		FROM human_calling_calls
		WHERE id = $1
		FOR UPDATE
	`, command.CallID).Scan(
		&practiceID, &locationID, &state, &winner, &claimantSession,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StaffTransfer{}, ErrDenied
		}
		return StaffTransfer{}, fmt.Errorf("lock transfer Call: %w", err)
	}
	if state != CallConnected || winner != command.Identity.Subject ||
		claimantSession != command.SessionID {
		return StaffTransfer{}, ErrConflict
	}
	if _, err := m.access.LockReadAuthorization(
		ctx, tx, command.Identity, practiceID, locationID,
	); err != nil {
		return StaffTransfer{}, ErrDenied
	}
	candidates, err := transferCandidates(
		ctx, tx, practiceID, locationID, command.Identity.Subject, m.now(),
	)
	if err != nil {
		return StaffTransfer{}, err
	}
	eligible := false
	for _, candidate := range candidates {
		eligible = eligible || candidate.Subject == command.RecipientSubject
	}
	if !eligible {
		return StaffTransfer{}, ErrConflict
	}
	now := m.now()
	transferID := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_staff_transfers (
			id, call_id, practice_id, location_id, requested_by_subject,
			requested_by_session_id, recipient_subject, handoff_note, state,
			expires_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'REQUESTED', $9, $10, $10)
	`, transferID, command.CallID, practiceID, locationID,
		command.Identity.Subject, command.SessionID, command.RecipientSubject,
		command.HandoffNote, now.Add(30*time.Second), now,
	); err != nil {
		if isUniqueViolation(err) {
			return StaffTransfer{}, ErrConflict
		}
		return StaffTransfer{}, fmt.Errorf("create staff transfer: %w", err)
	}
	if err := appendTimeline(
		ctx, tx, command.CallID, practiceID, "transfer.requested",
		command.Identity.Subject, "", "", "", "", now,
	); err != nil {
		return StaffTransfer{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return StaffTransfer{}, err
	}
	transfer, err := readStaffTransfer(ctx, tx, transferID)
	if err != nil {
		return StaffTransfer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StaffTransfer{}, fmt.Errorf("commit staff transfer: %w", err)
	}
	return transfer, nil
}

func (m *Module) ListStaffTransfers(
	ctx context.Context,
	identity access.Identity,
) ([]StaffTransfer, error) {
	if m.pool == nil || strings.TrimSpace(identity.Subject) == "" {
		return nil, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin staff transfer list: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := m.expireStaffTransfers(ctx, tx, identity.Subject, m.now()); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, staffTransferSelect+`
		WHERE (transfer.requested_by_subject = $1 OR transfer.recipient_subject = $1)
			AND transfer.state IN ('REQUESTED', 'ACCEPTED')
		ORDER BY transfer.created_at, transfer.id
	`, identity.Subject)
	if err != nil {
		return nil, fmt.Errorf("query staff transfers: %w", err)
	}
	defer rows.Close()
	transfers := []StaffTransfer{}
	for rows.Next() {
		transfer, err := scanStaffTransfer(rows)
		if err != nil {
			return nil, err
		}
		transfers = append(transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate staff transfers: %w", err)
	}
	rows.Close()
	for _, transfer := range transfers {
		if _, err := m.access.LockReadAuthorization(
			ctx, tx, identity, transfer.PracticeID, transfer.LocationID,
		); err != nil {
			return nil, ErrDenied
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit staff transfer list: %w", err)
	}
	return transfers, nil
}

func (m *Module) AcceptStaffTransfer(
	ctx context.Context,
	command RespondStaffTransferCommand,
) (StaffTransfer, error) {
	command.TransferID = strings.TrimSpace(command.TransferID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	if m.pool == nil || command.TransferID == "" || command.SessionID == "" {
		return StaffTransfer{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return StaffTransfer{}, fmt.Errorf("begin staff transfer acceptance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := m.expireStaffTransfers(ctx, tx, command.Identity.Subject, m.now()); err != nil {
		return StaffTransfer{}, err
	}
	transfer, err := readStaffTransferForUpdate(ctx, tx, command.TransferID)
	if err != nil {
		return StaffTransfer{}, err
	}
	if transfer.State != StaffTransferRequested ||
		transfer.RecipientSubject != command.Identity.Subject {
		return StaffTransfer{}, ErrConflict
	}
	if _, err := m.access.LockReadAuthorization(
		ctx, tx, command.Identity, transfer.PracticeID, transfer.LocationID,
	); err != nil {
		return StaffTransfer{}, ErrDenied
	}
	now := m.now()
	var recipientReady bool
	if err := tx.QueryRow(ctx, `
		SELECT desired_available
			AND registered
			AND microphone_ready
			AND audio_ready
			AND session_healthy
			AND lease_expires_at > $3
		FROM human_calling_softphone_leases
		WHERE user_subject = $1 AND session_id = $2
		FOR UPDATE
	`, command.Identity.Subject, command.SessionID, now).Scan(&recipientReady); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StaffTransfer{}, ErrConflict
		}
		return StaffTransfer{}, fmt.Errorf("lock transfer recipient lease: %w", err)
	}
	if !recipientReady {
		return StaffTransfer{}, ErrConflict
	}
	candidates, err := transferCandidates(
		ctx, tx, transfer.PracticeID, transfer.LocationID,
		transfer.RequestedBySubject, now,
	)
	if err != nil {
		return StaffTransfer{}, err
	}
	eligible := false
	for _, candidate := range candidates {
		eligible = eligible || candidate.Subject == command.Identity.Subject
	}
	if !eligible {
		return StaffTransfer{}, ErrConflict
	}
	var sipUsername, callerControlID, callerID string
	if err := tx.QueryRow(ctx, `
		SELECT credential.provider_sip_username,
			call.caller_call_control_id,
			COALESCE(NULLIF(call.outbound_caller_id, ''), $3)
		FROM human_calling_credentials credential
		JOIN human_calling_calls call ON call.id = $1
		WHERE credential.user_subject = $2
			AND credential.state = 'ACTIVE'
			AND call.state = 'CONNECTED'
		FOR UPDATE OF call
	`, transfer.CallID, command.Identity.Subject, m.config.FromNumber).Scan(
		&sipUsername, &callerControlID, &callerID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StaffTransfer{}, ErrConflict
		}
		return StaffTransfer{}, fmt.Errorf("prepare staff transfer Dial: %w", err)
	}
	attemptID := uuid.NewString()
	commandID := uuid.NewString()
	payload := map[string]any{
		"to":                    managedSIPDestination(sipUsername, m.config.StaffSIPDomain),
		"connection_id":         m.config.CallControlID,
		"from":                  callerID,
		"link_to":               callerControlID,
		"bridge_intent":         true,
		"bridge_on_answer":      true,
		"prevent_double_bridge": true,
		"client_state":          opaqueClientState(transfer.CallID, "staff", attemptID),
		"timeout_secs":          int(m.config.ConnectionTimeout.Seconds()),
		"custom_headers": []map[string]string{{
			"name": "X-Acuity-Media-Token", "value": m.staffMediaToken(transfer.CallID, attemptID),
		}},
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_connection_attempts (
			id, call_id, claimant_subject, claimant_session_id,
			connection_deadline, staff_transfer_id, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
	`, attemptID, transfer.CallID, command.Identity.Subject, command.SessionID,
		now.Add(m.config.ConnectionTimeout), transfer.ID, now,
	); err != nil {
		return StaffTransfer{}, fmt.Errorf("create transfer connection attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			id, call_id, attempt_id, user_subject, action, target_id, payload, next_attempt_at
		)
		VALUES ($1, $2, $3, $4, 'DIAL_STAFF', $5, $6, $7)
	`, commandID, transfer.CallID, attemptID, command.Identity.Subject,
		callerControlID, payload, now,
	); err != nil {
		return StaffTransfer{}, fmt.Errorf("commit transfer Dial: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_staff_transfers
		SET state = 'ACCEPTED', recipient_session_id = $2,
			expires_at = $4, updated_at = $3
		WHERE id = $1 AND state = 'REQUESTED'
	`, transfer.ID, command.SessionID, now, now.Add(m.config.ConnectionTimeout)); err != nil {
		return StaffTransfer{}, fmt.Errorf("accept staff transfer: %w", err)
	}
	leaseUpdate, err := tx.Exec(ctx, `
		UPDATE human_calling_softphone_leases
		SET desired_available = false, version = version + 1, updated_at = $3
		WHERE user_subject = $1 AND session_id = $2 AND desired_available
	`, command.Identity.Subject, command.SessionID, now)
	if err != nil {
		return StaffTransfer{}, fmt.Errorf("reserve recipient softphone: %w", err)
	}
	if leaseUpdate.RowsAffected() != 1 {
		return StaffTransfer{}, ErrConflict
	}
	if err := appendTimeline(
		ctx, tx, transfer.CallID, transfer.PracticeID, "transfer.accepted",
		command.Identity.Subject, "", commandID, "", "", now,
	); err != nil {
		return StaffTransfer{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, transfer.PracticeID); err != nil {
		return StaffTransfer{}, err
	}
	transfer, err = readStaffTransfer(ctx, tx, transfer.ID)
	if err != nil {
		return StaffTransfer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StaffTransfer{}, fmt.Errorf("commit staff transfer acceptance: %w", err)
	}
	return transfer, nil
}

func (m *Module) DeclineStaffTransfer(
	ctx context.Context,
	command RespondStaffTransferCommand,
) (StaffTransfer, error) {
	return m.finishStaffTransferResponse(ctx, command, StaffTransferDeclined, true)
}

func (m *Module) CancelStaffTransfer(
	ctx context.Context,
	command RespondStaffTransferCommand,
) (StaffTransfer, error) {
	return m.finishStaffTransferResponse(ctx, command, StaffTransferCancelled, false)
}

func (m *Module) finishStaffTransferResponse(
	ctx context.Context,
	command RespondStaffTransferCommand,
	state StaffTransferState,
	recipient bool,
) (StaffTransfer, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return StaffTransfer{}, fmt.Errorf("begin staff transfer response: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	transfer, err := readStaffTransferForUpdate(ctx, tx, command.TransferID)
	if err != nil {
		return StaffTransfer{}, err
	}
	authorizedSubject := transfer.RequestedBySubject
	if recipient {
		authorizedSubject = transfer.RecipientSubject
	}
	validState := transfer.State == StaffTransferRequested
	if !recipient && transfer.State == StaffTransferAccepted {
		validState = true
	}
	if command.Identity.Subject != authorizedSubject || !validState {
		return StaffTransfer{}, ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_staff_transfers
		SET state = $2, updated_at = $3
		WHERE id = $1
	`, transfer.ID, state, m.now()); err != nil {
		return StaffTransfer{}, fmt.Errorf("record staff transfer response: %w", err)
	}
	if transfer.State == StaffTransferAccepted {
		var attemptID, staffControlID, recipientSession string
		if err := tx.QueryRow(ctx, `
			UPDATE human_calling_connection_attempts attempt
			SET ended_at = COALESCE(ended_at, $2), updated_at = $2
			FROM human_calling_staff_transfers transfer
			WHERE attempt.staff_transfer_id = transfer.id AND transfer.id = $1
			RETURNING attempt.id::text, COALESCE(attempt.staff_call_control_id, ''),
				COALESCE(transfer.recipient_session_id, '')
		`, transfer.ID, m.now()).Scan(
			&attemptID, &staffControlID, &recipientSession,
		); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return StaffTransfer{}, fmt.Errorf("end cancelled transfer attempt: %w", err)
		}
		if staffControlID != "" {
			if err := ensureHangupCommand(
				ctx, tx, transfer.CallID, attemptID, transfer.RecipientSubject,
				staffControlID, "staff", m.now(),
			); err != nil {
				return StaffTransfer{}, err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_softphone_leases
			SET desired_available = true, version = version + 1, updated_at = $3
			WHERE user_subject = $1 AND session_id = $2
		`, transfer.RecipientSubject, recipientSession, m.now()); err != nil {
			return StaffTransfer{}, fmt.Errorf("restore cancelled transfer recipient: %w", err)
		}
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, transfer.PracticeID); err != nil {
		return StaffTransfer{}, err
	}
	transfer, err = readStaffTransfer(ctx, tx, transfer.ID)
	if err != nil {
		return StaffTransfer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StaffTransfer{}, fmt.Errorf("commit staff transfer response: %w", err)
	}
	return transfer, nil
}

func (m *Module) finishStaffTransferDial(
	ctx context.Context,
	tx pgx.Tx,
	command ProviderCommand,
	callID string,
	practiceID string,
	result ProviderResult,
	executeErr error,
	commandState string,
) (bool, error) {
	var transferID string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(staff_transfer_id::text, '')
		FROM human_calling_connection_attempts
		WHERE id = $1 AND call_id = $2
	`, command.AttemptID, callID).Scan(&transferID); err != nil {
		return false, fmt.Errorf("identify staff transfer Dial: %w", err)
	}
	if transferID == "" {
		return false, nil
	}
	now := m.now()
	if executeErr == nil {
		if result.CallControlID == "" || result.CallLegID == "" {
			return true, fmt.Errorf("successful transfer Dial omitted provider leg identity")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_connection_attempts
			SET staff_call_control_id = COALESCE(staff_call_control_id, $2),
				staff_call_leg_id = COALESCE(staff_call_leg_id, $3),
				updated_at = $4
			WHERE id = $1
		`, command.AttemptID, result.CallControlID, result.CallLegID, now); err != nil {
			return true, fmt.Errorf("record transfer provider leg: %w", err)
		}
		return true, nil
	}
	if commandState == "AMBIGUOUS" {
		return true, nil
	}
	var recipientSubject, recipientSession string
	err := tx.QueryRow(ctx, `
		UPDATE human_calling_staff_transfers
		SET state = 'FAILED', updated_at = $2
		WHERE id = $1 AND state = 'ACCEPTED'
		RETURNING recipient_subject, COALESCE(recipient_session_id, '')
	`, transferID, now).Scan(&recipientSubject, &recipientSession)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return true, fmt.Errorf("fail rejected staff transfer: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_connection_attempts
		SET ended_at = COALESCE(ended_at, $2),
			provider_termination = 'TRANSFER_DIAL_REJECTED',
			updated_at = $2
		WHERE id = $1
	`, command.AttemptID, now); err != nil {
		return true, fmt.Errorf("end rejected transfer attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_softphone_leases
		SET desired_available = true, version = version + 1, updated_at = $3
		WHERE user_subject = $1 AND session_id = $2
	`, recipientSubject, recipientSession, now); err != nil {
		return true, fmt.Errorf("restore rejected transfer recipient: %w", err)
	}
	if err := appendTimeline(
		ctx, tx, callID, practiceID, "transfer.failed", recipientSubject,
		"", command.ID, "", "TRANSFER_DIAL_REJECTED", now,
	); err != nil {
		return true, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return true, err
	}
	return true, nil
}

func transferCandidates(
	ctx context.Context,
	tx pgx.Tx,
	practiceID, locationID, excludedSubject string,
	now time.Time,
) ([]TransferCandidate, error) {
	rows, err := tx.Query(ctx, `
		SELECT membership.user_subject, membership.email
		FROM access_memberships membership
		JOIN human_calling_softphone_leases lease
			ON lease.user_subject = membership.user_subject
		JOIN human_calling_credentials credential
			ON credential.user_subject = membership.user_subject
			AND credential.state = 'ACTIVE'
		WHERE membership.practice_id = $1
			AND membership.revoked_at IS NULL
			AND membership.user_subject <> $3
			AND (
				membership.location_scope = 'ALL'
				OR EXISTS (
					SELECT 1 FROM access_membership_locations scope
					WHERE scope.membership_id = membership.id AND scope.location_id = $2
				)
			)
			AND lease.lease_expires_at > $4
			AND lease.desired_available
			AND lease.registered
			AND lease.microphone_ready
			AND lease.audio_ready
			AND lease.session_healthy
			AND NOT EXISTS (
				SELECT 1 FROM human_calling_calls active
				WHERE (active.claimant_subject = membership.user_subject
					OR active.winner_subject = membership.user_subject)
					AND active.state IN (
						'PREPARING', 'RINGING', 'CONNECTING', 'CONNECTED', 'RECONCILING'
					)
			)
		ORDER BY membership.email, membership.user_subject
	`, practiceID, locationID, excludedSubject, now)
	if err != nil {
		return nil, fmt.Errorf("query transfer candidates: %w", err)
	}
	defer rows.Close()
	candidates := []TransferCandidate{}
	for rows.Next() {
		var candidate TransferCandidate
		if err := rows.Scan(&candidate.Subject, &candidate.Email); err != nil {
			return nil, fmt.Errorf("scan transfer candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transfer candidates: %w", err)
	}
	return candidates, nil
}

func (m *Module) expireStaffTransfers(
	ctx context.Context,
	tx pgx.Tx,
	subject string,
	now time.Time,
) error {
	rows, err := tx.Query(ctx, `
		SELECT id::text, call_id::text, practice_id::text, recipient_subject,
			COALESCE(recipient_session_id, ''), state
		FROM human_calling_staff_transfers
		WHERE (requested_by_subject = $1 OR recipient_subject = $1)
			AND state IN ('REQUESTED', 'ACCEPTED')
			AND expires_at <= $2
		FOR UPDATE
	`, subject, now)
	if err != nil {
		return fmt.Errorf("lock expired staff transfers: %w", err)
	}
	expired := []expiredStaffTransfer{}
	for rows.Next() {
		var transfer expiredStaffTransfer
		if err := rows.Scan(
			&transfer.id, &transfer.callID, &transfer.practiceID,
			&transfer.recipient, &transfer.session, &transfer.state,
		); err != nil {
			rows.Close()
			return fmt.Errorf("scan expired staff transfer: %w", err)
		}
		expired = append(expired, transfer)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate expired staff transfers: %w", err)
	}
	rows.Close()
	for _, transfer := range expired {
		if err := m.expireStaffTransfer(ctx, tx, transfer, now); err != nil {
			return err
		}
	}
	return nil
}

type expiredStaffTransfer struct {
	id, callID, practiceID, recipient, session string
	state                                      StaffTransferState
}

func (m *Module) expireNextStaffTransfer(ctx context.Context, now time.Time) (bool, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin expired staff transfer recovery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var transfer expiredStaffTransfer
	err = tx.QueryRow(ctx, `
		SELECT id::text, call_id::text, practice_id::text, recipient_subject,
			COALESCE(recipient_session_id, ''), state
		FROM human_calling_staff_transfers
		WHERE state IN ('REQUESTED', 'ACCEPTED') AND expires_at <= $1
		ORDER BY expires_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, now).Scan(
		&transfer.id, &transfer.callID, &transfer.practiceID,
		&transfer.recipient, &transfer.session, &transfer.state,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit empty staff transfer recovery: %w", err)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock next expired staff transfer: %w", err)
	}
	if err := m.expireStaffTransfer(ctx, tx, transfer, now); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit expired staff transfer recovery: %w", err)
	}
	return true, nil
}

func (m *Module) expireStaffTransfer(
	ctx context.Context,
	tx pgx.Tx,
	transfer expiredStaffTransfer,
	now time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_staff_transfers
		SET state = 'EXPIRED', updated_at = $2
		WHERE id = $1 AND state IN ('REQUESTED', 'ACCEPTED')
	`, transfer.id, now); err != nil {
		return fmt.Errorf("expire staff transfer: %w", err)
	}
	if transfer.state == StaffTransferAccepted {
		if err := m.endStaffTransferAttempt(ctx, tx, transfer, now); err != nil {
			return err
		}
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, transfer.practiceID); err != nil {
		return err
	}
	return nil
}

func (m *Module) endStaffTransferAttempt(
	ctx context.Context,
	tx pgx.Tx,
	transfer expiredStaffTransfer,
	now time.Time,
) error {
	var attemptID, staffControlID string
	err := tx.QueryRow(ctx, `
		UPDATE human_calling_connection_attempts
		SET ended_at = COALESCE(ended_at, $2), updated_at = $2
		WHERE staff_transfer_id = $1
		RETURNING id::text, COALESCE(staff_call_control_id, '')
	`, transfer.id, now).Scan(&attemptID, &staffControlID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("end staff transfer attempt: %w", err)
	}
	if staffControlID != "" {
		if err := ensureHangupCommand(
			ctx, tx, transfer.callID, attemptID, transfer.recipient,
			staffControlID, "staff", now,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_softphone_leases
		SET desired_available = true, version = version + 1, updated_at = $3
		WHERE user_subject = $1 AND session_id = $2
	`, transfer.recipient, transfer.session, now); err != nil {
		return fmt.Errorf("restore staff transfer recipient: %w", err)
	}
	return nil
}

func (m *Module) terminateStaffTransfersForCallerHangup(
	ctx context.Context,
	tx pgx.Tx,
	callID, practiceID string,
	now time.Time,
) error {
	rows, err := tx.Query(ctx, `
		SELECT id::text, call_id::text, practice_id::text, recipient_subject,
			COALESCE(recipient_session_id, ''), state
		FROM human_calling_staff_transfers
		WHERE call_id = $1 AND state IN ('REQUESTED', 'ACCEPTED')
		FOR UPDATE
	`, callID)
	if err != nil {
		return fmt.Errorf("lock transfers after caller hangup: %w", err)
	}
	transfers := []expiredStaffTransfer{}
	for rows.Next() {
		var transfer expiredStaffTransfer
		if err := rows.Scan(
			&transfer.id, &transfer.callID, &transfer.practiceID,
			&transfer.recipient, &transfer.session, &transfer.state,
		); err != nil {
			rows.Close()
			return fmt.Errorf("scan transfer after caller hangup: %w", err)
		}
		transfers = append(transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate transfers after caller hangup: %w", err)
	}
	rows.Close()
	for _, transfer := range transfers {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_staff_transfers
			SET state = 'CANCELLED', updated_at = $2
			WHERE id = $1 AND state IN ('REQUESTED', 'ACCEPTED')
		`, transfer.id, now); err != nil {
			return fmt.Errorf("cancel transfer after caller hangup: %w", err)
		}
		if transfer.state == StaffTransferAccepted {
			if err := m.endStaffTransferAttempt(ctx, tx, transfer, now); err != nil {
				return err
			}
		}
		if err := appendTimeline(
			ctx, tx, callID, practiceID, "transfer.caller_hangup",
			transfer.recipient, "", "", "", "CALLER_ENDED_CALL", now,
		); err != nil {
			return err
		}
	}
	return nil
}

const staffTransferSelect = `
	SELECT
		transfer.id::text,
		transfer.call_id::text,
		transfer.practice_id::text,
		transfer.location_id::text,
		location.name,
		COALESCE(handoff.phone, call.destination_phone, ''),
		COALESCE(handoff.display_name, ''),
		transfer.requested_by_subject,
		COALESCE(requester.email, ''),
		transfer.recipient_subject,
		COALESCE(recipient.email, ''),
		transfer.handoff_note,
		transfer.state,
		transfer.expires_at,
		transfer.created_at,
		transfer.completed_at
	FROM human_calling_staff_transfers transfer
	JOIN human_calling_calls call ON call.id = transfer.call_id
	JOIN access_locations location
		ON location.practice_id = transfer.practice_id AND location.id = transfer.location_id
	LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.handoff_id
	LEFT JOIN access_memberships requester
		ON requester.practice_id = transfer.practice_id
		AND requester.user_subject = transfer.requested_by_subject
	LEFT JOIN access_memberships recipient
		ON recipient.practice_id = transfer.practice_id
		AND recipient.user_subject = transfer.recipient_subject
`

func readStaffTransfer(ctx context.Context, tx pgx.Tx, transferID string) (StaffTransfer, error) {
	return scanStaffTransfer(tx.QueryRow(ctx, staffTransferSelect+` WHERE transfer.id = $1`, transferID))
}

func readStaffTransferForUpdate(
	ctx context.Context,
	tx pgx.Tx,
	transferID string,
) (StaffTransfer, error) {
	return scanStaffTransfer(tx.QueryRow(
		ctx,
		staffTransferSelect+` WHERE transfer.id = $1 FOR UPDATE OF transfer`,
		transferID,
	))
}

type transferScanner interface {
	Scan(...any) error
}

func scanStaffTransfer(scanner transferScanner) (StaffTransfer, error) {
	var transfer StaffTransfer
	if err := scanner.Scan(
		&transfer.ID,
		&transfer.CallID,
		&transfer.PracticeID,
		&transfer.LocationID,
		&transfer.LocationName,
		&transfer.Phone,
		&transfer.DisplayName,
		&transfer.RequestedBySubject,
		&transfer.RequestedByEmail,
		&transfer.RecipientSubject,
		&transfer.RecipientEmail,
		&transfer.HandoffNote,
		&transfer.State,
		&transfer.ExpiresAt,
		&transfer.CreatedAt,
		&transfer.CompletedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StaffTransfer{}, ErrDenied
		}
		return StaffTransfer{}, fmt.Errorf("scan staff transfer: %w", err)
	}
	return transfer, nil
}
