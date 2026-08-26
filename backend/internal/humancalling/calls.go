package humancalling

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
)

type callProjection struct {
	call             Call
	terminalOutcome  string
	disposition      string
	staffState       string
	destinationState string
	voicemailPhase   string
}

const staffEndedBeforeProviderStartErrorCode = "STAFF_ENDED_BEFORE_PROVIDER_START"

func (m *Module) ReadCall(
	ctx context.Context,
	identity access.Identity,
	callID string,
) (Call, error) {
	if m.access == nil || callID == "" {
		return Call{}, ErrDenied
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Call{}, fmt.Errorf("begin Call read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	projection, err := m.loadCallProjection(ctx, tx, callID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Call{}, ErrDenied
	}
	if err != nil {
		return Call{}, err
	}
	if _, err := m.access.LockReadAuthorization(
		ctx, tx, identity, projection.call.PracticeID, projection.call.LocationID,
	); err != nil {
		return Call{}, ErrDenied
	}
	if err := tx.Commit(ctx); err != nil {
		return Call{}, fmt.Errorf("commit Call read: %w", err)
	}
	return projection.call, nil
}

func (m *Module) loadCallProjection(
	ctx context.Context,
	querier interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	callID string,
) (callProjection, error) {
	var result callProjection
	var connectedAt *time.Time
	err := querier.QueryRow(ctx, `
		SELECT call.id::text, call.practice_id::text, call.location_id::text,
			location.name, call.direction, call.entry_point,
			COALESCE(call.task_id::text, ''),
			COALESCE(call.caller_phone, call.destination_phone, ''),
			COALESCE(call.outbound_caller_id, ''),
			COALESCE(handoff.phone_source, ''),
			COALESCE(handoff.display_name, ''), COALESCE(handoff.name_source, ''),
			COALESCE(handoff.transfer_reason, ''), COALESCE(handoff.reason_source, ''),
			COALESCE(call.provider_termination, ''), call.version,
			COALESCE(call.terminal_outcome, ''),
			EXISTS (
				SELECT 1 FROM human_calling_timeline timeline
				WHERE timeline.call_id = call.id
					AND timeline.kind = 'call.hangup.requested'
			),
			COALESCE(call.disposition_outcome, ''), call.disposition_deadline,
			COALESCE(call.retry_of_call_id::text, ''),
			(
				SELECT min(leg.bridged_at) FROM human_calling_call_legs leg
				WHERE leg.call_id = call.id AND leg.bridged_at IS NOT NULL
			),
			COALESCE((
				SELECT leg.state FROM human_calling_call_legs leg
				WHERE leg.call_id = call.id AND leg.role = 'STAFF'
				ORDER BY CASE leg.state
					WHEN 'BRIDGED' THEN 1 WHEN 'BRIDGE_PENDING' THEN 2
					WHEN 'ENDING' THEN 3 WHEN 'RINGING' THEN 4
					WHEN 'DIALING' THEN 5 ELSE 6 END, leg.updated_at DESC
				LIMIT 1
			), ''),
			COALESCE((
				SELECT leg.state FROM human_calling_call_legs leg
				WHERE leg.call_id = call.id AND leg.role = 'DESTINATION'
				ORDER BY leg.sequence DESC LIMIT 1
			), ''),
			COALESCE((
				SELECT CASE command.action
					WHEN 'START_VOICEMAIL_RECORDING' THEN 'VOICEMAIL_RECORDING'
					ELSE 'VOICEMAIL_GREETING'
				END
				FROM human_calling_provider_commands command
				WHERE command.call_id = call.id
					AND command.action IN ('SPEAK_VOICEMAIL', 'START_VOICEMAIL_RECORDING')
					AND command.state IN ('PENDING', 'SENDING', 'SENT', 'AMBIGUOUS', 'RECONCILED')
				ORDER BY
					CASE command.action WHEN 'START_VOICEMAIL_RECORDING' THEN 1 ELSE 2 END,
					command.created_at DESC,
					command.id DESC
				LIMIT 1
			), ''),
			COALESCE(voicemail.outcome, ''), COALESCE(voicemail.audio_state, ''),
			COALESCE(voicemail.task_id::text, ''),
			COALESCE(voicemail.duration_millis / 1000, 0),
			COALESCE(CASE
				WHEN recording.audio_state = 'READY'
					AND recording.content_expires_at <= $2
				THEN 'EXPIRED'
				ELSE recording.audio_state
			END, ''),
			COALESCE(recording.duration_millis / 1000, 0)
		FROM human_calling_calls call
		JOIN access_locations location
			ON location.practice_id = call.practice_id AND location.id = call.location_id
		LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id
		LEFT JOIN human_calling_voicemails voicemail ON voicemail.call_id = call.id
		LEFT JOIN human_calling_call_recordings recording ON recording.call_id = call.id
		WHERE call.id = $1
	`, callID, m.now()).Scan(
		&result.call.ID, &result.call.PracticeID, &result.call.LocationID,
		&result.call.LocationName, &result.call.Direction, &result.call.EntryPoint,
		&result.call.TaskID, &result.call.Phone, &result.call.CallerID,
		&result.call.PhoneSource, &result.call.DisplayName, &result.call.NameSource,
		&result.call.TransferReason, &result.call.ReasonSource,
		&result.call.ProviderTermination, &result.call.Version,
		&result.terminalOutcome, &result.call.EndRequested, &result.disposition,
		&result.call.DispositionDeadline, &result.call.RetryOfCallID,
		&connectedAt, &result.staffState, &result.destinationState,
		&result.voicemailPhase,
		&result.call.Voicemail.Outcome, &result.call.Voicemail.AudioState,
		&result.call.Voicemail.TaskID, &result.call.Voicemail.DurationSeconds,
		&result.call.Recording.AudioState, &result.call.Recording.DurationSeconds,
	)
	if err != nil {
		return callProjection{}, fmt.Errorf("read Call projection: %w", err)
	}
	result.call.ConnectedAt = connectedAt
	result.call.State = deriveCallState(result.terminalOutcome, result.disposition,
		result.staffState, result.destinationState, result.voicemailPhase,
		result.call.Direction,
		connectedAt != nil)
	result.call.RetryAllowed = result.call.Direction == CallOutbound &&
		(result.call.State == CallUnanswered || result.call.State == CallMissed)
	return result, nil
}

func deriveCallState(
	terminalOutcome string,
	disposition string,
	staffState string,
	destinationState string,
	voicemailPhase string,
	direction CallDirection,
	connected bool,
) CallState {
	if disposition != "" {
		switch disposition {
		case string(DispositionFollowUpRequired), string(DispositionCreateTask):
			return CallFollowUpRequired
		default:
			return CallResolved
		}
	}
	if terminalOutcome != "" {
		switch terminalOutcome {
		case "VOICEMAIL":
			return CallVoicemail
		case "MISSED", "ABANDONED":
			return CallMissed
		case "RESOLVED":
			return CallResolved
		case "FOLLOW_UP_REQUIRED":
			return CallFollowUpRequired
		case "ENDED":
			if connected {
				return CallNeedsDisposition
			}
			return CallUnanswered
		default:
			return CallUnanswered
		}
	}
	if voicemailPhase == string(CallVoicemailRecording) {
		return CallVoicemailRecording
	}
	if voicemailPhase == string(CallVoicemailGreeting) {
		return CallVoicemailGreeting
	}
	if staffState == "BRIDGED" || destinationState == "BRIDGED" {
		return CallConnected
	}
	if staffState == "BRIDGE_PENDING" {
		return CallConnecting
	}
	if staffState != "" {
		return CallRinging
	}
	if direction == CallOutbound {
		return CallPreparing
	}
	return CallRinging
}

type callHistoryCursor struct {
	StartedAt time.Time `json:"startedAt"`
	ID        string    `json:"id"`
}

func (m *Module) QueryCallHistory(
	ctx context.Context,
	query CallHistoryQuery,
) (CallHistoryPage, error) {
	query.Phone = strings.TrimSpace(query.Phone)
	if m.access == nil || query.Identity.Subject == "" || query.PracticeID == "" ||
		!canonicalE164.MatchString(query.Phone) {
		return CallHistoryPage{}, ErrDenied
	}
	if query.Limit <= 0 || query.Limit > 100 {
		query.Limit = 25
	}
	var cursor callHistoryCursor
	if query.Cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(query.Cursor)
		if err != nil || json.Unmarshal(decoded, &cursor) != nil ||
			cursor.StartedAt.IsZero() || cursor.ID == "" {
			return CallHistoryPage{}, ErrInvalidInput
		}
	}
	rows, err := m.database.Query(ctx, `
		SELECT call.id::text, call.direction, call.created_at, call.ended_at,
			GREATEST(0, EXTRACT(EPOCH FROM (
				COALESCE(call.ended_at, $6) - call.created_at
			))::bigint), call.location_id::text, location.name,
			COALESCE(staff_membership.email, staff_operator.email, ''),
			COALESCE(handoff.transfer_reason, ''),
			COALESCE(call.terminal_outcome, ''),
			COALESCE(call.disposition_outcome, ''),
			COALESCE((
				SELECT leg.state FROM human_calling_call_legs leg
				WHERE leg.call_id = call.id AND leg.role = 'STAFF'
				ORDER BY CASE leg.state WHEN 'BRIDGED' THEN 1 WHEN 'BRIDGE_PENDING' THEN 2 ELSE 3 END
				LIMIT 1
			), ''),
			COALESCE((
				SELECT leg.state FROM human_calling_call_legs leg
				WHERE leg.call_id = call.id AND leg.role = 'DESTINATION'
				ORDER BY leg.sequence DESC LIMIT 1
			), ''),
			EXISTS (
				SELECT 1 FROM human_calling_call_legs leg
				WHERE leg.call_id = call.id AND leg.bridged_at IS NOT NULL
			)
		FROM human_calling_calls call
		JOIN access_locations location
			ON location.practice_id = call.practice_id AND location.id = call.location_id
		JOIN access_calling_scopes calling_scope
			ON calling_scope.practice_id = call.practice_id
			AND calling_scope.user_subject = $1
		LEFT JOIN access_membership_locations allowed
			ON allowed.membership_id = calling_scope.membership_id
			AND allowed.location_id = call.location_id
		LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id
		LEFT JOIN human_calling_call_legs bridged_staff
			ON bridged_staff.call_id = call.id AND bridged_staff.role = 'STAFF'
			AND bridged_staff.bridged_at IS NOT NULL
		LEFT JOIN access_memberships staff_membership
			ON staff_membership.practice_id = call.practice_id
			AND staff_membership.user_subject = bridged_staff.staff_subject
		LEFT JOIN access_platform_operators staff_operator
			ON staff_operator.user_subject = bridged_staff.staff_subject
		WHERE call.practice_id = $2
			AND COALESCE(call.caller_phone, call.destination_phone) = $3
			AND (calling_scope.location_scope = 'ALL' OR allowed.location_id IS NOT NULL)
			AND ($4::timestamptz IS NULL OR (call.created_at, call.id) < ($4, $5::uuid))
		ORDER BY call.created_at DESC, call.id DESC
		LIMIT $7
	`, query.Identity.Subject, query.PracticeID, query.Phone,
		nullTime(cursor.StartedAt), nullString(cursor.ID), m.now(), query.Limit+1)
	if err != nil {
		return CallHistoryPage{}, fmt.Errorf("query Call history: %w", err)
	}
	defer rows.Close()
	page := CallHistoryPage{Items: []CallHistoryItem{}}
	for rows.Next() {
		var item CallHistoryItem
		var terminal, disposition, staffState, destinationState string
		var connected bool
		if err := rows.Scan(
			&item.ID, &item.Direction, &item.StartedAt, &item.EndedAt,
			&item.DurationSeconds, &item.LocationID, &item.LocationName,
			&item.AnsweredByEmail, &item.TransferReason, &terminal, &disposition,
			&staffState, &destinationState, &connected,
		); err != nil {
			return CallHistoryPage{}, fmt.Errorf("scan Call history: %w", err)
		}
		item.Type = "CALL"
		item.Outcome = deriveCallState(terminal, disposition, staffState,
			destinationState, "", CallDirection(item.Direction), connected)
		item.Current = item.ID == query.CurrentCallID
		item.Originating = item.ID == query.OriginatingCallID
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return CallHistoryPage{}, fmt.Errorf("iterate Call history: %w", err)
	}
	if len(page.Items) > query.Limit {
		last := page.Items[query.Limit-1]
		encoded, _ := json.Marshal(callHistoryCursor{StartedAt: last.StartedAt, ID: last.ID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
		page.Items = page.Items[:query.Limit]
	}
	return page, nil
}

func (m *Module) RequestHangup(
	ctx context.Context,
	identity access.Identity,
	sessionID string,
	callID string,
) (Call, error) {
	if sessionID == "" || callID == "" {
		return Call{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Call{}, fmt.Errorf("begin exact-leg Hangup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID, locationID, initiatingSubject string
	var direction CallDirection
	var terminal bool
	if err := tx.QueryRow(ctx, `
		SELECT call.practice_id::text, call.location_id::text,
			call.direction, COALESCE(call.initiating_subject, ''),
			call.terminal_outcome IS NOT NULL
		FROM human_calling_calls call
		WHERE call.id = $1
		FOR UPDATE OF call
	`, callID).Scan(
		&practiceID, &locationID, &direction, &initiatingSubject, &terminal,
	); err != nil {
		return Call{}, ErrDenied
	}
	if _, err := m.access.LockMembershipAuthorization(
		ctx, tx, identity, practiceID, locationID,
	); err != nil {
		return Call{}, ErrDenied
	}
	hangupCommitted := terminal
	if !hangupCommitted {
		rows, err := tx.Query(ctx, `
			SELECT COALESCE(payload->>'client_state', '')
			FROM human_calling_provider_commands
			WHERE call_id = $1 AND action = 'HANGUP_LEG'
			ORDER BY created_at, id
		`, callID)
		if err != nil {
			return Call{}, fmt.Errorf("read committed Hangup commands: %w", err)
		}
		for rows.Next() {
			var clientState string
			if err := rows.Scan(&clientState); err != nil {
				rows.Close()
				return Call{}, fmt.Errorf("scan committed Hangup command: %w", err)
			}
			state, valid := parseCallLegClientState(clientState)
			if valid && state.CallID == callID && state.Kind == callLegClientStateStaffHangup {
				hangupCommitted = true
				break
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return Call{}, fmt.Errorf("iterate committed Hangup commands: %w", err)
		}
	}
	if hangupCommitted {
		if err := tx.Commit(ctx); err != nil {
			return Call{}, fmt.Errorf("commit idempotent Hangup: %w", err)
		}
		return m.ReadCall(ctx, identity, callID)
	}
	var ownsLease bool
	if err := tx.QueryRow(ctx, `
		SELECT session_id = $2 AND lease_expires_at > $3
		FROM human_calling_softphone_leases
		WHERE user_subject = $1 FOR UPDATE
	`, identity.Subject, sessionID, m.now()).Scan(&ownsLease); err != nil || !ownsLease {
		return Call{}, ErrConflict
	}
	var ownsConnectedCall bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM human_calling_call_legs
			WHERE call_id = $1 AND role = 'STAFF' AND staff_subject = $2
				AND state IN ('BRIDGE_PENDING', 'BRIDGED')
		)
	`, callID, identity.Subject).Scan(&ownsConnectedCall); err != nil ||
		(!ownsConnectedCall &&
			(direction != CallOutbound || initiatingSubject != identity.Subject)) {
		return Call{}, ErrConflict
	}
	if direction == CallOutbound {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_commands
			SET state = 'FAILED', last_error_code = $3,
				updated_at = $2
			WHERE call_id = $1
				AND action IN ('DIAL_OUTBOUND_STAFF', 'DIAL_OUTBOUND_DESTINATION', 'BRIDGE')
				AND state = 'PENDING'
		`, callID, m.now(), staffEndedBeforeProviderStartErrorCode); err != nil {
			return Call{}, fmt.Errorf("cancel unsent outbound work: %w", err)
		}
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, role, COALESCE(staff_subject, ''),
			COALESCE(provider_call_control_id, ''),
			COALESCE((
				SELECT command.id::text FROM human_calling_provider_commands command
				WHERE command.call_leg_id = human_calling_call_legs.id
					AND command.action IN (
						'DIAL_OUTBOUND_STAFF', 'DIAL_OUTBOUND_DESTINATION'
					)
					AND command.state IN ('SENDING', 'AMBIGUOUS')
				ORDER BY command.created_at, command.id
				LIMIT 1
			), '')
		FROM human_calling_call_legs
		WHERE call_id = $1 AND state NOT IN ('ENDED', 'FAILED', 'ENDING')
		ORDER BY role, id FOR UPDATE
	`, callID)
	if err != nil {
		return Call{}, fmt.Errorf("lock exact Hangup targets: %w", err)
	}
	type target struct {
		id, role, subject, controlID, uncertainDialCommandID string
	}
	targets := []target{}
	for rows.Next() {
		var item target
		if err := rows.Scan(
			&item.id, &item.role, &item.subject, &item.controlID,
			&item.uncertainDialCommandID,
		); err != nil {
			rows.Close()
			return Call{}, err
		}
		targets = append(targets, item)
	}
	rows.Close()
	if len(targets) == 0 {
		return Call{}, ErrConflict
	}
	providerWorkRemaining := false
	for _, target := range targets {
		if target.controlID == "" && target.uncertainDialCommandID == "" {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_call_legs
				SET state = 'FAILED', ending_at = COALESCE(ending_at, $2),
					ended_at = COALESCE(ended_at, $2),
					error_code = $3, updated_at = $2
				WHERE id = $1
			`, target.id, m.now(), staffEndedBeforeProviderStartErrorCode); err != nil {
				return Call{}, err
			}
			continue
		}
		providerWorkRemaining = true
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_call_legs
			SET state = 'ENDING', ending_at = COALESCE(ending_at, $2), updated_at = $2
			WHERE id = $1
		`, target.id, m.now()); err != nil {
			return Call{}, err
		}
		if target.controlID == "" {
			if _, err := m.insertCallLegCommand(
				ctx, tx, callID, target.id, "", target.subject, CommandHangupLeg, "",
				map[string]any{"client_state": encodeCallLegClientState(
					callID, target.id, target.role, callLegClientStateStaffHangup,
				)}, target.uncertainDialCommandID,
			); err != nil {
				return Call{}, err
			}
			continue
		}
		if _, err := m.insertCallLegCommand(
			ctx, tx, callID, target.id, "", target.subject, CommandHangupLeg,
			target.controlID, map[string]any{"client_state": encodeCallLegClientState(
				callID, target.id, target.role, callLegClientStateStaffHangup,
			)}, "",
		); err != nil {
			return Call{}, err
		}
	}
	if direction == CallOutbound && !providerWorkRemaining {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET terminal_outcome = 'UNANSWERED', ended_at = COALESCE(ended_at, $2),
				version = version + 1, updated_at = $2
			WHERE id = $1 AND terminal_outcome IS NULL
		`, callID, m.now()); err != nil {
			return Call{}, fmt.Errorf("end outbound Call before provider start: %w", err)
		}
	} else if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1, updated_at = $2 WHERE id = $1
	`, callID, m.now()); err != nil {
		return Call{}, err
	}
	if err := appendTimeline(ctx, tx, callID, practiceID, "call.hangup.requested",
		identity.Subject, "", "", "", "", m.now()); err != nil {
		return Call{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Call{}, fmt.Errorf("commit exact-leg Hangup: %w", err)
	}
	return m.ReadCall(ctx, identity, callID)
}

func (m *Module) RecordDisposition(
	ctx context.Context,
	identity access.Identity,
	sessionID string,
	callID string,
	disposition Disposition,
) (DispositionResult, error) {
	if sessionID == "" || m.work == nil || !validDisposition(disposition) {
		return DispositionResult{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DispositionResult{}, fmt.Errorf("begin Call disposition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID, locationID, taskID, phone, reason, terminal, existing string
	var direction CallDirection
	var entryPoint CallEntryPoint
	if err := tx.QueryRow(ctx, `
		SELECT call.practice_id::text, call.location_id::text, call.direction,
			call.entry_point, COALESCE(call.task_id::text, call.surfaced_task_id::text, ''),
			COALESCE(call.caller_phone, call.destination_phone, ''),
			COALESCE(handoff.transfer_reason, ''), COALESCE(call.terminal_outcome, ''),
			COALESCE(call.disposition_outcome, '')
		FROM human_calling_calls call
		LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id
		WHERE call.id = $1 FOR UPDATE OF call
	`, callID).Scan(&practiceID, &locationID, &direction, &entryPoint, &taskID,
		&phone, &reason, &terminal, &existing); err != nil {
		return DispositionResult{}, ErrDenied
	}
	authorization, err := m.access.LockMembershipAuthorization(
		ctx, tx, identity, practiceID, locationID,
	)
	if err != nil {
		return DispositionResult{}, ErrDenied
	}
	var owner bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM human_calling_call_legs leg
			WHERE leg.call_id = $1 AND leg.role = 'STAFF'
				AND leg.staff_subject = $2 AND leg.bridged_at IS NOT NULL
		) OR EXISTS (
			SELECT 1 FROM human_calling_calls call
			WHERE call.id = $1 AND call.direction = 'OUTBOUND'
				AND call.initiating_subject = $2
		)
	`, callID, identity.Subject).Scan(&owner); err != nil || !owner {
		return DispositionResult{}, ErrConflict
	}
	var ownsLease bool
	if err := tx.QueryRow(ctx, `
		SELECT session_id = $2 AND lease_expires_at > $3
		FROM human_calling_softphone_leases
		WHERE user_subject = $1 FOR UPDATE
	`, identity.Subject, sessionID, m.now()).Scan(&ownsLease); err != nil || !ownsLease {
		return DispositionResult{}, ErrConflict
	}
	connected := terminal == "ENDED" && m.callHasBridge(ctx, tx, callID)
	if !dispositionAllowed(direction, entryPoint, terminal, connected, disposition) {
		return DispositionResult{}, ErrConflict
	}
	if existing != "" {
		if existing != string(disposition) {
			return DispositionResult{}, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return DispositionResult{}, err
		}
		call, err := m.ReadCall(ctx, identity, callID)
		return DispositionResult{Call: call, TaskID: taskID}, err
	}
	if disposition == DispositionCompleteTask || disposition == DispositionKeepOpen ||
		(disposition == DispositionResolved && taskID != "") {
		updated, err := m.work.ApplyCallTaskDisposition(ctx, tx, taskID,
			disposition != DispositionKeepOpen, authorization.Actor, m.now())
		if err != nil {
			return DispositionResult{}, err
		}
		taskID = updated.ID
	}
	if disposition == DispositionFollowUpRequired || disposition == DispositionCreateTask {
		task, err := m.work.EnsureCallFollowUp(ctx, tx, work.EnsureCallFollowUpCommand{
			CallID: callID, PracticeID: practiceID, LocationID: locationID,
			Phone: phone, Reason: reason, Creator: authorization.Actor,
		})
		if err != nil {
			return DispositionResult{}, err
		}
		taskID = task.ID
	}
	now := m.now()
	terminalOutcome := "RESOLVED"
	if disposition == DispositionFollowUpRequired || disposition == DispositionCreateTask {
		terminalOutcome = "FOLLOW_UP_REQUIRED"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET disposition_actor_subject = $2,
			disposition_at = $3, disposition_outcome = $4,
			disposition_deadline = NULL, terminal_outcome = $5,
			version = version + 1, updated_at = $3 WHERE id = $1
	`, callID, identity.Subject, now, disposition, terminalOutcome); err != nil {
		return DispositionResult{}, fmt.Errorf("record Call disposition: %w", err)
	}
	if err := appendTimeline(ctx, tx, callID, practiceID, "call.dispositioned",
		identity.Subject, "", "", "", "", now); err != nil {
		return DispositionResult{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return DispositionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DispositionResult{}, fmt.Errorf("commit Call disposition: %w", err)
	}
	call, err := m.ReadCall(ctx, identity, callID)
	return DispositionResult{Call: call, TaskID: taskID}, err
}

// ExpireDispositions applies the ordinary resolved outcome from durable bridge
// and hangup evidence when the browser does not submit a disposition in time.
func (m *Module) ExpireDispositions(ctx context.Context) (int, error) {
	if m.work == nil {
		return 0, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin disposition expiry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT call.id::text, call.practice_id::text,
			COALESCE(call.task_id::text, call.surfaced_task_id::text, ''),
			winner.staff_subject, COALESCE(membership.email, platform_operator.email, '')
		FROM human_calling_calls call
		JOIN LATERAL (
			SELECT leg.staff_subject
			FROM human_calling_call_legs leg
			WHERE leg.call_id = call.id AND leg.role = 'STAFF'
				AND leg.bridged_at IS NOT NULL
			ORDER BY leg.bridged_at, leg.id
			LIMIT 1
		) winner ON true
		LEFT JOIN access_memberships membership
			ON membership.practice_id = call.practice_id
			AND membership.user_subject = winner.staff_subject
		LEFT JOIN access_platform_operators platform_operator
			ON platform_operator.user_subject = winner.staff_subject
		WHERE call.terminal_outcome = 'ENDED'
			AND call.disposition_outcome IS NULL
			AND call.disposition_deadline <= $1
		ORDER BY call.disposition_deadline, call.id
		FOR UPDATE OF call SKIP LOCKED
		LIMIT 100
	`, m.now())
	if err != nil {
		return 0, fmt.Errorf("claim expired dispositions: %w", err)
	}
	type expiredDisposition struct {
		callID, practiceID, taskID, subject, email string
	}
	items := []expiredDisposition{}
	for rows.Next() {
		var item expiredDisposition
		if err := rows.Scan(&item.callID, &item.practiceID, &item.taskID,
			&item.subject, &item.email); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired disposition: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate expired dispositions: %w", err)
	}
	rows.Close()
	for _, item := range items {
		now := m.now()
		if item.taskID != "" {
			if item.subject == "" || item.email == "" {
				return 0, fmt.Errorf("resolve automatic disposition actor: %w", ErrConflict)
			}
			if _, err := m.work.ApplyCallTaskDisposition(ctx, tx, item.taskID, true,
				access.Actor{Subject: item.subject, Email: item.email}, now); err != nil {
				return 0, fmt.Errorf("complete automatic disposition Task: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET disposition_actor_subject = $2, disposition_at = $3,
				disposition_outcome = 'RESOLVED', disposition_deadline = NULL,
				terminal_outcome = 'RESOLVED', version = version + 1, updated_at = $3
			WHERE id = $1 AND terminal_outcome = 'ENDED'
				AND disposition_outcome IS NULL
		`, item.callID, item.subject, now); err != nil {
			return 0, fmt.Errorf("resolve expired Call disposition: %w", err)
		}
		if err := appendTimeline(ctx, tx, item.callID, item.practiceID,
			"call.dispositioned", item.subject, "", "", "", "AUTO_RESOLVED", now); err != nil {
			return 0, err
		}
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, item.practiceID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit disposition expiry: %w", err)
	}
	return len(items), nil
}

func (m *Module) callHasBridge(ctx context.Context, tx pgx.Tx, callID string) bool {
	var bridged bool
	_ = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM human_calling_call_legs
			WHERE call_id = $1 AND bridged_at IS NOT NULL
		)
	`, callID).Scan(&bridged)
	return bridged
}

func validDisposition(disposition Disposition) bool {
	switch disposition {
	case DispositionResolved, DispositionFollowUpRequired,
		DispositionCompleteTask, DispositionKeepOpen,
		DispositionCreateTask, DispositionNoFollowUp:
		return true
	default:
		return false
	}
}

func dispositionAllowed(
	direction CallDirection,
	entryPoint CallEntryPoint,
	terminal string,
	connected bool,
	disposition Disposition,
) bool {
	if terminal != "ENDED" {
		return false
	}
	if direction == CallInbound {
		return connected && (disposition == DispositionResolved ||
			disposition == DispositionFollowUpRequired)
	}
	if entryPoint == CallEntryTask {
		return connected && (disposition == DispositionCompleteTask ||
			disposition == DispositionKeepOpen)
	}
	if connected {
		return disposition == DispositionResolved || disposition == DispositionCreateTask
	}
	return disposition == DispositionNoFollowUp || disposition == DispositionCreateTask
}

func (m *Module) ReadOperatorTimeline(
	ctx context.Context,
	identity access.Identity,
	callID string,
) (OperatorTimeline, error) {
	if m.access == nil || callID == "" {
		return OperatorTimeline{}, ErrDenied
	}
	discovery, err := m.access.DiscoverActor(ctx, identity)
	if err != nil || !discovery.PlatformOperator {
		return OperatorTimeline{}, ErrDenied
	}
	projection, err := m.loadCallProjection(ctx, m.database, callID)
	if err != nil {
		return OperatorTimeline{}, ErrDenied
	}
	result := OperatorTimeline{
		CallID: callID, PracticeID: projection.call.PracticeID,
		State: projection.call.State, Version: projection.call.Version,
		Entries: []TimelineEntry{},
	}
	rows, err := m.database.Query(ctx, `
		WITH entries AS (
			SELECT timeline.kind, COALESCE(timeline.opaque_reference, '') AS opaque_reference,
				COALESCE(timeline.error_code, command.last_error_code,
					receipt.projection_error_code, '') AS error_code,
				COALESCE(command.action, '') AS command_action,
				COALESCE(command.state, '') AS command_state,
				COALESCE(command.attempts, 0) AS command_attempts,
				COALESCE(receipt.state, '') AS receipt_state,
				COALESCE(command.created_at, receipt.received_at, timeline.occurred_at) AS started_at,
				timeline.occurred_at AS occurred_at, timeline.id::text AS stable_id,
				COALESCE(receipt.event_id, '') AS recovery_event_id
			FROM human_calling_timeline timeline
			LEFT JOIN human_calling_provider_commands command
				ON command.id = timeline.provider_command_id
			LEFT JOIN human_calling_provider_receipts receipt
				ON receipt.event_id = timeline.provider_event_id
			WHERE timeline.call_id = $1
			UNION ALL
			SELECT 'provider.command.committed', '', COALESCE(command.last_error_code, ''),
				command.action, command.state, command.attempts, '', command.created_at,
				command.created_at, command.id::text, ''
			FROM human_calling_provider_commands command
			WHERE command.call_id = $1 AND NOT EXISTS (
				SELECT 1 FROM human_calling_timeline timeline
				WHERE timeline.provider_command_id = command.id
			)
			UNION ALL
			SELECT 'provider.receipt.' || lower(receipt.state), '',
				COALESCE(receipt.projection_error_code, ''), '', '', 0, receipt.state,
				receipt.received_at, COALESCE(receipt.occurred_at, receipt.received_at),
				receipt.event_id, receipt.event_id
			FROM human_calling_provider_receipts receipt
			WHERE receipt.call_id = $1 AND NOT EXISTS (
				SELECT 1 FROM human_calling_timeline timeline
				WHERE timeline.provider_event_id = receipt.event_id
			)
		)
		SELECT kind, opaque_reference, error_code, command_action, command_state,
			command_attempts, receipt_state,
			GREATEST(0, EXTRACT(EPOCH FROM ($2 - started_at))::bigint),
			occurred_at, recovery_event_id
		FROM entries ORDER BY occurred_at, stable_id
	`, callID, m.now())
	if err != nil {
		return OperatorTimeline{}, fmt.Errorf("read operator timeline: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry TimelineEntry
		var recoveryEventID string
		if err := rows.Scan(&entry.Kind, &entry.OpaqueReference, &entry.ErrorCode,
			&entry.CommandAction, &entry.CommandState, &entry.CommandAttempts,
			&entry.ReceiptState, &entry.AgeSeconds, &entry.OccurredAt,
			&recoveryEventID); err != nil {
			return OperatorTimeline{}, fmt.Errorf("scan operator timeline: %w", err)
		}
		if entry.ReceiptState == string(ReceiptQuarantined) {
			entry.RecoveryReference = m.receiptRecoveryReference(recoveryEventID)
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, rows.Err()
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
