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
	StaffTransferExpired   StaffTransferState = "EXPIRED"
	StaffTransferCanceled  StaffTransferState = "CANCELED"
	StaffTransferFailed    StaffTransferState = "FAILED"
)

const (
	staffTransferTargetKind = "staff_transfer_target"
	staffTransferSourceKind = "staff_transfer_source"
)

type staffTransferResponder uint8

const (
	staffTransferOriginator staffTransferResponder = iota
	staffTransferRecipient
)

type staffTransferAvailability uint8

const (
	leaveTransferTargetAvailability staffTransferAvailability = iota
	restoreTransferTargetAvailability
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
	SourceCallLegID    string
	TargetCallLegID    string
	CustomerCallLegID  string
	ProviderCommandID  string
	RequestedBySubject string
	RequestedByEmail   string
	RecipientSubject   string
	RecipientEmail     string
	HandoffNote        string
	State              StaffTransferState
	ExpiresAt          time.Time
	CreatedAt          time.Time
	TargetAnsweredAt   *time.Time
	BridgeObservedAt   *time.Time
	CompletedAt        *time.Time
	FailureCode        string
}

type RequestStaffTransferCommand struct {
	Identity         access.Identity
	CallID           string
	SessionID        string
	RecipientSubject string
	IdempotencyKey   string
	HandoffNote      string
	ExpectedVersion  int64
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
	sessionID string,
) ([]TransferCandidate, error) {
	callID = strings.TrimSpace(callID)
	sessionID = strings.TrimSpace(sessionID)
	if callID == "" || sessionID == "" || identity.Subject == "" {
		return nil, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin transfer candidate lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	practiceID, locationID, _, err := m.lockTransferSource(
		ctx, tx, identity, callID, sessionID,
	)
	if err != nil {
		return nil, err
	}
	candidates, err := m.transferCandidates(
		ctx, tx, practiceID, locationID, identity.Subject,
	)
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
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.HandoffNote = strings.TrimSpace(command.HandoffNote)
	if command.Identity.Subject == "" || command.CallID == "" ||
		command.SessionID == "" || command.RecipientSubject == "" ||
		command.IdempotencyKey == "" || len(command.IdempotencyKey) > 200 ||
		len(command.HandoffNote) > 500 || command.ExpectedVersion <= 0 {
		return StaffTransfer{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return StaffTransfer{}, fmt.Errorf("begin staff transfer request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if existing, found, err := m.idempotentStaffTransfer(ctx, tx, command); err != nil {
		return StaffTransfer{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return StaffTransfer{}, err
		}
		return existing, nil
	}
	practiceID, locationID, currentVersion, err := m.lockTransferSource(
		ctx, tx, command.Identity, command.CallID, command.SessionID,
	)
	if err != nil {
		return StaffTransfer{}, err
	}
	// A same-key request can commit while this transaction waits for the Call.
	// Re-read behind the Call lock before applying the stale expected version.
	if existing, found, err := m.idempotentStaffTransfer(ctx, tx, command); err != nil {
		return StaffTransfer{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return StaffTransfer{}, err
		}
		return existing, nil
	}
	if currentVersion != command.ExpectedVersion {
		return StaffTransfer{}, ErrConflict
	}

	candidates, err := m.transferCandidates(
		ctx, tx, practiceID, locationID, command.Identity.Subject,
	)
	if err != nil {
		return StaffTransfer{}, err
	}
	eligible := false
	for _, candidate := range candidates {
		if candidate.Subject == command.RecipientSubject {
			eligible = true
			break
		}
	}
	if !eligible {
		return StaffTransfer{}, ErrIneligible
	}

	var sourceLegID, customerLegID, customerRole, customerControlID string
	if err := tx.QueryRow(ctx, `
		SELECT source.id::text, customer.id::text, customer.role,
			customer.provider_call_control_id
		FROM human_calling_calls call
		JOIN human_calling_call_legs source ON source.call_id = call.id
			AND source.role = 'STAFF' AND source.staff_subject = $2
			AND source.state = 'BRIDGED'
		JOIN human_calling_call_legs customer ON customer.call_id = call.id
			AND customer.role = CASE call.direction
				WHEN 'INBOUND' THEN 'CALLER' ELSE 'DESTINATION' END
			AND customer.state = 'BRIDGED'
		WHERE call.id = $1 AND call.terminal_outcome IS NULL
		FOR UPDATE OF source, customer
	`, command.CallID, command.Identity.Subject).Scan(
		&sourceLegID, &customerLegID, &customerRole, &customerControlID,
	); err != nil {
		return StaffTransfer{}, ErrConflict
	}
	var recipientSession, sipUsername string
	if err := tx.QueryRow(ctx, `
		SELECT lease.session_id, credential.provider_sip_username
		FROM human_calling_softphone_leases lease
		JOIN human_calling_credentials credential
			ON credential.user_subject = lease.user_subject
		WHERE lease.user_subject = $1 AND lease.desired_available
			AND lease.registered AND lease.microphone_ready AND lease.audio_ready
			AND lease.session_healthy AND lease.lease_expires_at > $2
			AND lease.readiness_updated_at > $2 - $3::interval
			AND credential.state = 'ACTIVE'
		FOR UPDATE OF lease
	`, command.RecipientSubject, m.now(), m.config.ReadinessGrace.String()).Scan(
		&recipientSession, &sipUsername,
	); err != nil {
		return StaffTransfer{}, ErrIneligible
	}

	transferID := uuid.NewString()
	targetLegID := uuid.NewString()
	var targetSequence int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(max(sequence), 0) + 1
		FROM human_calling_call_legs
		WHERE call_id = $1 AND role = 'STAFF'
	`, command.CallID).Scan(&targetSequence); err != nil {
		return StaffTransfer{}, err
	}
	now := m.now()
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_call_legs (
			id, call_id, role, sequence, staff_subject, staff_session_id,
			state, created_at, updated_at
		) VALUES ($1, $2, 'STAFF', $3, $4, $5, 'PENDING', $6, $6)
	`, targetLegID, command.CallID, targetSequence, command.RecipientSubject,
		recipientSession, now); err != nil {
		return StaffTransfer{}, fmt.Errorf("create transfer target CallLeg: %w", err)
	}
	targetClientState := encodeStaffTransferClientState(
		command.CallID, targetLegID, "STAFF", staffTransferTargetKind, transferID,
	)
	commandID, err := m.insertCallLegCommand(
		ctx, tx, command.CallID, targetLegID, customerLegID,
		command.RecipientSubject, CommandTransferStaff, customerControlID,
		map[string]any{
			"to":           managedSIPDestination(sipUsername, m.config.StaffSIPDomain),
			"early_media":  false,
			"timeout_secs": int(m.config.StaffTransferDuration.Seconds()),
			"client_state": encodeStaffTransferClientState(
				command.CallID, customerLegID, customerRole,
				staffTransferSourceKind, transferID,
			),
			"target_leg_client_state": targetClientState,
			"webhook_retries_policies": telnyxWebhookRetryPolicies(
				FactCallInitiated, FactCallAnswered, FactCallBridged, FactCallHangup,
			),
			"custom_headers": []map[string]string{{
				"name":  "X-Acuity-Media-Token",
				"value": m.staffMediaToken(command.CallID, targetLegID),
			}},
		}, "",
	)
	if err != nil {
		return StaffTransfer{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_staff_transfers (
			id, call_id, practice_id, location_id, source_staff_leg_id,
			target_staff_leg_id, customer_leg_id, provider_command_id,
			requested_by_subject, requested_by_session_id, recipient_subject,
			recipient_session_id, idempotency_key, handoff_note, state,
			expires_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			$13, $14, 'REQUESTED', $15, $16, $16
		)
	`, transferID, command.CallID, practiceID, locationID, sourceLegID,
		targetLegID, customerLegID, commandID, command.Identity.Subject,
		command.SessionID, command.RecipientSubject, recipientSession,
		command.IdempotencyKey, command.HandoffNote,
		now.Add(m.config.StaffTransferDuration), now); err != nil {
		return StaffTransfer{}, fmt.Errorf("create staff transfer: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET version = version + 1, updated_at = $2 WHERE id = $1
	`, command.CallID, now); err != nil {
		return StaffTransfer{}, err
	}
	if err := appendTimeline(ctx, tx, command.CallID, practiceID,
		"staff_transfer.requested", command.Identity.Subject, "", commandID,
		opaqueReference(transferID), "", now); err != nil {
		return StaffTransfer{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return StaffTransfer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StaffTransfer{}, fmt.Errorf("commit staff transfer request: %w", err)
	}
	// Transfer is latency-sensitive. The stable command is issued immediately;
	// any interruption or uncertain response remains durable for worker repair.
	_, _ = m.processCommand(ctx, commandID)
	current, err := scanStaffTransfer(m.database.QueryRow(
		ctx, staffTransferSelect+` WHERE transfer.id = $1`, transferID,
	))
	if err != nil {
		return StaffTransfer{}, fmt.Errorf(
			"read transfer after immediate command: %w", err,
		)
	}
	return current, nil
}

func (m *Module) idempotentStaffTransfer(
	ctx context.Context,
	tx pgx.Tx,
	command RequestStaffTransferCommand,
) (StaffTransfer, bool, error) {
	var existingID, existingSession string
	err := tx.QueryRow(ctx, `
		SELECT id::text, requested_by_session_id
		FROM human_calling_staff_transfers
		WHERE requested_by_subject = $1 AND idempotency_key = $2
	`, command.Identity.Subject, command.IdempotencyKey).Scan(
		&existingID, &existingSession,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return StaffTransfer{}, false, nil
	}
	if err != nil {
		return StaffTransfer{}, false, fmt.Errorf("read idempotent staff transfer: %w", err)
	}
	existing, err := readStaffTransfer(ctx, tx, existingID)
	if err != nil {
		return StaffTransfer{}, false, err
	}
	if existing.CallID != command.CallID ||
		existing.RecipientSubject != command.RecipientSubject ||
		existing.HandoffNote != command.HandoffNote ||
		existingSession != command.SessionID {
		return StaffTransfer{}, false, ErrConflict
	}
	if _, err := m.access.LockMembershipAuthorization(
		ctx, tx, command.Identity, existing.PracticeID, existing.LocationID,
	); err != nil {
		return StaffTransfer{}, false, ErrDenied
	}
	return existing, true, nil
}

func (m *Module) DeclineStaffTransfer(
	ctx context.Context,
	command RespondStaffTransferCommand,
) (StaffTransfer, error) {
	return m.respondStaffTransfer(
		ctx, command, StaffTransferDeclined, staffTransferRecipient,
	)
}

func (m *Module) CancelStaffTransfer(
	ctx context.Context,
	command RespondStaffTransferCommand,
) (StaffTransfer, error) {
	return m.respondStaffTransfer(
		ctx, command, StaffTransferCanceled, staffTransferOriginator,
	)
}

func (m *Module) respondStaffTransfer(
	ctx context.Context,
	command RespondStaffTransferCommand,
	state StaffTransferState,
	responder staffTransferResponder,
) (StaffTransfer, error) {
	command.TransferID = strings.TrimSpace(command.TransferID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	if command.Identity.Subject == "" || command.TransferID == "" || command.SessionID == "" {
		return StaffTransfer{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return StaffTransfer{}, fmt.Errorf("begin staff transfer response: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	transfer, err := readStaffTransfer(ctx, tx, command.TransferID)
	if err != nil {
		return StaffTransfer{}, err
	}
	if _, err := tx.Exec(ctx, `
		SELECT id FROM human_calling_calls WHERE id = $1 FOR UPDATE
	`, transfer.CallID); err != nil {
		return StaffTransfer{}, fmt.Errorf("lock transfer Call response: %w", err)
	}
	transfer, err = readStaffTransferForUpdate(ctx, tx, command.TransferID)
	if err != nil {
		return StaffTransfer{}, err
	}
	actorSubject := transfer.RequestedBySubject
	if responder == staffTransferRecipient {
		actorSubject = transfer.RecipientSubject
	}
	respondable := transfer.State == StaffTransferRequested ||
		(responder == staffTransferOriginator && transfer.State == StaffTransferAccepted)
	if command.Identity.Subject != actorSubject || !respondable {
		return StaffTransfer{}, ErrConflict
	}
	if responder == staffTransferRecipient {
		var expectedSession string
		if err := tx.QueryRow(ctx, `
			SELECT recipient_session_id
			FROM human_calling_staff_transfers WHERE id = $1
		`, transfer.ID).Scan(&expectedSession); err != nil || expectedSession != command.SessionID {
			return StaffTransfer{}, ErrConflict
		}
	}
	var ownsSession bool
	if err := tx.QueryRow(ctx, `
		SELECT session_id = $2 AND lease_expires_at > $3
		FROM human_calling_softphone_leases
		WHERE user_subject = $1 FOR UPDATE
	`, actorSubject, command.SessionID, m.now()).Scan(&ownsSession); err != nil || !ownsSession {
		return StaffTransfer{}, ErrConflict
	}
	if _, err := m.access.LockMembershipAuthorization(
		ctx, tx, command.Identity, transfer.PracticeID, transfer.LocationID,
	); err != nil {
		return StaffTransfer{}, ErrDenied
	}
	availability := leaveTransferTargetAvailability
	if responder == staffTransferOriginator {
		availability = restoreTransferTargetAvailability
	}
	if err := m.failStaffTransferTx(
		ctx, tx, transfer.ID, state, "STAFF_"+string(state), availability,
	); err != nil {
		return StaffTransfer{}, err
	}
	updated, err := readStaffTransfer(ctx, tx, transfer.ID)
	if err != nil {
		return StaffTransfer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StaffTransfer{}, fmt.Errorf("commit staff transfer response: %w", err)
	}
	return updated, nil
}

func (m *Module) ExpireStaffTransfers(ctx context.Context) (int, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin staff transfer expiry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var transferID string
	err = tx.QueryRow(ctx, `
		SELECT transfer.id::text
		FROM human_calling_staff_transfers transfer
		JOIN human_calling_calls call ON call.id = transfer.call_id
		WHERE transfer.state IN ('REQUESTED', 'ACCEPTED')
			AND transfer.expires_at <= $1
		ORDER BY transfer.expires_at, transfer.id
		FOR UPDATE OF call SKIP LOCKED
		LIMIT 1
	`, m.now()).Scan(&transferID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, tx.Commit(ctx)
	}
	if err != nil {
		return 0, fmt.Errorf("lock expired staff transfer: %w", err)
	}
	var stillExpired bool
	if err := tx.QueryRow(ctx, `
		SELECT state IN ('REQUESTED', 'ACCEPTED') AND expires_at <= $2
		FROM human_calling_staff_transfers
		WHERE id = $1
		FOR UPDATE
	`, transferID, m.now()).Scan(&stillExpired); err != nil {
		return 0, fmt.Errorf("lock expired staff transfer state: %w", err)
	}
	if !stillExpired {
		return 0, tx.Commit(ctx)
	}
	if err := m.failStaffTransferTx(
		ctx, tx, transferID, StaffTransferExpired, "TRANSFER_TIMEOUT",
		restoreTransferTargetAvailability,
	); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit staff transfer expiry: %w", err)
	}
	return 1, nil
}

func (m *Module) lockTransferSource(
	ctx context.Context,
	tx pgx.Tx,
	identity access.Identity,
	callID string,
	sessionID string,
) (string, string, int64, error) {
	var practiceID, locationID, ownerSession string
	var version int64
	if err := tx.QueryRow(ctx, `
		SELECT call.practice_id::text, call.location_id::text, call.version,
			COALESCE(source.staff_session_id, '')
		FROM human_calling_calls call
		JOIN human_calling_call_legs source ON source.call_id = call.id
			AND source.role = 'STAFF' AND source.staff_subject = $2
			AND source.state = 'BRIDGED'
		WHERE call.id = $1 AND call.terminal_outcome IS NULL
		FOR UPDATE OF call, source
	`, callID, identity.Subject).Scan(
		&practiceID, &locationID, &version, &ownerSession,
	); err != nil {
		return "", "", 0, ErrConflict
	}
	if ownerSession != sessionID {
		return "", "", 0, ErrConflict
	}
	if _, err := m.access.LockMembershipAuthorization(
		ctx, tx, identity, practiceID, locationID,
	); err != nil {
		return "", "", 0, ErrDenied
	}
	var ownsLease bool
	if err := tx.QueryRow(ctx, `
		SELECT session_id = $2 AND lease_expires_at > $3
		FROM human_calling_softphone_leases
		WHERE user_subject = $1 FOR UPDATE
	`, identity.Subject, sessionID, m.now()).Scan(&ownsLease); err != nil || !ownsLease {
		return "", "", 0, ErrConflict
	}
	return practiceID, locationID, version, nil
}

func (m *Module) transferCandidates(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	locationID string,
	excludedSubject string,
) ([]TransferCandidate, error) {
	rows, err := tx.Query(ctx, `
		SELECT membership.user_subject, membership.email
		FROM access_memberships membership
		JOIN human_calling_softphone_leases lease
			ON lease.user_subject = membership.user_subject
		JOIN human_calling_credentials credential
			ON credential.user_subject = membership.user_subject
		WHERE membership.practice_id = $1 AND membership.role = 'STAFF'
			AND membership.revoked_at IS NULL
			AND membership.user_subject <> $3
			AND NOT EXISTS (
				SELECT 1 FROM access_platform_operators operator
				WHERE operator.user_subject = membership.user_subject
					OR operator.email = membership.email
			)
			AND (membership.location_scope = 'ALL' OR EXISTS (
				SELECT 1 FROM access_membership_locations allowed
				WHERE allowed.membership_id = membership.id AND allowed.location_id = $2
			))
			AND lease.desired_available AND lease.registered
			AND lease.microphone_ready AND lease.audio_ready AND lease.session_healthy
			AND lease.lease_expires_at > $4
			AND lease.readiness_updated_at > $4 - $5::interval
			AND credential.state = 'ACTIVE'
			AND credential.provider_sip_username IS NOT NULL
			AND NOT EXISTS (
				SELECT 1 FROM human_calling_call_legs occupied
				WHERE occupied.staff_subject = membership.user_subject
					AND (occupied.state IN ('ANSWERED', 'BRIDGE_PENDING', 'BRIDGED')
						OR (occupied.state = 'ENDING' AND occupied.answered_at IS NOT NULL))
			)
		ORDER BY membership.email, membership.user_subject
	`, practiceID, locationID, excludedSubject, m.now(), m.config.ReadinessGrace.String())
	if err != nil {
		return nil, fmt.Errorf("query transfer candidates: %w", err)
	}
	defer rows.Close()
	result := []TransferCandidate{}
	for rows.Next() {
		var candidate TransferCandidate
		if err := rows.Scan(&candidate.Subject, &candidate.Email); err != nil {
			return nil, fmt.Errorf("scan transfer candidate: %w", err)
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transfer candidates: %w", err)
	}
	return result, nil
}

const staffTransferSelect = `
	SELECT transfer.id::text, transfer.call_id::text,
		transfer.practice_id::text, transfer.location_id::text, location.name,
		transfer.source_staff_leg_id::text, transfer.target_staff_leg_id::text,
		transfer.customer_leg_id::text, transfer.provider_command_id::text,
		transfer.requested_by_subject,
		COALESCE((SELECT email FROM access_memberships membership
			WHERE membership.practice_id = transfer.practice_id
				AND membership.user_subject = transfer.requested_by_subject LIMIT 1), ''),
		transfer.recipient_subject,
		COALESCE((SELECT email FROM access_memberships membership
			WHERE membership.practice_id = transfer.practice_id
				AND membership.user_subject = transfer.recipient_subject LIMIT 1), ''),
		transfer.handoff_note, transfer.state, transfer.expires_at,
		transfer.created_at, transfer.target_answered_at,
		transfer.bridge_observed_at, transfer.completed_at,
		COALESCE(transfer.failure_code, '')
	FROM human_calling_staff_transfers transfer
	JOIN access_locations location ON location.practice_id = transfer.practice_id
		AND location.id = transfer.location_id
`

type transferScanner interface{ Scan(...any) error }

func scanStaffTransfer(scanner transferScanner) (StaffTransfer, error) {
	var transfer StaffTransfer
	if err := scanner.Scan(
		&transfer.ID, &transfer.CallID, &transfer.PracticeID, &transfer.LocationID,
		&transfer.LocationName, &transfer.SourceCallLegID, &transfer.TargetCallLegID,
		&transfer.CustomerCallLegID, &transfer.ProviderCommandID,
		&transfer.RequestedBySubject, &transfer.RequestedByEmail,
		&transfer.RecipientSubject, &transfer.RecipientEmail, &transfer.HandoffNote,
		&transfer.State, &transfer.ExpiresAt, &transfer.CreatedAt,
		&transfer.TargetAnsweredAt, &transfer.BridgeObservedAt,
		&transfer.CompletedAt, &transfer.FailureCode,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StaffTransfer{}, ErrDenied
		}
		return StaffTransfer{}, fmt.Errorf("scan staff transfer: %w", err)
	}
	return transfer, nil
}

func readStaffTransfer(ctx context.Context, tx pgx.Tx, id string) (StaffTransfer, error) {
	return scanStaffTransfer(tx.QueryRow(ctx, staffTransferSelect+` WHERE transfer.id = $1`, id))
}

func readStaffTransferForUpdate(
	ctx context.Context,
	tx pgx.Tx,
	id string,
) (StaffTransfer, error) {
	return scanStaffTransfer(tx.QueryRow(
		ctx, staffTransferSelect+` WHERE transfer.id = $1 FOR UPDATE OF transfer`, id,
	))
}
