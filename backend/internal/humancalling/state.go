package humancalling

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

func callingScopeLocationFilter(scopeAlias, resourceAlias string) string {
	return `(` + scopeAlias + `.location_scope = 'ALL'
		OR EXISTS (
			SELECT 1 FROM access_membership_locations allowed
			WHERE allowed.membership_id = ` + scopeAlias + `.membership_id
				AND allowed.location_id = ` + resourceAlias + `.location_id
		)
	)`
}

type CallingState struct {
	Softphone   SoftphoneState
	Ringing     []RingingCallLeg
	Bridged     *CallingStateCall
	Voicemail   *CallingStateCall
	Disposition *CallingStateCall
	Transfers   []StaffTransfer
	PracticeIDs []string
	ETag        string
}

type RingingCallLeg struct {
	CallID          string
	CallLegID       string
	MediaToken      string
	PracticeID      string
	LocationID      string
	LocationName    string
	DisplayName     string
	Phone           string
	TransferReason  string
	State           string
	Version         int64
	CreatedAt       time.Time
	Deadline        time.Time
	OfferKind       string
	StaffTransferID string
	OriginatorEmail string
	HandoffNote     string
}

type CallingStateCall struct {
	CallID       string
	CallLegID    string
	PracticeID   string
	LocationID   string
	LocationName string
	State        string
	Version      int64
}

func (m *Module) ReadCallingStateConditionally(
	ctx context.Context,
	identity access.Identity,
	ifNoneMatch string,
) (CallingState, bool, error) {
	if identity.Subject == "" || !identity.EmailVerified {
		return CallingState{}, false, ErrDenied
	}
	discovery, err := m.access.DiscoverActorReadOnly(ctx, identity)
	if err != nil {
		return CallingState{}, false, callingAccessError(err)
	}
	if !hasOperationalCallingAccess(discovery) {
		return CallingState{}, false, ErrDenied
	}
	state, err := m.readCallingState(ctx, identity.Subject)
	if err != nil {
		return CallingState{}, false, err
	}
	state.PracticeIDs = []string{}
	for _, practice := range discovery.Practices {
		state.PracticeIDs = append(state.PracticeIDs, practice.ID)
	}
	state.ETag, err = callingStateETag(state)
	if err != nil {
		return CallingState{}, false, err
	}
	if ifNoneMatch == state.ETag {
		return CallingState{ETag: state.ETag}, true, nil
	}
	return state, false, nil
}

func callingStateETag(state CallingState) (string, error) {
	body, err := json.Marshal(withoutVolatileFields(state))
	if err != nil {
		return "", fmt.Errorf("encode Calling state validator: %w", err)
	}
	digest := sha256.Sum256(body)
	return `W/"` + base64.RawURLEncoding.EncodeToString(digest[:]) + `"`, nil
}

func withoutVolatileFields(state CallingState) CallingState {
	state.Softphone.LeaseExpiresAt = time.Time{}
	return state
}

func (m *Module) readCallingState(
	ctx context.Context,
	staffSubject string,
) (CallingState, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return CallingState{}, fmt.Errorf("begin Calling state read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	state := CallingState{Ringing: []RingingCallLeg{}, Transfers: []StaffTransfer{}}
	if err := tx.QueryRow(ctx, `
		SELECT session_id, lease_expires_at,
			lease_expires_at > $2,
			(desired_available AND registered AND microphone_ready
				AND audio_ready AND session_healthy
				AND lease_expires_at > $2)
		FROM human_calling_softphone_leases
		WHERE user_subject = $1
	`, staffSubject, m.now()).Scan(
		&state.Softphone.SessionID,
		&state.Softphone.LeaseExpiresAt,
		&state.Softphone.Owner,
		&state.Softphone.Available,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return state, nil
		}
		return CallingState{}, fmt.Errorf("read Calling lease state: %w", err)
	}
	if err := loadCurrentCallCapacity(ctx, tx, staffSubject, &state.Softphone); err != nil {
		return CallingState{}, err
	}

	rows, err := tx.Query(ctx, `
		SELECT call.id::text, leg.id::text, call.practice_id::text,
			call.location_id::text, location.name,
			COALESCE(handoff.display_name, ''),
			COALESCE(call.caller_phone, call.destination_phone, ''),
			COALESCE(handoff.transfer_reason, ''), leg.state,
			call.version, leg.created_at,
			COALESCE(transfer.expires_at, ring.sent_at + $2::interval),
			CASE WHEN transfer.id IS NULL THEN 'INBOUND_OFFER' ELSE 'STAFF_TRANSFER' END,
			COALESCE(transfer.id::text, ''),
			COALESCE(requester.email, operator.email, ''),
			COALESCE(transfer.handoff_note, '')
		FROM human_calling_call_legs leg
		JOIN human_calling_calls call ON call.id = leg.call_id
		JOIN access_locations location
			ON location.practice_id = call.practice_id
			AND location.id = call.location_id
		JOIN access_operational_scopes operational_scope
			ON operational_scope.practice_id = call.practice_id
			AND operational_scope.user_subject = leg.staff_subject
		LEFT JOIN human_calling_handoffs handoff
			ON handoff.id = call.source_handoff_id
		LEFT JOIN human_calling_staff_transfers transfer
			ON transfer.target_staff_leg_id = leg.id
		LEFT JOIN access_memberships requester
			ON requester.practice_id = call.practice_id
			AND requester.user_subject = transfer.requested_by_subject
		LEFT JOIN access_platform_operators operator
			ON operator.user_subject = transfer.requested_by_subject
		LEFT JOIN LATERAL (
			SELECT command.sent_at
			FROM human_calling_provider_commands command
			WHERE command.call_id = call.id
				AND (
					(call.direction = 'INBOUND' AND command.action = 'START_RING_WINDOW')
					OR (
						call.direction = 'OUTBOUND'
						AND command.action = 'DIAL_OUTBOUND_STAFF'
						AND command.call_leg_id = leg.id
					)
				)
				AND command.state IN ('SENT', 'RECONCILED')
				AND command.sent_at IS NOT NULL
			ORDER BY command.created_at, command.id
			LIMIT 1
		) ring ON true
		WHERE leg.role = 'STAFF'
			AND leg.staff_subject = $1
			AND leg.state IN ('PENDING', 'DIALING', 'RINGING', 'ANSWERED', 'BRIDGE_PENDING')
			AND (transfer.id IS NOT NULL OR ring.sent_at IS NOT NULL)
			AND (
				call.direction = 'OUTBOUND'
				OR operational_scope.role = 'STAFF'
				OR operational_scope.role IS NULL
			)
			AND `+callingScopeLocationFilter("operational_scope", "call")+`
		ORDER BY leg.created_at, leg.id
	`, staffSubject, m.config.RingWindowDuration.String())
	if err != nil {
		return CallingState{}, fmt.Errorf("read ringing CallLegs: %w", err)
	}
	for rows.Next() {
		var leg RingingCallLeg
		if err := rows.Scan(
			&leg.CallID, &leg.CallLegID, &leg.PracticeID, &leg.LocationID,
			&leg.LocationName, &leg.DisplayName, &leg.Phone, &leg.TransferReason,
			&leg.State, &leg.Version, &leg.CreatedAt, &leg.Deadline,
			&leg.OfferKind, &leg.StaffTransferID, &leg.OriginatorEmail,
			&leg.HandoffNote,
		); err != nil {
			rows.Close()
			return CallingState{}, fmt.Errorf("scan ringing CallLeg: %w", err)
		}
		leg.MediaToken = m.staffMediaToken(leg.CallID, leg.CallLegID)
		state.Ringing = append(state.Ringing, leg)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return CallingState{}, fmt.Errorf("iterate ringing CallLegs: %w", err)
	}
	rows.Close()

	state.Bridged, err = readStaffStateCall(ctx, tx, staffSubject, `
		leg.state = 'BRIDGED' AND call.terminal_outcome IS NULL
	`)
	if err != nil {
		return CallingState{}, err
	}
	state.Disposition, err = readStaffStateCall(ctx, tx, staffSubject, `
		leg.bridged_at IS NOT NULL AND call.terminal_outcome = 'ENDED'
		AND call.disposition_at IS NULL
		AND leg.id = (
			SELECT owner.call_leg_id
			FROM human_calling_current_staff_owners owner
			WHERE owner.call_id = call.id
		)
	`)
	if err != nil {
		return CallingState{}, err
	}
	state.Voicemail, err = readScopedVoicemailState(ctx, tx, staffSubject)
	if err != nil {
		return CallingState{}, err
	}
	transferRows, err := tx.Query(ctx, staffTransferSelect+`
		JOIN access_calling_scopes calling_scope
			ON calling_scope.practice_id = transfer.practice_id
			AND calling_scope.user_subject = $1
		WHERE (transfer.requested_by_subject = $1 OR transfer.recipient_subject = $1)
			AND transfer.state IN ('REQUESTED', 'ACCEPTED')
			AND `+callingScopeLocationFilter("calling_scope", "transfer")+`
		ORDER BY transfer.created_at, transfer.id
	`, staffSubject)
	if err != nil {
		return CallingState{}, fmt.Errorf("read active staff transfers: %w", err)
	}
	for transferRows.Next() {
		transfer, err := scanStaffTransfer(transferRows)
		if err != nil {
			transferRows.Close()
			return CallingState{}, err
		}
		state.Transfers = append(state.Transfers, transfer)
	}
	if err := transferRows.Err(); err != nil {
		transferRows.Close()
		return CallingState{}, err
	}
	transferRows.Close()

	return state, nil
}

func hasOperationalCallingAccess(discovery access.Discovery) bool {
	return len(discovery.Practices) > 0
}

func readStaffStateCall(
	ctx context.Context,
	tx pgx.Tx,
	staffSubject string,
	predicate string,
) (*CallingStateCall, error) {
	query := `
		SELECT call.id::text, leg.id::text, call.practice_id::text,
			call.location_id::text, location.name, leg.state, call.version
		FROM human_calling_call_legs leg
		JOIN human_calling_calls call ON call.id = leg.call_id
		JOIN access_locations location
			ON location.practice_id = call.practice_id
			AND location.id = call.location_id
		JOIN access_operational_scopes operational_scope
			ON operational_scope.practice_id = call.practice_id
			AND operational_scope.user_subject = leg.staff_subject
		WHERE leg.role = 'STAFF' AND leg.staff_subject = $1
			AND (
				call.direction = 'OUTBOUND'
				OR operational_scope.role = 'STAFF'
				OR operational_scope.role IS NULL
			)
			AND ` + callingScopeLocationFilter("operational_scope", "call") + `
			AND (` + predicate + `)
		ORDER BY call.updated_at DESC, call.id
		LIMIT 1
	`
	var result CallingStateCall
	err := tx.QueryRow(ctx, query, staffSubject).Scan(
		&result.CallID, &result.CallLegID, &result.PracticeID,
		&result.LocationID, &result.LocationName, &result.State, &result.Version,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read Staff Calling state: %w", err)
	}
	return &result, nil
}

func readScopedVoicemailState(
	ctx context.Context,
	tx pgx.Tx,
	staffSubject string,
) (*CallingStateCall, error) {
	var result CallingStateCall
	err := tx.QueryRow(ctx, `
		SELECT call.id::text, caller.id::text, call.practice_id::text,
			call.location_id::text, location.name,
			(
				SELECT call_state.state FROM human_calling_call_states call_state
				WHERE call_state.call_id = call.id
			),
			call.version
		FROM human_calling_calls call
		JOIN human_calling_call_legs caller
			ON caller.call_id = call.id AND caller.role = 'CALLER'
		JOIN access_locations location
			ON location.practice_id = call.practice_id
			AND location.id = call.location_id
		JOIN access_calling_scopes calling_scope
			ON calling_scope.practice_id = call.practice_id
			AND calling_scope.user_subject = $1
		WHERE (
			call.terminal_outcome = 'VOICEMAIL'
			OR (
				call.terminal_outcome IS NULL
				AND EXISTS (
					SELECT 1 FROM human_calling_provider_commands voicemail
					WHERE voicemail.call_id = call.id
						AND voicemail.action IN ('SPEAK_VOICEMAIL', 'START_VOICEMAIL_RECORDING')
						AND voicemail.state IN ('PENDING', 'SENDING', 'SENT', 'AMBIGUOUS', 'RECONCILED')
				)
			)
		)
			AND call.disposition_at IS NULL
			AND `+callingScopeLocationFilter("calling_scope", "call")+`
		ORDER BY call.updated_at DESC, call.id
		LIMIT 1
	`, staffSubject).Scan(
		&result.CallID, &result.CallLegID, &result.PracticeID,
		&result.LocationID, &result.LocationName, &result.State, &result.Version,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read scoped voicemail state: %w", err)
	}
	return &result, nil
}
