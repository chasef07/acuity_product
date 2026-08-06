package humancalling

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	ETag        string
}

type RingingCallLeg struct {
	CallID         string
	CallLegID      string
	MediaToken     string
	PracticeID     string
	LocationID     string
	LocationName   string
	DisplayName    string
	TransferReason string
	State          string
	Version        int64
	CreatedAt      time.Time
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

func (m *Module) ReadCallingState(
	ctx context.Context,
	identity access.Identity,
) (CallingState, error) {
	if identity.Subject == "" || !identity.EmailVerified {
		return CallingState{}, ErrDenied
	}
	discovery, err := m.access.DiscoverActor(ctx, identity)
	if err != nil || discovery.PlatformOperator || len(discovery.Practices) == 0 {
		return CallingState{}, ErrDenied
	}

	state := CallingState{Ringing: []RingingCallLeg{}}
	var leaseVersion int64
	if err := m.pool.QueryRow(ctx, `
		SELECT session_id, lease_expires_at,
			lease_expires_at > $2,
			(desired_available AND registered AND microphone_ready
				AND audio_ready AND session_healthy
				AND lease_expires_at > $2),
			version
		FROM human_calling_softphone_leases
		WHERE user_subject = $1
	`, identity.Subject, m.now()).Scan(
		&state.Softphone.SessionID,
		&state.Softphone.LeaseExpiresAt,
		&state.Softphone.Owner,
		&state.Softphone.Available,
		&leaseVersion,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			state.ETag = callingStateETag(state, leaseVersion)
			return state, nil
		}
		return CallingState{}, fmt.Errorf("read Calling lease state: %w", err)
	}
	if err := m.loadCurrentCallCapacity(ctx, identity.Subject, &state.Softphone); err != nil {
		return CallingState{}, err
	}

	rows, err := m.pool.Query(ctx, `
		SELECT call.id::text, leg.id::text, call.practice_id::text,
			call.location_id::text, location.name,
			COALESCE(handoff.display_name, ''),
			COALESCE(handoff.transfer_reason, ''), leg.state,
			call.version, leg.created_at
		FROM human_calling_call_legs leg
		JOIN human_calling_calls call ON call.id = leg.call_id
		JOIN access_locations location
			ON location.practice_id = call.practice_id
			AND location.id = call.location_id
		JOIN access_memberships membership
			ON membership.practice_id = call.practice_id
			AND membership.user_subject = leg.staff_subject
			AND membership.revoked_at IS NULL
		LEFT JOIN human_calling_handoffs handoff
			ON handoff.id = call.source_handoff_id
		WHERE leg.role = 'STAFF'
			AND leg.staff_subject = $1
			AND leg.state IN ('PENDING', 'DIALING', 'RINGING', 'BRIDGE_PENDING')
			AND (
				membership.location_scope = 'ALL'
				OR EXISTS (
					SELECT 1 FROM access_membership_locations allowed
					WHERE allowed.membership_id = membership.id
						AND allowed.location_id = call.location_id
				)
			)
		ORDER BY leg.created_at, leg.id
	`, identity.Subject)
	if err != nil {
		return CallingState{}, fmt.Errorf("read ringing CallLegs: %w", err)
	}
	for rows.Next() {
		var leg RingingCallLeg
		if err := rows.Scan(
			&leg.CallID, &leg.CallLegID, &leg.PracticeID, &leg.LocationID,
			&leg.LocationName, &leg.DisplayName, &leg.TransferReason,
			&leg.State, &leg.Version, &leg.CreatedAt,
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

	state.Bridged, err = m.readStaffStateCall(ctx, identity.Subject, `
		leg.state = 'BRIDGED' AND call.terminal_outcome IS NULL
	`)
	if err != nil {
		return CallingState{}, err
	}
	state.Disposition, err = m.readStaffStateCall(ctx, identity.Subject, `
		leg.bridged_at IS NOT NULL AND call.terminal_outcome = 'ENDED'
		AND call.disposition_at IS NULL
	`)
	if err != nil {
		return CallingState{}, err
	}
	state.Voicemail, err = m.readScopedVoicemailState(ctx, identity.Subject)
	if err != nil {
		return CallingState{}, err
	}

	state.ETag = callingStateETag(state, leaseVersion)
	return state, nil
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
		JOIN access_memberships membership
			ON membership.practice_id = call.practice_id
			AND membership.user_subject = leg.staff_subject
			AND membership.revoked_at IS NULL
		WHERE leg.role = 'STAFF' AND leg.staff_subject = $1
			AND (
				membership.location_scope = 'ALL'
				OR EXISTS (
					SELECT 1 FROM access_membership_locations allowed
					WHERE allowed.membership_id = membership.id
						AND allowed.location_id = call.location_id
				)
			)
			AND (` + predicate + `)
		ORDER BY call.updated_at DESC, call.id
		LIMIT 1
	`
	var result CallingStateCall
	err := m.pool.QueryRow(ctx, query, staffSubject).Scan(
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
	err := m.pool.QueryRow(ctx, `
		SELECT call.id::text, caller.id::text, call.practice_id::text,
			call.location_id::text, location.name, caller.state, call.version
		FROM human_calling_calls call
		JOIN human_calling_call_legs caller
			ON caller.call_id = call.id AND caller.role = 'CALLER'
		JOIN access_locations location
			ON location.practice_id = call.practice_id
			AND location.id = call.location_id
		JOIN access_memberships membership
			ON membership.practice_id = call.practice_id
			AND membership.user_subject = $1
			AND membership.revoked_at IS NULL
		WHERE call.terminal_outcome = 'VOICEMAIL'
			AND call.disposition_at IS NULL
			AND (
				membership.location_scope = 'ALL'
				OR EXISTS (
					SELECT 1 FROM access_membership_locations allowed
					WHERE allowed.membership_id = membership.id
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

func callingStateETag(state CallingState, leaseVersion int64) string {
	value := fmt.Sprintf("lease:%d:%s:%t:%t", leaseVersion,
		state.Softphone.SessionID, state.Softphone.Owner, state.Softphone.Available)
	for _, leg := range state.Ringing {
		value += fmt.Sprintf("|ring:%s:%s:%s:%d", leg.CallID, leg.CallLegID, leg.State, leg.Version)
	}
	orderedCalls := []struct {
		name string
		call *CallingStateCall
	}{
		{name: "bridged", call: state.Bridged},
		{name: "voicemail", call: state.Voicemail},
		{name: "disposition", call: state.Disposition},
	}
	for _, item := range orderedCalls {
		name, call := item.name, item.call
		if call != nil {
			value += fmt.Sprintf("|%s:%s:%s:%s:%d", name, call.CallID,
				call.CallLegID, call.State, call.Version)
		}
	}
	digest := sha256.Sum256([]byte(value))
	return `"` + base64.RawURLEncoding.EncodeToString(digest[:]) + `"`
}
