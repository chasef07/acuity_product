package humancalling

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	outboundIdempotencyKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)
	usPhoneNumber          = regexp.MustCompile(`^\+1[2-9][0-9]{2}[2-9][0-9]{6}$`)
)

type StartOutboundCallCommand struct {
	Identity       access.Identity
	SessionID      string
	IdempotencyKey string
	TaskID         string
	PracticeID     string
	LocationID     string
	Destination    string
	RetryOfCallID  string
}

type ConfirmOutboundMediaCommand struct {
	Identity   access.Identity
	SessionID  string
	CallID     string
	MediaToken string
}

type LocationVoiceProvision struct {
	PracticeKey       string
	LocationKey       string
	Number            string
	Enabled           bool
	VoicemailGreeting string
}

type OutboundVoiceFallbackProvision struct {
	PracticeKey string
	LocationKey string
}

type OutboundEligibility struct {
	Eligible bool
	Reason   string
}

func (m *Module) TaskOutboundEligibility(
	ctx context.Context,
	identity access.Identity,
	taskID string,
) (OutboundEligibility, error) {
	if m.work == nil || strings.TrimSpace(taskID) == "" {
		return OutboundEligibility{}, ErrInvalidInput
	}
	task, err := m.work.ReadTask(ctx, identity, taskID)
	if err != nil {
		if errors.Is(err, work.ErrDenied) {
			return OutboundEligibility{}, ErrDenied
		}
		return OutboundEligibility{}, err
	}
	if task.State != work.TaskOpen {
		return OutboundEligibility{Reason: "Complete Tasks must be reopened before calling."}, nil
	}
	if !supportedUSDestination(task.Phone) {
		return OutboundEligibility{Reason: "This Task does not have a supported US destination."}, nil
	}
	if _, err := outboundCallerID(ctx, m.pool, task.PracticeID, task.LocationID); err != nil {
		if !errors.Is(err, ErrConflict) {
			return OutboundEligibility{}, err
		}
		return OutboundEligibility{Reason: "Calling requires one configured office caller ID."}, nil
	}
	return OutboundEligibility{Eligible: true}, nil
}

func (m *Module) ProvisionLocationVoices(
	ctx context.Context,
	provisions []LocationVoiceProvision,
) error {
	if m.pool == nil {
		return ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin Location voice provisioning: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := m.ProvisionLocationVoicesInTx(ctx, tx, provisions); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Location voice provisioning: %w", err)
	}
	return nil
}

func (m *Module) ProvisionLocationVoicesInTx(
	ctx context.Context,
	tx pgx.Tx,
	provisions []LocationVoiceProvision,
) error {
	if tx == nil {
		return ErrInvalidInput
	}
	for _, provision := range provisions {
		provision.PracticeKey = strings.TrimSpace(provision.PracticeKey)
		provision.LocationKey = strings.TrimSpace(provision.LocationKey)
		provision.Number = strings.TrimSpace(provision.Number)
		provision.VoicemailGreeting = strings.TrimSpace(provision.VoicemailGreeting)
		if provision.VoicemailGreeting == "" {
			provision.VoicemailGreeting = defaultVoicemailGreeting
		}
		if provision.PracticeKey == "" || provision.LocationKey == "" ||
			!validUSNumber(provision.Number) ||
			utf8.RuneCountInString(provision.VoicemailGreeting) > 2000 {
			return ErrInvalidInput
		}
		var practiceID, locationID string
		if err := tx.QueryRow(ctx, `
			SELECT practice.id::text, location.id::text
			FROM access_practices practice
			JOIN access_locations location ON location.practice_id = practice.id
			WHERE practice.provisioning_key = $1 AND location.provisioning_key = $2
			FOR UPDATE OF location
		`, provision.PracticeKey, provision.LocationKey).Scan(
			&practiceID, &locationID,
		); err != nil {
			return ErrInvalidInput
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM human_calling_location_voice_numbers
			WHERE practice_id = $1 AND location_id = $2 AND phone <> $3
		`, practiceID, locationID, provision.Number); err != nil {
			return fmt.Errorf("reconcile Location voice number: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO human_calling_location_voice_numbers (
				practice_id, location_id, phone, enabled, voicemail_greeting
			) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (practice_id, location_id, phone) DO UPDATE SET
				enabled = EXCLUDED.enabled,
				voicemail_greeting = EXCLUDED.voicemail_greeting,
				updated_at = now()
		`, practiceID, locationID, provision.Number, provision.Enabled,
			provision.VoicemailGreeting); err != nil {
			return fmt.Errorf("provision Location voice number: %w", err)
		}
	}
	return nil
}

func (m *Module) ProvisionOutboundVoiceFallbacks(
	ctx context.Context,
	provisions []OutboundVoiceFallbackProvision,
) error {
	if m.pool == nil {
		return ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin outbound voice fallback provisioning: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := m.ProvisionOutboundVoiceFallbacksInTx(ctx, tx, provisions); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit outbound voice fallback provisioning: %w", err)
	}
	return nil
}

func (m *Module) ProvisionOutboundVoiceFallbacksInTx(
	ctx context.Context,
	tx pgx.Tx,
	provisions []OutboundVoiceFallbackProvision,
) error {
	if tx == nil {
		return ErrInvalidInput
	}
	for _, provision := range provisions {
		provision.PracticeKey = strings.TrimSpace(provision.PracticeKey)
		provision.LocationKey = strings.TrimSpace(provision.LocationKey)
		if provision.PracticeKey == "" {
			return ErrInvalidInput
		}
		var practiceID string
		if err := tx.QueryRow(ctx, `
			SELECT id::text FROM access_practices
			WHERE provisioning_key = $1
			FOR UPDATE
		`, provision.PracticeKey).Scan(&practiceID); err != nil {
			return ErrInvalidInput
		}
		if provision.LocationKey == "" {
			if _, err := tx.Exec(ctx, `
				DELETE FROM human_calling_outbound_voice_fallbacks
				WHERE practice_id = $1
			`, practiceID); err != nil {
				return fmt.Errorf("remove outbound voice fallback: %w", err)
			}
			continue
		}
		var locationID string
		if err := tx.QueryRow(ctx, `
			SELECT id::text FROM access_locations
			WHERE practice_id = $1 AND provisioning_key = $2
			FOR UPDATE
		`, practiceID, provision.LocationKey).Scan(&locationID); err != nil {
			return ErrInvalidInput
		}
		var voiceCount int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM human_calling_location_voice_numbers
			WHERE practice_id = $1 AND location_id = $2 AND enabled
		`, practiceID, locationID).Scan(&voiceCount); err != nil {
			return fmt.Errorf("read outbound voice fallback number: %w", err)
		}
		if voiceCount != 1 {
			return fmt.Errorf("%w: outbound voice fallback requires one enabled voice number", ErrInvalidInput)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO human_calling_outbound_voice_fallbacks (
				practice_id, location_id
			) VALUES ($1, $2)
			ON CONFLICT (practice_id) DO UPDATE SET
				location_id = EXCLUDED.location_id,
				updated_at = now()
		`, practiceID, locationID); err != nil {
			return fmt.Errorf("provision outbound voice fallback: %w", err)
		}
	}
	return nil
}

func (m *Module) StartOutboundCall(
	ctx context.Context,
	command StartOutboundCallCommand,
) (Call, error) {
	normalizeOutboundCommand(&command)
	if m.work == nil || command.Identity.Subject == "" || command.SessionID == "" ||
		!outboundIdempotencyKey.MatchString(command.IdempotencyKey) ||
		(command.TaskID == "" && (command.PracticeID == "" ||
			command.LocationID == "" || !supportedUSDestination(command.Destination))) ||
		(command.TaskID != "" && (command.PracticeID != "" ||
			command.LocationID != "" || command.Destination != "")) {
		return Call{}, ErrInvalidInput
	}
	fingerprint, err := outboundFingerprint(command)
	if err != nil {
		return Call{}, err
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Call{}, fmt.Errorf("begin outbound Call: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := m.access.LockOperationalActor(ctx, tx, command.Identity); err != nil {
		return Call{}, ErrDenied
	}
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended($1, 0) # hashtextextended($2, 1))
	`, command.Identity.Subject, command.IdempotencyKey); err != nil {
		return Call{}, fmt.Errorf("lock outbound idempotency: %w", err)
	}
	var existingID string
	var existingFingerprint []byte
	err = tx.QueryRow(ctx, `
		SELECT id::text, outbound_input_fingerprint FROM human_calling_calls
		WHERE initiating_subject = $1 AND outbound_idempotency_key = $2 FOR UPDATE
	`, command.Identity.Subject, command.IdempotencyKey).Scan(
		&existingID, &existingFingerprint,
	)
	if err == nil {
		if !hmac.Equal(existingFingerprint, fingerprint[:]) {
			return Call{}, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Call{}, err
		}
		return m.ReadCall(ctx, command.Identity, existingID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Call{}, fmt.Errorf("find replayed outbound Call: %w", err)
	}

	entryPoint := CallEntryStandalone
	taskID, practiceID, locationID, destination := "", command.PracticeID,
		command.LocationID, command.Destination
	if command.TaskID != "" {
		task, err := m.work.LockOpenOutboundTask(ctx, tx, command.TaskID)
		if err != nil {
			return Call{}, err
		}
		entryPoint, taskID = CallEntryTask, task.ID
		practiceID, locationID, destination = task.PracticeID, task.LocationID, task.Phone
	}
	if !supportedUSDestination(destination) {
		return Call{}, ErrInvalidInput
	}
	if _, err := m.access.LockMembershipAuthorization(
		ctx, tx, command.Identity, practiceID, locationID,
	); err != nil {
		return Call{}, ErrDenied
	}
	var leaseReady bool
	if err := tx.QueryRow(ctx, `
		SELECT session_id = $2 AND lease_expires_at > $3
			AND readiness_updated_at > $3 - $4::interval
			AND registered AND microphone_ready AND audio_ready AND session_healthy
		FROM human_calling_softphone_leases
		WHERE user_subject = $1 FOR UPDATE
	`, command.Identity.Subject, command.SessionID, m.now(),
		m.config.ReadinessGrace.String()).Scan(&leaseReady); err != nil || !leaseReady {
		return Call{}, ErrIneligible
	}
	var occupied bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM human_calling_call_legs leg
			WHERE leg.staff_subject = $1 AND leg.role = 'STAFF'
				AND (
					leg.state IN (
						'PENDING', 'DIALING', 'RINGING', 'BRIDGE_PENDING', 'BRIDGED'
					)
					OR (leg.state = 'ENDING' AND leg.answered_at IS NOT NULL)
				)
		)
	`, command.Identity.Subject).Scan(&occupied); err != nil {
		return Call{}, fmt.Errorf("read outbound Call occupancy: %w", err)
	}
	if occupied {
		return Call{}, ErrOccupied
	}
	callerID, err := outboundCallerID(ctx, tx, practiceID, locationID)
	if err != nil {
		return Call{}, err
	}
	var sipUsername string
	if err := tx.QueryRow(ctx, `
		SELECT provider_sip_username FROM human_calling_credentials
		WHERE user_subject = $1 AND state = 'ACTIVE' FOR UPDATE
	`, command.Identity.Subject).Scan(&sipUsername); err != nil {
		return Call{}, ErrIneligible
	}
	if command.RetryOfCallID != "" {
		var retryOwner string
		var retryTerminal *string
		if err := tx.QueryRow(ctx, `
			SELECT initiating_subject, terminal_outcome FROM human_calling_calls
			WHERE id = $1 AND direction = 'OUTBOUND' FOR UPDATE
		`, command.RetryOfCallID).Scan(&retryOwner, &retryTerminal); err != nil ||
			retryOwner != command.Identity.Subject || retryTerminal == nil {
			return Call{}, ErrConflict
		}
	}

	now := m.now()
	callID, callerLegID, staffLegID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_calls (
			id, practice_id, location_id, direction, entry_point, task_id,
			destination_phone, outbound_caller_id, initiating_subject,
			outbound_idempotency_key, outbound_input_fingerprint,
			retry_of_call_id, version, created_at, updated_at
		) VALUES (
			$1, $2, $3, 'OUTBOUND', $4, NULLIF($5, '')::uuid,
			$6, $7, $8, $9, $10, NULLIF($11, '')::uuid, 1, $12, $12
		)
	`, callID, practiceID, locationID, entryPoint, taskID, destination,
		callerID, command.Identity.Subject, command.IdempotencyKey,
		fingerprint[:], command.RetryOfCallID, now); err != nil {
		return Call{}, fmt.Errorf("create outbound Call: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_call_legs (
			id, call_id, role, sequence, state, created_at, updated_at
		) VALUES ($1, $2, 'CALLER', 1, 'PENDING', $3, $3)
	`, callerLegID, callID, now); err != nil {
		return Call{}, fmt.Errorf("create outbound caller CallLeg: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_call_legs (
			id, call_id, role, sequence, staff_subject, staff_session_id,
			state, created_at, updated_at
		) VALUES ($1, $2, 'STAFF', 1, $3, $4, 'PENDING', $5, $5)
	`, staffLegID, callID, command.Identity.Subject, command.SessionID, now); err != nil {
		return Call{}, fmt.Errorf("create outbound Staff CallLeg: %w", err)
	}
	payload := map[string]any{
		"to":               managedSIPDestination(sipUsername, m.config.StaffSIPDomain),
		"connection_id":    m.config.CallControlID,
		"from":             callerID,
		"bridge_on_answer": false,
		"timeout_secs":     int(m.config.RingWindowDuration.Seconds()),
		"webhook_retries_policies": telnyxWebhookRetryPolicies(
			FactCallInitiated,
			FactCallAnswered,
			FactCallHangup,
		),
		"client_state": encodeCallLegClientState(callID, staffLegID, "STAFF", "outbound_media"),
		"custom_headers": []map[string]string{{
			"name": "X-Acuity-Media-Token", "value": m.staffMediaToken(callID, staffLegID),
		}},
	}
	commandID, err := m.insertCallLegCommand(ctx, tx, callID, staffLegID, "",
		command.Identity.Subject, CommandDialOutboundStaff, "", payload, "")
	if err != nil {
		return Call{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_softphone_leases
		SET desired_available = false, version = version + 1, updated_at = $2
		WHERE user_subject = $1 AND session_id = $3
	`, command.Identity.Subject, now, command.SessionID); err != nil {
		return Call{}, fmt.Errorf("reserve outbound softphone: %w", err)
	}
	if err := appendTimeline(ctx, tx, callID, practiceID, "outbound.preparing",
		command.Identity.Subject, "", commandID, "", "", now); err != nil {
		return Call{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return Call{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Call{}, fmt.Errorf("commit outbound Call: %w", err)
	}
	return m.ReadCall(ctx, command.Identity, callID)
}

type outboundCallerIDQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func outboundCallerID(
	ctx context.Context,
	querier outboundCallerIDQuerier,
	practiceID string,
	locationID string,
) (string, error) {
	var callerID string
	var callerIDCount int
	if err := querier.QueryRow(ctx, `
		WITH direct AS (
			SELECT voice.phone
			FROM human_calling_location_voice_numbers voice
			WHERE voice.practice_id = $1
				AND voice.location_id = $2
				AND voice.enabled
		), configured_fallback AS (
			SELECT voice.phone
			FROM human_calling_outbound_voice_fallbacks fallback
			JOIN human_calling_location_voice_numbers voice
				ON voice.practice_id = fallback.practice_id
				AND voice.location_id = fallback.location_id
			WHERE fallback.practice_id = $1
				AND voice.enabled
				AND NOT EXISTS (SELECT 1 FROM direct)
		)
		SELECT count(*), COALESCE(min(phone), '')
		FROM (
			SELECT phone FROM direct
			UNION ALL
			SELECT phone FROM configured_fallback
		) candidates
	`, practiceID, locationID).Scan(&callerIDCount, &callerID); err != nil {
		return "", fmt.Errorf("read outbound caller ID: %w", err)
	}
	if callerIDCount != 1 {
		return "", fmt.Errorf("%w: Location requires one enabled voice number", ErrConflict)
	}
	return callerID, nil
}

func (m *Module) ConfirmOutboundMedia(
	ctx context.Context,
	command ConfirmOutboundMediaCommand,
) (Call, error) {
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.CallID = strings.TrimSpace(command.CallID)
	command.MediaToken = strings.TrimSpace(command.MediaToken)
	if command.Identity.Subject == "" || command.SessionID == "" ||
		command.CallID == "" || command.MediaToken == "" {
		return Call{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Call{}, fmt.Errorf("begin outbound media confirmation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID, locationID, destination, callerID, terminal string
	if err := tx.QueryRow(ctx, `
		SELECT practice_id::text, location_id::text, destination_phone,
			outbound_caller_id, COALESCE(terminal_outcome, '')
		FROM human_calling_calls
		WHERE id = $1 AND direction = 'OUTBOUND' FOR UPDATE
	`, command.CallID).Scan(&practiceID, &locationID, &destination,
		&callerID, &terminal); err != nil || terminal != "" {
		return Call{}, ErrConflict
	}
	if _, err := m.access.LockMembershipAuthorization(ctx, tx, command.Identity,
		practiceID, locationID); err != nil {
		return Call{}, ErrDenied
	}
	var staffLegID, staffControlID, staffSession, staffState string
	var answered bool
	if err := tx.QueryRow(ctx, `
		SELECT id::text, COALESCE(provider_call_control_id, ''),
			COALESCE(staff_session_id, ''), state, answered_at IS NOT NULL
		FROM human_calling_call_legs
		WHERE call_id = $1 AND role = 'STAFF' AND staff_subject = $2 FOR UPDATE
	`, command.CallID, command.Identity.Subject).Scan(&staffLegID, &staffControlID,
		&staffSession, &staffState, &answered); err != nil {
		return Call{}, ErrConflict
	}
	var ownsLease bool
	if err := tx.QueryRow(ctx, `
		SELECT session_id = $2 AND lease_expires_at > $3
		FROM human_calling_softphone_leases WHERE user_subject = $1 FOR UPDATE
	`, command.Identity.Subject, command.SessionID, m.now()).Scan(&ownsLease); err != nil {
		return Call{}, ErrConflict
	}
	if !ownsLease || staffSession != command.SessionID || !answered ||
		!hmac.Equal([]byte(m.staffMediaToken(command.CallID, staffLegID)),
			[]byte(command.MediaToken)) ||
		(staffState != "BRIDGE_PENDING" && staffState != "BRIDGED") {
		return Call{}, ErrConflict
	}
	var existingDestinationLeg string
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM human_calling_call_legs
		WHERE call_id = $1 AND role = 'DESTINATION' ORDER BY sequence DESC LIMIT 1
	`, command.CallID).Scan(&existingDestinationLeg)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return Call{}, err
		}
		return m.ReadCall(ctx, command.Identity, command.CallID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Call{}, err
	}
	destinationLegID := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_call_legs (
			id, call_id, role, sequence, state, created_at, updated_at
		) VALUES ($1, $2, 'DESTINATION', 1, 'PENDING', $3, $3)
	`, destinationLegID, command.CallID, m.now()); err != nil {
		return Call{}, fmt.Errorf("create outbound destination CallLeg: %w", err)
	}
	_, err = m.insertCallLegCommand(ctx, tx, command.CallID, destinationLegID,
		staffLegID, command.Identity.Subject, CommandDialOutboundDestination,
		staffControlID, map[string]any{
			"to": destination, "connection_id": m.config.CallControlID,
			"from": callerID, "link_to": staffControlID,
			"bridge_intent": true, "bridge_on_answer": false, "timeout_secs": 30,
			"answering_machine_detection": "disabled",
			"webhook_retries_policies": telnyxWebhookRetryPolicies(
				FactCallInitiated,
				FactCallAnswered,
				FactCallHangup,
			),
			"client_state": encodeCallLegClientState(
				command.CallID, destinationLegID, "DESTINATION", "dial",
			),
		}, "")
	if err != nil {
		return Call{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1, updated_at = $2 WHERE id = $1
	`, command.CallID, m.now()); err != nil {
		return Call{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Call{}, fmt.Errorf("commit outbound media confirmation: %w", err)
	}
	return m.ReadCall(ctx, command.Identity, command.CallID)
}

func (m *Module) applyOutboundDestinationFact(
	ctx context.Context,
	fact ProviderFact,
	callID string,
) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || state.CallID != callID || state.Role != "DESTINATION" ||
		fact.CallControlID == "" || fact.CallLegID == "" || fact.CallSessionID == "" {
		return ErrConflict
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin outbound destination projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var practiceID, terminal string
	if err := tx.QueryRow(ctx, `
		SELECT practice_id::text, COALESCE(terminal_outcome, '')
		FROM human_calling_calls
		WHERE id = $1 AND direction = 'OUTBOUND' FOR UPDATE
	`, callID).Scan(&practiceID, &terminal); err != nil {
		return fmt.Errorf("lock outbound Call: %w", err)
	}
	var priorState, storedConnectionID, storedControlID, storedLegID, storedSessionID string
	if err := tx.QueryRow(ctx, `
		SELECT state, COALESCE(provider_connection_id, ''),
			COALESCE(provider_call_control_id, ''),
			COALESCE(provider_call_leg_id, ''),
			COALESCE(provider_call_session_id, '')
		FROM human_calling_call_legs
		WHERE id = $1 AND call_id = $2 AND role = 'DESTINATION'
		FOR UPDATE
	`, state.CallLegID, callID).Scan(
		&priorState, &storedConnectionID, &storedControlID, &storedLegID, &storedSessionID,
	); err != nil {
		return fmt.Errorf("lock outbound destination CallLeg: %w", err)
	}
	if (storedConnectionID != "" && fact.ConnectionID != storedConnectionID) ||
		(storedControlID != "" && fact.CallControlID != storedControlID) ||
		(storedLegID != "" && fact.CallLegID != storedLegID) ||
		(storedSessionID != "" && fact.CallSessionID != storedSessionID) {
		return ErrConflict
	}
	nextState := "RINGING"
	if fact.Type == FactCallAnswered {
		nextState = "BRIDGE_PENDING"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_call_legs SET
			provider_connection_id = COALESCE(provider_connection_id, NULLIF($2, '')),
			provider_call_control_id = COALESCE(provider_call_control_id, $3),
			provider_call_leg_id = COALESCE(provider_call_leg_id, $4),
			provider_call_session_id = COALESCE(provider_call_session_id, NULLIF($5, '')),
			state = CASE
				WHEN $6 = 'BRIDGE_PENDING' AND state IN ('PENDING', 'DIALING', 'RINGING')
					THEN 'BRIDGE_PENDING'
				WHEN $6 = 'RINGING' AND state IN ('PENDING', 'DIALING') THEN 'RINGING'
				ELSE state
			END,
			answered_at = CASE WHEN $6 = 'BRIDGE_PENDING'
				THEN COALESCE(answered_at, $7) ELSE answered_at END,
			bridge_pending_at = CASE WHEN $6 = 'BRIDGE_PENDING'
				THEN COALESCE(bridge_pending_at, $7) ELSE bridge_pending_at END,
			updated_at = $8
		WHERE id = $1 AND call_id = $9 AND role = 'DESTINATION'
	`, state.CallLegID, fact.ConnectionID, fact.CallControlID, fact.CallLegID,
		fact.CallSessionID, nextState, fact.OccurredAt, m.now(), callID); err != nil {
		return fmt.Errorf("project outbound destination CallLeg: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands SET state = 'RECONCILED',
			sent_at = COALESCE(sent_at, $2), last_error_code = NULL, updated_at = $3
		WHERE call_leg_id = $1 AND action = 'DIAL_OUTBOUND_DESTINATION'
			AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
	`, state.CallLegID, fact.OccurredAt, m.now()); err != nil {
		return fmt.Errorf("reconcile outbound destination Dial: %w", err)
	}
	if terminal != "" || priorState == "ENDING" || priorState == "ENDED" ||
		priorState == "FAILED" {
		if _, err := m.insertCallLegCommand(
			ctx, tx, callID, state.CallLegID, "", "", CommandHangupLeg,
			fact.CallControlID,
			map[string]any{"client_state": encodeCallLegClientState(
				callID, state.CallLegID, "DESTINATION", "late_fact_cleanup",
			)},
			"",
		); err != nil {
			return err
		}
	} else if fact.Type == FactCallAnswered &&
		(priorState == "PENDING" || priorState == "DIALING" || priorState == "RINGING") {
		var staffLegID, staffControlID string
		if err := tx.QueryRow(ctx, `
			SELECT id::text, provider_call_control_id FROM human_calling_call_legs
			WHERE call_id = $1 AND role = 'STAFF' AND state = 'BRIDGE_PENDING'
			FOR UPDATE
		`, callID).Scan(&staffLegID, &staffControlID); err != nil {
			return fmt.Errorf("lock outbound Staff CallLeg: %w", err)
		}
		if _, err := m.insertCallLegCommand(ctx, tx, callID, state.CallLegID,
			staffLegID, "", CommandBridge, fact.CallControlID, map[string]any{
				"call_control_id": staffControlID, "prevent_double_bridge": true,
				"client_state": encodeCallLegClientState(
					callID, state.CallLegID, "DESTINATION", "bridge",
				),
			}, ""); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1, updated_at = $2 WHERE id = $1
	`, callID, m.now()); err != nil {
		return err
	}
	if err := appendTimeline(ctx, tx, callID, practiceID,
		"provider.destination."+strings.TrimPrefix(string(fact.Type), "call."), "",
		fact.EventID, "", opaqueReference(fact.CallLegID), "", fact.OccurredAt); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *Module) applyOutboundBridge(ctx context.Context, fact ProviderFact) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || state.Role != "DESTINATION" || state.Kind != "bridge" {
		return ErrConflict
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin outbound Bridge projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var practiceID, staffLegID, staffState string
	var destinationControlID, destinationProviderLegID, destinationSessionID string
	if err := tx.QueryRow(ctx, `
		SELECT call.practice_id::text, staff.id::text, staff.state,
			destination.provider_call_control_id, destination.provider_call_leg_id,
			COALESCE(destination.provider_call_session_id, '')
		FROM human_calling_calls call
		JOIN human_calling_call_legs destination
			ON destination.call_id = call.id AND destination.id = $2
		JOIN human_calling_call_legs staff
			ON staff.call_id = call.id AND staff.role = 'STAFF'
		WHERE call.id = $1 AND destination.role = 'DESTINATION'
		FOR UPDATE OF call, destination, staff
	`, state.CallID, state.CallLegID).Scan(
		&practiceID, &staffLegID, &staffState, &destinationControlID,
		&destinationProviderLegID, &destinationSessionID,
	); err != nil {
		return fmt.Errorf("lock outbound bridged CallLegs: %w", err)
	}
	if fact.CallControlID != destinationControlID ||
		fact.CallLegID != destinationProviderLegID ||
		fact.CallSessionID != destinationSessionID {
		return ErrConflict
	}
	historicalBridge, err := projectBridgeEvidence(
		ctx, tx, state.CallID, state.CallLegID, fact.OccurredAt, m.now(),
	)
	if err != nil {
		return fmt.Errorf("confirm outbound Bridge: %w", err)
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
		UPDATE human_calling_provider_commands SET state = 'RECONCILED',
			sent_at = COALESCE(sent_at, $2), last_error_code = NULL, updated_at = $3
		WHERE call_leg_id = $1 AND action = 'BRIDGE'
			AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
	`, state.CallLegID, fact.OccurredAt, m.now()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1, updated_at = $2 WHERE id = $1
	`, state.CallID, m.now()); err != nil {
		return err
	}
	if staffState == "BRIDGED" || historicalUpgrade {
		if err := appendTimeline(ctx, tx, state.CallID, practiceID, "call.connected",
			"", fact.EventID, "", opaqueReference(fact.CallLegID), "",
			fact.OccurredAt); err != nil {
			return err
		}
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *Module) applyOutboundStaffBridge(ctx context.Context, fact ProviderFact) error {
	state, ok := parseCallLegClientState(fact.ClientState)
	if !ok || state.Role != "STAFF" {
		return ErrConflict
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin outbound Staff Bridge projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil || !claimed {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var practiceID, controlID, providerLegID, sessionID string
	var destinationBridged bool
	if err := tx.QueryRow(ctx, `
		SELECT call.practice_id::text, staff.provider_call_control_id,
			staff.provider_call_leg_id, COALESCE(staff.provider_call_session_id, ''),
			EXISTS (
				SELECT 1 FROM human_calling_call_legs destination
				WHERE destination.call_id = call.id AND destination.role = 'DESTINATION'
					AND destination.state = 'BRIDGED'
			)
		FROM human_calling_calls call
		JOIN human_calling_call_legs staff
			ON staff.call_id = call.id AND staff.id = $2 AND staff.role = 'STAFF'
		WHERE call.id = $1 AND call.direction = 'OUTBOUND'
		FOR UPDATE OF call, staff
	`, state.CallID, state.CallLegID).Scan(
		&practiceID, &controlID, &providerLegID, &sessionID, &destinationBridged,
	); err != nil {
		return fmt.Errorf("lock outbound Staff Bridge CallLeg: %w", err)
	}
	if fact.CallControlID != controlID || fact.CallLegID != providerLegID ||
		fact.CallSessionID != sessionID {
		return ErrConflict
	}
	historicalBridge, err := projectBridgeEvidence(
		ctx, tx, state.CallID, state.CallLegID, fact.OccurredAt, m.now(),
	)
	if err != nil {
		return fmt.Errorf("confirm outbound Staff Bridge: %w", err)
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
	if destinationBridged || historicalUpgrade {
		if err := appendTimeline(ctx, tx, state.CallID, practiceID, "call.connected",
			"", fact.EventID, "", opaqueReference(fact.CallLegID), "",
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

func normalizeOutboundCommand(command *StartOutboundCallCommand) {
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.TaskID = strings.TrimSpace(command.TaskID)
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.LocationID = strings.TrimSpace(command.LocationID)
	command.Destination = strings.TrimSpace(command.Destination)
	command.RetryOfCallID = strings.TrimSpace(command.RetryOfCallID)
}

func supportedUSDestination(phone string) bool {
	if !validUSNumber(phone) {
		return false
	}
	digits := strings.TrimPrefix(phone, "+1")
	return digits[:3] != "900" && digits[3:6] != "976"
}

func validUSNumber(phone string) bool {
	if !usPhoneNumber.MatchString(phone) {
		return false
	}
	digits := strings.TrimPrefix(phone, "+1")
	return digits[1:3] != "11" && digits[4:6] != "11"
}

func outboundFingerprint(command StartOutboundCallCommand) ([32]byte, error) {
	encoded, err := json.Marshal(struct {
		Subject, SessionID, TaskID, PracticeID, LocationID, Destination, RetryOf string
	}{command.Identity.Subject, command.SessionID, command.TaskID, command.PracticeID,
		command.LocationID, command.Destination, command.RetryOfCallID})
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode outbound fingerprint: %w", err)
	}
	return sha256.Sum256(encoded), nil
}
