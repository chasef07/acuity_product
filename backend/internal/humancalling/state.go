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

type CallingState struct {
	Softphone   SoftphoneState
	Ringing     []RingingCallLeg
	Bridged     *CallingStateCall
	Voicemail   *CallingStateCall
	Disposition *CallingStateCall
	Transfers   []StaffTransfer
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

type callingAccessSnapshot struct {
	ActorSubject     string                  `json:"actorSubject"`
	PlatformOperator bool                    `json:"platformOperator"`
	Practices        []callingPracticeAccess `json:"practices"`
}

type callingPracticeAccess struct {
	PracticeID     string             `json:"practiceId"`
	Membership     *access.Membership `json:"membership,omitempty"`
	Locations      []access.Location  `json:"locations"`
	CallingEnabled bool               `json:"callingEnabled"`
}

func callingAccessState(discovery access.Discovery) callingAccessSnapshot {
	practices := make([]callingPracticeAccess, 0, len(discovery.Practices))
	for _, practice := range discovery.Practices {
		practices = append(practices, callingPracticeAccess{
			PracticeID:     practice.ID,
			Membership:     practice.Membership,
			Locations:      practice.Locations,
			CallingEnabled: practice.CallingEnabled,
		})
	}
	return callingAccessSnapshot{
		ActorSubject:     discovery.Actor.Subject,
		PlatformOperator: discovery.PlatformOperator,
		Practices:        practices,
	}
}

func (m *Module) ReadCallingState(
	ctx context.Context,
	identity access.Identity,
) (CallingState, error) {
	state, _, err := m.ReadCallingStateConditionally(ctx, identity, "")
	return state, err
}

// ReadCallingStateConditionally checks the authoritative state token before
// loading the full Calling projection. A matching validator returns only the
// validator and does not execute the multi-query projection read.
func (m *Module) ReadCallingStateConditionally(
	ctx context.Context,
	identity access.Identity,
	ifNoneMatch string,
) (CallingState, bool, error) {
	if identity.Subject == "" || !identity.EmailVerified {
		return CallingState{}, false, ErrDenied
	}
	discovery, err := m.access.DiscoverActor(ctx, identity)
	if err != nil || !hasOperationalCallingAccess(discovery) {
		return CallingState{}, false, ErrDenied
	}
	etag, err := m.readCallingStateETag(ctx, identity.Subject, discovery)
	if err != nil {
		return CallingState{}, false, err
	}
	if ifNoneMatch != "" && ifNoneMatch == etag {
		return CallingState{ETag: etag}, true, nil
	}
	state, err := m.readCallingState(ctx, identity.Subject)
	if err != nil {
		return CallingState{}, false, err
	}
	state.ETag = etag
	return state, false, nil
}

func (m *Module) readCallingState(
	ctx context.Context,
	staffSubject string,
) (CallingState, error) {
	state := CallingState{Ringing: []RingingCallLeg{}, Transfers: []StaffTransfer{}}
	if err := m.database.QueryRow(ctx, `
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
	if err := m.loadCurrentCallCapacity(ctx, staffSubject, &state.Softphone); err != nil {
		return CallingState{}, err
	}

	rows, err := m.database.Query(ctx, `
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
			AND (
				operational_scope.location_scope = 'ALL'
				OR EXISTS (
					SELECT 1 FROM access_membership_locations allowed
					WHERE allowed.membership_id = operational_scope.membership_id
						AND allowed.location_id = call.location_id
				)
			)
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

	state.Bridged, err = m.readStaffStateCall(ctx, staffSubject, `
		leg.state = 'BRIDGED' AND call.terminal_outcome IS NULL
	`)
	if err != nil {
		return CallingState{}, err
	}
	state.Disposition, err = m.readStaffStateCall(ctx, staffSubject, `
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
	state.Voicemail, err = m.readScopedVoicemailState(ctx, staffSubject)
	if err != nil {
		return CallingState{}, err
	}
	transferRows, err := m.database.Query(ctx, staffTransferSelect+`
		JOIN access_calling_scopes calling_scope
			ON calling_scope.practice_id = transfer.practice_id
			AND calling_scope.user_subject = $1
		WHERE (transfer.requested_by_subject = $1 OR transfer.recipient_subject = $1)
			AND transfer.state IN ('REQUESTED', 'ACCEPTED')
			AND (
				calling_scope.location_scope = 'ALL'
				OR EXISTS (
					SELECT 1 FROM access_membership_locations allowed
					WHERE allowed.membership_id = calling_scope.membership_id
						AND allowed.location_id = transfer.location_id
				)
			)
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
	for _, practice := range discovery.Practices {
		if practice.CallingEnabled {
			return true
		}
	}
	return false
}

func (m *Module) readStaffStateCall(
	ctx context.Context,
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
			AND (
				operational_scope.location_scope = 'ALL'
				OR EXISTS (
					SELECT 1 FROM access_membership_locations allowed
					WHERE allowed.membership_id = operational_scope.membership_id
						AND allowed.location_id = call.location_id
				)
			)
			AND (` + predicate + `)
		ORDER BY call.updated_at DESC, call.id
		LIMIT 1
	`
	var result CallingStateCall
	err := m.database.QueryRow(ctx, query, staffSubject).Scan(
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

func (m *Module) readScopedVoicemailState(
	ctx context.Context,
	staffSubject string,
) (*CallingStateCall, error) {
	var result CallingStateCall
	err := m.database.QueryRow(ctx, `
		SELECT call.id::text, caller.id::text, call.practice_id::text,
			call.location_id::text, location.name,
			CASE
				WHEN call.terminal_outcome = 'VOICEMAIL' THEN 'VOICEMAIL'
				WHEN EXISTS (
					SELECT 1 FROM human_calling_provider_commands recording
					WHERE recording.call_id = call.id
						AND recording.action = 'START_VOICEMAIL_RECORDING'
						AND recording.state IN ('PENDING', 'SENDING', 'SENT', 'AMBIGUOUS', 'RECONCILED')
				) THEN 'VOICEMAIL_RECORDING'
				ELSE 'VOICEMAIL_GREETING'
			END,
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
			AND (
				calling_scope.location_scope = 'ALL'
				OR EXISTS (
					SELECT 1 FROM access_membership_locations allowed
					WHERE allowed.membership_id = calling_scope.membership_id
						AND allowed.location_id = call.location_id
				)
			)
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

func (m *Module) readCallingStateETag(
	ctx context.Context,
	staffSubject string,
	discovery access.Discovery,
) (string, error) {
	practiceIDs := make([]string, 0, len(discovery.Practices))
	locationIDs := make([]string, 0)
	for _, practice := range discovery.Practices {
		if practice.CallingEnabled {
			practiceIDs = append(practiceIDs, practice.ID)
			for _, location := range practice.Locations {
				locationIDs = append(locationIDs, location.ID)
			}
		}
	}
	accessSnapshot, err := json.Marshal(callingAccessState(discovery))
	if err != nil {
		return "", fmt.Errorf("encode Calling access state: %w", err)
	}

	// Only the newest scoped voicemail is visible. Match that projection before
	// loading CallLegs and commands so older voicemail history cannot amplify
	// every poll. Location and handoff values are immutable after Call admission
	// or versioned by Discovery.
	// Provider-command rows remain part of the token because command acceptance
	// can change ringing or voicemail state before another Call mutation occurs.
	var callingSnapshot string
	if err := m.database.QueryRow(ctx, `
		WITH relevant_call_ids AS MATERIALIZED (
			(
			SELECT call.id
			FROM human_calling_calls call
			JOIN human_calling_call_legs caller
				ON caller.call_id = call.id AND caller.role = 'CALLER'
			JOIN access_calling_scopes calling_scope
				ON calling_scope.practice_id = call.practice_id
				AND calling_scope.user_subject = $1
			WHERE call.practice_id = ANY($2::uuid[])
				AND call.location_id = ANY($3::uuid[])
				AND call.disposition_at IS NULL
				AND (
					call.terminal_outcome = 'VOICEMAIL'
					OR (
						call.terminal_outcome IS NULL
						AND EXISTS (
							SELECT 1
							FROM human_calling_provider_commands voicemail
							WHERE voicemail.call_id = call.id
								AND voicemail.action IN (
									'SPEAK_VOICEMAIL', 'START_VOICEMAIL_RECORDING'
								)
								AND voicemail.state IN (
									'PENDING', 'SENDING', 'SENT', 'AMBIGUOUS', 'RECONCILED'
								)
						)
					)
				)
				AND (
					calling_scope.location_scope = 'ALL'
					OR EXISTS (
						SELECT 1 FROM access_membership_locations allowed
						WHERE allowed.membership_id = calling_scope.membership_id
							AND allowed.location_id = call.location_id
					)
				)
			ORDER BY call.updated_at DESC, call.id
			LIMIT 1
			)
			UNION
			SELECT own_leg.call_id
			FROM human_calling_call_legs own_leg
			JOIN human_calling_calls call ON call.id = own_leg.call_id
			WHERE own_leg.role = 'STAFF'
				AND own_leg.staff_subject = $1
				AND (
					own_leg.state IN ('PENDING', 'DIALING', 'RINGING', 'ANSWERED',
						'BRIDGE_PENDING', 'BRIDGED')
					OR (own_leg.state = 'ENDING' AND own_leg.answered_at IS NOT NULL)
					OR (
						own_leg.bridged_at IS NOT NULL
						AND call.terminal_outcome = 'ENDED'
						AND call.disposition_at IS NULL
					)
				)
			UNION
			SELECT own_transfer.call_id
			FROM human_calling_staff_transfers own_transfer
			WHERE own_transfer.state IN ('REQUESTED', 'ACCEPTED')
				AND own_transfer.location_id = ANY($3::uuid[])
				AND (
					own_transfer.requested_by_subject = $1
					OR own_transfer.recipient_subject = $1
				)
		), relevant_calls AS MATERIALIZED (
			SELECT call.*
			FROM human_calling_calls call
			JOIN relevant_call_ids relevant ON relevant.id = call.id
		), state_rows AS (
			SELECT 'lease'::text AS kind, lease.user_subject AS id,
				to_jsonb(lease) || jsonb_build_object(
					'leaseCurrent', lease.lease_expires_at > $4
				) AS value
			FROM human_calling_softphone_leases lease
			WHERE lease.user_subject = $1
			UNION ALL
			SELECT 'call', call.id::text, to_jsonb(call)
			FROM relevant_calls call
			UNION ALL
			SELECT 'leg', leg.id::text, to_jsonb(leg)
			FROM human_calling_call_legs leg
			JOIN relevant_calls call ON call.id = leg.call_id
			UNION ALL
			SELECT 'command', command.id::text, jsonb_build_object(
				'callId', command.call_id,
				'callLegId', command.call_leg_id,
				'action', command.action,
				'state', command.state,
				'sentAt', command.sent_at,
				'createdAt', command.created_at
			)
			FROM human_calling_provider_commands command
			JOIN relevant_calls call ON call.id = command.call_id
			UNION ALL
			SELECT 'transfer', transfer.id::text, to_jsonb(transfer)
			FROM human_calling_staff_transfers transfer
			JOIN relevant_calls call ON call.id = transfer.call_id
		)
		SELECT COALESCE(
			jsonb_agg(
				jsonb_build_array(kind, id, value)
				ORDER BY kind, id
			)::text,
			'[]'
		)
		FROM state_rows
	`, staffSubject, practiceIDs, locationIDs, m.now()).Scan(&callingSnapshot); err != nil {
		return "", fmt.Errorf("read Calling state validator: %w", err)
	}

	digest := sha256.New()
	_, _ = digest.Write(accessSnapshot)
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(callingSnapshot))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(m.config.RingWindowDuration.String()))
	return `"` + base64.RawURLEncoding.EncodeToString(digest.Sum(nil)) + `"`, nil
}
