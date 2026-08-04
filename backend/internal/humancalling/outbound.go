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
	"time"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var outboundIdempotencyKey = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`,
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
		return OutboundEligibility{
			Reason: "Complete Tasks must be reopened before calling.",
		}, nil
	}
	if !supportedUSDestination(task.Phone) {
		return OutboundEligibility{
			Reason: "This Task does not have a supported US destination.",
		}, nil
	}
	var count int
	if err := m.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM human_calling_location_voice_numbers
		WHERE practice_id = $1
			AND location_id = $2
			AND enabled
	`, task.PracticeID, task.LocationID).Scan(&count); err != nil {
		return OutboundEligibility{}, fmt.Errorf(
			"read Task Location voice configuration: %w",
			err,
		)
	}
	if count == 0 {
		return OutboundEligibility{
			Reason: "Calling is not configured for this Task's office.",
		}, nil
	}
	if count > 1 {
		return OutboundEligibility{
			Reason: "This Task's office has conflicting caller ID configuration.",
		}, nil
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
	for _, provision := range provisions {
		provision.PracticeKey = strings.TrimSpace(provision.PracticeKey)
		provision.LocationKey = strings.TrimSpace(provision.LocationKey)
		provision.Number = strings.TrimSpace(provision.Number)
		provision.VoicemailGreeting = strings.TrimSpace(
			provision.VoicemailGreeting,
		)
		if provision.VoicemailGreeting == "" {
			provision.VoicemailGreeting = defaultVoicemailGreeting
		}
		if provision.PracticeKey == "" ||
			provision.LocationKey == "" ||
			!validUSNumber(provision.Number) ||
			utf8.RuneCountInString(provision.VoicemailGreeting) > 2000 {
			return ErrInvalidInput
		}
		var practiceID, locationID string
		if err := tx.QueryRow(ctx, `
			SELECT practice.id::text, location.id::text
			FROM access_practices practice
			JOIN access_locations location
				ON location.practice_id = practice.id
			WHERE practice.provisioning_key = $1
				AND location.provisioning_key = $2
			FOR UPDATE OF location
		`, provision.PracticeKey, provision.LocationKey).Scan(
			&practiceID,
			&locationID,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInvalidInput
			}
			return fmt.Errorf("resolve provisioned Location voice: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM human_calling_location_voice_numbers
			WHERE practice_id = $1
				AND location_id = $2
				AND phone <> $3
		`, practiceID, locationID, provision.Number); err != nil {
			return fmt.Errorf("reconcile Location voice number: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO human_calling_location_voice_numbers (
				practice_id,
				location_id,
				phone,
				enabled,
				voicemail_greeting
			)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (practice_id, location_id, phone)
			DO UPDATE SET
				enabled = EXCLUDED.enabled,
				voicemail_greeting = EXCLUDED.voicemail_greeting,
				updated_at = now()
		`, practiceID, locationID, provision.Number, provision.Enabled,
			provision.VoicemailGreeting); err != nil {
			return fmt.Errorf("provision Location voice number: %w", err)
		}
		var configured, enabled int
		if err := tx.QueryRow(ctx, `
			SELECT count(*), count(*) FILTER (WHERE enabled)
			FROM human_calling_location_voice_numbers
			WHERE practice_id = $1
				AND location_id = $2
		`, practiceID, locationID).Scan(&configured, &enabled); err != nil {
			return fmt.Errorf("verify Location voice number: %w", err)
		}
		expectedEnabled := 0
		if provision.Enabled {
			expectedEnabled = 1
		}
		if configured != 1 || enabled != expectedEnabled {
			return fmt.Errorf(
				"verify Location voice number: configured=%d enabled=%d",
				configured,
				enabled,
			)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Location voice provisioning: %w", err)
	}
	return nil
}

func (m *Module) StartOutboundCall(
	ctx context.Context,
	command StartOutboundCallCommand,
) (Call, error) {
	normalizeOutboundCommand(&command)
	if m.work == nil ||
		command.Identity.Subject == "" ||
		command.SessionID == "" ||
		!outboundIdempotencyKey.MatchString(command.IdempotencyKey) ||
		(command.TaskID == "" &&
			(command.PracticeID == "" ||
				command.LocationID == "" ||
				!supportedUSDestination(command.Destination))) ||
		(command.TaskID != "" &&
			(command.PracticeID != "" ||
				command.LocationID != "" ||
				command.Destination != "")) {
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
	if _, err := m.access.LockOperationalActor(
		ctx,
		tx,
		command.Identity,
	); err != nil {
		return Call{}, ErrDenied
	}
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(
			hashtextextended($1, 0)
				# hashtextextended($2, 1)
				# 53464936
		)
	`, command.Identity.Subject, command.IdempotencyKey); err != nil {
		return Call{}, fmt.Errorf("lock outbound idempotency key: %w", err)
	}

	var existingID string
	var existingFingerprint []byte
	err = tx.QueryRow(ctx, `
		SELECT id::text, outbound_input_fingerprint
		FROM human_calling_calls
		WHERE initiating_subject = $1
			AND outbound_idempotency_key = $2
		FOR UPDATE
	`, command.Identity.Subject, command.IdempotencyKey).Scan(
		&existingID,
		&existingFingerprint,
	)
	if err == nil {
		if !hmac.Equal(existingFingerprint, fingerprint[:]) {
			return Call{}, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Call{}, fmt.Errorf("commit replayed outbound Call: %w", err)
		}
		return m.ReadCall(ctx, command.Identity, existingID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Call{}, fmt.Errorf("find replayed outbound Call: %w", err)
	}

	entryPoint := CallEntryStandalone
	taskID := ""
	practiceID := command.PracticeID
	locationID := command.LocationID
	destination := command.Destination
	if command.TaskID != "" {
		task, err := m.work.LockOpenOutboundTask(ctx, tx, command.TaskID)
		if err != nil {
			return Call{}, err
		}
		entryPoint = CallEntryTask
		taskID = task.ID
		practiceID = task.PracticeID
		locationID = task.LocationID
		destination = task.Phone
	}
	if !supportedUSDestination(destination) {
		return Call{}, ErrInvalidInput
	}
	if _, err := m.access.LockMembershipAuthorization(
		ctx,
		tx,
		command.Identity,
		practiceID,
		locationID,
	); err != nil {
		return Call{}, ErrDenied
	}

	var leaseReady, priorAvailability bool
	if err := tx.QueryRow(ctx, `
		SELECT
			session_id = $2
				AND lease_expires_at > $3
				AND readiness_updated_at > $3 - $4::interval
				AND registered
				AND microphone_ready
				AND audio_ready
				AND session_healthy,
			desired_available
		FROM human_calling_softphone_leases
		WHERE user_subject = $1
		FOR UPDATE
	`, command.Identity.Subject, command.SessionID, m.now(),
		m.config.ReadinessGrace.String(),
	).Scan(&leaseReady, &priorAvailability); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Call{}, ErrIneligible
		}
		return Call{}, fmt.Errorf("lock outbound softphone readiness: %w", err)
	}
	if !leaseReady {
		return Call{}, ErrIneligible
	}
	var callerID string
	var callerIDCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*), COALESCE(min(phone), '')
		FROM human_calling_location_voice_numbers
		WHERE practice_id = $1
			AND location_id = $2
			AND enabled
	`, practiceID, locationID).Scan(
		&callerIDCount,
		&callerID,
	); err != nil {
		return Call{}, fmt.Errorf("resolve Location voice number: %w", err)
	}
	if callerIDCount != 1 {
		return Call{}, fmt.Errorf(
			"%w: Location requires exactly one enabled voice number",
			ErrConflict,
		)
	}
	var sipUsername string
	if err := tx.QueryRow(ctx, `
		SELECT provider_sip_username
		FROM human_calling_credentials
		WHERE user_subject = $1 AND state = 'ACTIVE'
		FOR UPDATE
	`, command.Identity.Subject).Scan(&sipUsername); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Call{}, ErrIneligible
		}
		return Call{}, fmt.Errorf("load outbound SIP credential: %w", err)
	}
	if command.RetryOfCallID != "" {
		var retryState CallState
		var retryPriorAvailability bool
		if err := tx.QueryRow(ctx, `
			SELECT state, prior_availability_intent
			FROM human_calling_calls
			WHERE id = $1
				AND direction = 'OUTBOUND'
				AND initiating_subject = $2
				AND state IN ('UNANSWERED', 'RESOLVED', 'RECONCILING')
			FOR UPDATE
		`, command.RetryOfCallID, command.Identity.Subject).Scan(
			&retryState,
			&retryPriorAvailability,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Call{}, ErrConflict
			}
			return Call{}, fmt.Errorf("validate outbound retry: %w", err)
		}
		priorAvailability = retryPriorAvailability
		if retryState == CallReconciling {
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_calls
				SET
					state = 'RESOLVED',
					ended_at = COALESCE(ended_at, $2),
					version = version + 1,
					updated_at = $2
				WHERE id = $1 AND state = 'RECONCILING'
			`, command.RetryOfCallID, m.now()); err != nil {
				return Call{}, fmt.Errorf(
					"close explicit unknown retry source: %w",
					err,
				)
			}
		}
	}

	callID := uuid.NewString()
	attemptID := uuid.NewString()
	commandID := uuid.NewString()
	createdAt := m.now()
	connectionDeadline := createdAt.Add(m.config.ConnectionTimeout)
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_calls (
			id,
			handoff_id,
			practice_id,
			location_id,
			state,
			offer_deadline,
			connection_deadline,
			caller_call_control_id,
			caller_call_leg_id,
			call_session_id,
			claimant_subject,
			claimant_session_id,
			current_attempt_id,
			direction,
			entry_point,
			task_id,
			destination_phone,
			outbound_caller_id,
			initiating_subject,
			outbound_idempotency_key,
			outbound_input_fingerprint,
			retry_of_call_id,
			prior_availability_intent,
			created_at,
			updated_at
		)
		VALUES (
			$1, NULL, $2, $3, 'PREPARING', $4, $5,
			NULL, NULL, NULL, $6, $7, $8, 'OUTBOUND', $9,
			NULLIF($10, '')::uuid, $11, $12, $6, $13, $14,
			NULLIF($15, '')::uuid, $16, $17, $17
		)
	`, callID, practiceID, locationID, createdAt.Add(30*time.Second),
		connectionDeadline, command.Identity.Subject, command.SessionID,
		attemptID, entryPoint, taskID, destination, callerID,
		command.IdempotencyKey, fingerprint[:], command.RetryOfCallID,
		priorAvailability, createdAt,
	); err != nil {
		if isUniqueViolation(err) {
			return Call{}, ErrConflict
		}
		return Call{}, fmt.Errorf("create outbound Call: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_connection_attempts (
			id,
			call_id,
			claimant_subject,
			claimant_session_id,
			connection_deadline,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
	`, attemptID, callID, command.Identity.Subject, command.SessionID,
		connectionDeadline, createdAt,
	); err != nil {
		return Call{}, fmt.Errorf("create outbound media attempt: %w", err)
	}
	payload := map[string]any{
		"to":            managedSIPDestination(sipUsername, m.config.StaffSIPDomain),
		"connection_id": m.config.CallControlID,
		"from":          callerID,
		"media_prep":    true,
		"client_state":  opaqueClientState(callID, "staff", attemptID),
		"timeout_secs":  int(m.config.ConnectionTimeout.Seconds()),
		"custom_headers": []map[string]string{{
			"name":  "X-Acuity-Media-Token",
			"value": m.staffMediaToken(callID, attemptID),
		}},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Call{}, fmt.Errorf("encode outbound media Dial: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			id,
			call_id,
			attempt_id,
			user_subject,
			action,
			payload,
			next_attempt_at
		)
		VALUES ($1, $2, $3, $4, 'DIAL_STAFF', $5, $6)
	`, commandID, callID, attemptID, command.Identity.Subject, encoded,
		createdAt,
	); err != nil {
		return Call{}, fmt.Errorf("commit outbound media Dial: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_softphone_leases
		SET desired_available = false, version = version + 1, updated_at = $2
		WHERE user_subject = $1 AND session_id = $3
	`, command.Identity.Subject, createdAt, command.SessionID); err != nil {
		return Call{}, fmt.Errorf("reserve outbound softphone: %w", err)
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"outbound.preparing",
		command.Identity.Subject,
		"",
		commandID,
		"",
		"",
		createdAt,
	); err != nil {
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

func (m *Module) ConfirmOutboundMedia(
	ctx context.Context,
	command ConfirmOutboundMediaCommand,
) (Call, error) {
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.CallID = strings.TrimSpace(command.CallID)
	command.MediaToken = strings.TrimSpace(command.MediaToken)
	if command.Identity.Subject == "" ||
		command.SessionID == "" ||
		command.CallID == "" ||
		command.MediaToken == "" {
		return Call{}, ErrInvalidInput
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Call{}, fmt.Errorf("begin outbound media confirmation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := m.access.LockOperationalActor(
		ctx,
		tx,
		command.Identity,
	); err != nil {
		return Call{}, ErrDenied
	}

	var practiceID, locationID, attemptID string
	var claimantSubject, claimantSession string
	var staffControlID, staffLegID, destination, callerID string
	var state CallState
	var staffAnsweredAt, mediaReadyAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT
			call.practice_id::text,
			call.location_id::text,
			call.state,
			call.current_attempt_id::text,
			call.claimant_subject,
			call.claimant_session_id,
			call.expected_staff_call_control_id,
			call.expected_staff_call_leg_id,
			call.destination_phone,
			call.outbound_caller_id,
			attempt.staff_answered_at,
			attempt.media_ready_at
		FROM human_calling_calls call
		JOIN human_calling_connection_attempts attempt
			ON attempt.id = call.current_attempt_id
		WHERE call.id = $1
			AND call.direction = 'OUTBOUND'
		FOR UPDATE OF call, attempt
	`, command.CallID).Scan(
		&practiceID,
		&locationID,
		&state,
		&attemptID,
		&claimantSubject,
		&claimantSession,
		&staffControlID,
		&staffLegID,
		&destination,
		&callerID,
		&staffAnsweredAt,
		&mediaReadyAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Call{}, ErrConflict
		}
		return Call{}, fmt.Errorf("lock outbound media attempt: %w", err)
	}
	if _, err := m.access.LockMembershipAuthorization(
		ctx,
		tx,
		command.Identity,
		practiceID,
		locationID,
	); err != nil {
		return Call{}, ErrDenied
	}
	var ownsLease bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM human_calling_softphone_leases
			WHERE user_subject = $1
				AND session_id = $2
				AND lease_expires_at > $3
		)
	`, command.Identity.Subject, command.SessionID, m.now()).Scan(
		&ownsLease,
	); err != nil {
		return Call{}, fmt.Errorf("confirm outbound media lease: %w", err)
	}
	expectedToken := m.staffMediaToken(command.CallID, attemptID)
	confirmationPending := mediaReadyAt == nil &&
		(state == CallPreparing || state == CallReconciling)
	confirmationComplete := mediaReadyAt != nil &&
		(state == CallRinging ||
			state == CallConnecting ||
			state == CallReconciling ||
			state == CallConnected)
	if !ownsLease ||
		claimantSubject != command.Identity.Subject ||
		claimantSession != command.SessionID ||
		!hmac.Equal([]byte(expectedToken), []byte(command.MediaToken)) ||
		staffAnsweredAt == nil ||
		(!confirmationPending && !confirmationComplete) {
		return Call{}, ErrConflict
	}
	if confirmationPending {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_connection_attempts
			SET media_ready_at = COALESCE(media_ready_at, $2), updated_at = $2
			WHERE id = $1 AND ended_at IS NULL
		`, attemptID, m.now()); err != nil {
			return Call{}, fmt.Errorf("record outbound media readiness: %w", err)
		}
		if err := insertCommand(
			ctx,
			tx,
			command.CallID,
			claimantSubject,
			CommandDialDestination,
			staffControlID,
			map[string]any{
				"to":                          destination,
				"connection_id":               m.config.CallControlID,
				"from":                        callerID,
				"link_to":                     staffControlID,
				"bridge_intent":               true,
				"bridge_on_answer":            true,
				"answering_machine_detection": "disabled",
				"timeout_secs":                30,
				"client_state": opaqueClientState(
					command.CallID,
					"destination",
					attemptID,
				),
			},
			m.now(),
		); err != nil {
			return Call{}, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET
				state = 'RINGING',
				connection_deadline = $2,
				version = version + 1,
				updated_at = $3
			WHERE id = $1
				AND current_attempt_id = $4
				AND state IN ('PREPARING', 'RECONCILING')
		`, command.CallID, m.now().Add(30*time.Second), m.now(),
			attemptID); err != nil {
			return Call{}, fmt.Errorf("start outbound destination ringing: %w", err)
		}
		if err := appendTimeline(
			ctx,
			tx,
			command.CallID,
			practiceID,
			"outbound.staff_media_ready",
			command.Identity.Subject,
			"",
			"",
			opaqueReference(staffLegID),
			"",
			m.now(),
		); err != nil {
			return Call{}, err
		}
		if _, err := m.access.RecordWorkspaceChange(
			ctx,
			tx,
			practiceID,
		); err != nil {
			return Call{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Call{}, fmt.Errorf("commit outbound media readiness: %w", err)
	}
	return m.ReadCall(ctx, command.Identity, command.CallID)
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
	areaCode := digits[:3]
	exchange := digits[3:6]
	return areaCode != "900" && exchange != "976"
}

func validUSNumber(phone string) bool {
	if !regexp.MustCompile(`^\+1[2-9][0-9]{2}[2-9][0-9]{6}$`).MatchString(phone) {
		return false
	}
	digits := strings.TrimPrefix(phone, "+1")
	areaCode := digits[:3]
	exchange := digits[3:6]
	return areaCode[1:] != "11" && exchange[1:] != "11"
}

func outboundFingerprint(
	command StartOutboundCallCommand,
) ([32]byte, error) {
	encoded, err := json.Marshal(struct {
		Subject       string `json:"subject"`
		SessionID     string `json:"sessionId"`
		TaskID        string `json:"taskId"`
		PracticeID    string `json:"practiceId"`
		LocationID    string `json:"locationId"`
		Destination   string `json:"destination"`
		RetryOfCallID string `json:"retryOfCallId"`
	}{
		Subject:       command.Identity.Subject,
		SessionID:     command.SessionID,
		TaskID:        command.TaskID,
		PracticeID:    command.PracticeID,
		LocationID:    command.LocationID,
		Destination:   command.Destination,
		RetryOfCallID: command.RetryOfCallID,
	})
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode outbound Call fingerprint: %w", err)
	}
	return sha256.Sum256(encoded), nil
}

func (m *Module) isOutboundCall(
	ctx context.Context,
	callID string,
) (bool, error) {
	var direction CallDirection
	if err := m.pool.QueryRow(ctx, `
		SELECT direction
		FROM human_calling_calls
		WHERE id = $1
	`, callID).Scan(&direction); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrConflict
		}
		return false, fmt.Errorf("read Call direction: %w", err)
	}
	return direction == CallOutbound, nil
}

func (m *Module) restoreOutboundAvailability(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
	occurredAt time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_softphone_leases lease
		SET
			desired_available = call.prior_availability_intent,
			version = lease.version + 1,
			updated_at = $2
		FROM human_calling_calls call
		WHERE call.id = $1
			AND call.direction = 'OUTBOUND'
			AND lease.user_subject = call.initiating_subject
	`, callID, occurredAt); err != nil {
		return fmt.Errorf("restore outbound availability intent: %w", err)
	}
	return nil
}

func (m *Module) expireOutboundCalls(ctx context.Context) (int, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin outbound Call expiry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT
			id::text,
			practice_id::text,
			COALESCE(current_attempt_id::text, ''),
			initiating_subject,
			COALESCE(expected_staff_call_control_id, ''),
			COALESCE(destination_call_control_id, ''),
			state,
			entry_point
		FROM human_calling_calls
		WHERE direction = 'OUTBOUND'
			AND connection_deadline <= $1
			AND (
				state = 'RINGING'
				OR (
					state IN ('PREPARING', 'RECONCILING')
					AND NOT EXISTS (
						SELECT 1
						FROM human_calling_provider_commands command
						WHERE command.call_id = human_calling_calls.id
							AND command.action = 'DIAL_DESTINATION'
					)
				)
			)
		ORDER BY connection_deadline, id
		FOR UPDATE SKIP LOCKED
		LIMIT 100
	`, m.now())
	if err != nil {
		return 0, fmt.Errorf("claim expired outbound Calls: %w", err)
	}
	type expiredCall struct {
		id                 string
		practiceID         string
		attemptID          string
		initiatingSubject  string
		staffControl       string
		destinationControl string
		state              CallState
		entryPoint         CallEntryPoint
	}
	expired := []expiredCall{}
	for rows.Next() {
		var call expiredCall
		if err := rows.Scan(
			&call.id,
			&call.practiceID,
			&call.attemptID,
			&call.initiatingSubject,
			&call.staffControl,
			&call.destinationControl,
			&call.state,
			&call.entryPoint,
		); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired outbound Call: %w", err)
		}
		expired = append(expired, call)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate expired outbound Calls: %w", err)
	}
	rows.Close()
	for _, call := range expired {
		if call.state != CallRinging {
			nextState := CallResolved
			if call.entryPoint == CallEntryStandalone {
				nextState = CallUnanswered
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_calls
				SET
					state = $2,
					provider_termination = 'MEDIA_READINESS_FAILED',
					ended_at = $3,
					version = version + 1,
					updated_at = $3
				WHERE id = $1
					AND state = $4
			`, call.id, nextState, m.now(), call.state); err != nil {
				return 0, fmt.Errorf(
					"expire outbound media preparation: %w",
					err,
				)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_connection_attempts
				SET
					ended_at = COALESCE(ended_at, $2),
					provider_termination = 'MEDIA_READINESS_FAILED',
					updated_at = $2
				WHERE id = NULLIF($1, '')::uuid
			`, call.attemptID, m.now()); err != nil {
				return 0, fmt.Errorf(
					"end expired outbound media attempt: %w",
					err,
				)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE human_calling_provider_commands
				SET
					state = 'FAILED',
					last_error_code = 'MEDIA_READINESS_TIMEOUT',
					updated_at = $2
				WHERE call_id = $1
					AND action = 'DIAL_STAFF'
					AND state = 'PENDING'
			`, call.id, m.now()); err != nil {
				return 0, fmt.Errorf(
					"cancel expired outbound staff Dial: %w",
					err,
				)
			}
			if call.staffControl != "" {
				if err := ensureHangupCommand(
					ctx,
					tx,
					call.id,
					call.attemptID,
					call.initiatingSubject,
					call.staffControl,
					"staff",
					m.now(),
				); err != nil {
					return 0, err
				}
			}
			if err := m.restoreOutboundAvailability(
				ctx,
				tx,
				call.id,
				m.now(),
			); err != nil {
				return 0, err
			}
			if err := appendTimeline(
				ctx,
				tx,
				call.id,
				call.practiceID,
				"outbound.media_readiness_timeout",
				call.initiatingSubject,
				"",
				"",
				"",
				"MEDIA_READINESS_FAILED",
				m.now(),
			); err != nil {
				return 0, err
			}
			if _, err := m.access.RecordWorkspaceChange(
				ctx,
				tx,
				call.practiceID,
			); err != nil {
				return 0, err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET
				state = 'RECONCILING',
				provider_termination = 'STATUS_UNKNOWN',
				version = version + 1,
				updated_at = $2
			WHERE id = $1 AND state = 'RINGING'
		`, call.id, m.now()); err != nil {
			return 0, fmt.Errorf("mark outbound ring timeout unknown: %w", err)
		}
		targets := []struct {
			id  string
			leg string
		}{}
		if call.destinationControl != "" {
			targets = append(targets, struct {
				id  string
				leg string
			}{id: call.destinationControl, leg: "destination"})
		}
		if call.staffControl != "" {
			targets = append(targets, struct {
				id  string
				leg string
			}{id: call.staffControl, leg: "staff"})
		}
		for _, target := range targets {
			if err := ensureHangupCommand(
				ctx,
				tx,
				call.id,
				call.attemptID,
				call.initiatingSubject,
				target.id,
				target.leg,
				m.now(),
			); err != nil {
				return 0, err
			}
		}
		if err := appendTimeline(
			ctx,
			tx,
			call.id,
			call.practiceID,
			"outbound.ring_timeout_reconciling",
			call.initiatingSubject,
			"",
			"",
			"",
			"STATUS_UNKNOWN",
			m.now(),
		); err != nil {
			return 0, err
		}
		if _, err := m.access.RecordWorkspaceChange(
			ctx,
			tx,
			call.practiceID,
		); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit outbound Call expiry: %w", err)
	}
	return len(expired), nil
}

func (m *Module) applyOutboundBridge(
	ctx context.Context,
	fact ProviderFact,
	callID string,
) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin outbound bridge projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil {
		return err
	}
	if !claimed {
		return tx.Commit(ctx)
	}

	var practiceID, currentAttemptID, initiatingSubject string
	var staffControlID, staffLegID, destinationControlID, destinationLegID string
	var callSessionID string
	var state CallState
	var entryPoint CallEntryPoint
	var connectedAt, endedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT
			practice_id::text,
			state,
			entry_point,
			COALESCE(current_attempt_id::text, ''),
			initiating_subject,
			COALESCE(expected_staff_call_control_id, ''),
			COALESCE(expected_staff_call_leg_id, ''),
			COALESCE(destination_call_control_id, ''),
			COALESCE(destination_call_leg_id, ''),
			COALESCE(call_session_id, ''),
			connected_at,
			ended_at
		FROM human_calling_calls
		WHERE id = $1 AND direction = 'OUTBOUND'
		FOR UPDATE
	`, callID).Scan(
		&practiceID,
		&state,
		&entryPoint,
		&currentAttemptID,
		&initiatingSubject,
		&staffControlID,
		&staffLegID,
		&destinationControlID,
		&destinationLegID,
		&callSessionID,
		&connectedAt,
		&endedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("lock outbound bridge Call: %w", err)
	}
	clientState, ok := parseOpaqueClientState(fact.ClientState)
	if !ok ||
		clientState.CallID != callID ||
		(clientState.AttemptID != "" &&
			clientState.AttemptID != currentAttemptID) ||
		(callSessionID != "" && fact.CallSessionID != callSessionID) {
		return ErrConflict
	}
	switch clientState.Leg {
	case "destination":
		if destinationControlID != "" &&
			destinationControlID != fact.CallControlID {
			return ErrConflict
		}
		if destinationLegID != "" && destinationLegID != fact.CallLegID {
			return ErrConflict
		}
		destinationControlID = fact.CallControlID
		destinationLegID = fact.CallLegID
	case "staff":
		if staffControlID != "" && staffControlID != fact.CallControlID {
			return ErrConflict
		}
		if staffLegID != "" && staffLegID != fact.CallLegID {
			return ErrConflict
		}
		staffControlID = fact.CallControlID
		staffLegID = fact.CallLegID
	default:
		return ErrConflict
	}
	if staffControlID == "" ||
		staffLegID == "" ||
		destinationControlID == "" ||
		destinationLegID == "" {
		return ErrConflict
	}
	lateTaskBridge := state == CallResolved &&
		entryPoint == CallEntryTask &&
		connectedAt == nil &&
		endedAt != nil &&
		!fact.OccurredAt.After(*endedAt)
	if !lateTaskBridge &&
		(state == CallResolved ||
			state == CallFollowUpRequired ||
			state == CallNeedsDisposition) {
		return tx.Commit(ctx)
	}
	if !lateTaskBridge &&
		state != CallRinging &&
		state != CallReconciling &&
		state != CallConnected {
		return ErrConflict
	}
	if connectedAt != nil {
		return tx.Commit(ctx)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE human_calling_connection_attempts
		SET
			staff_call_control_id = COALESCE(staff_call_control_id, $2),
			staff_call_leg_id = COALESCE(staff_call_leg_id, $3),
			bridge_occurred_at = CASE
				WHEN bridge_occurred_at IS NULL OR $4 < bridge_occurred_at THEN $4
				ELSE bridge_occurred_at
			END,
			updated_at = $5
		WHERE id = $1
			AND (ended_at IS NULL OR $4 <= ended_at)
	`, currentAttemptID, staffControlID, staffLegID, fact.OccurredAt,
		m.now())
	if err != nil {
		return fmt.Errorf("project outbound bridge attempt: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	nextState := CallConnected
	dispositionDeadline := fact.OccurredAt.Add(m.config.DispositionDuration)
	if lateTaskBridge {
		nextState = CallNeedsDisposition
		dispositionDeadline = endedAt.Add(m.config.DispositionDuration)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET
			state = $7,
			winner_subject = $2,
			destination_call_control_id = $3,
			destination_call_leg_id = $4,
			provider_termination = CASE
				WHEN $7 = 'CONNECTED' THEN NULL
				ELSE provider_termination
			END,
			connected_at = $5,
			disposition_deadline = CASE
				WHEN $7 = 'NEEDS_DISPOSITION' THEN $8
				ELSE NULL
			END,
			version = version + 1,
			updated_at = $6
		WHERE id = $1
	`, callID, initiatingSubject, destinationControlID, destinationLegID,
		fact.OccurredAt, m.now(), nextState,
		dispositionDeadline); err != nil {
		return fmt.Errorf("project outbound bridge: %w", err)
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"call.connected",
		initiatingSubject,
		fact.EventID,
		"",
		opaqueReference(fact.CallLegID),
		"",
		fact.OccurredAt,
	); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit outbound bridge projection: %w", err)
	}
	return nil
}

func (m *Module) applyOutboundHangup(
	ctx context.Context,
	fact ProviderFact,
	callID string,
) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin outbound hangup projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil {
		return err
	}
	if !claimed {
		return tx.Commit(ctx)
	}

	var practiceID, currentAttemptID, callSessionID string
	var staffControlID, destinationControlID string
	var state CallState
	var entryPoint CallEntryPoint
	if err := tx.QueryRow(ctx, `
		SELECT
			practice_id::text,
			state,
			entry_point,
			COALESCE(current_attempt_id::text, ''),
			COALESCE(expected_staff_call_control_id, ''),
			COALESCE(destination_call_control_id, ''),
			COALESCE(call_session_id, '')
		FROM human_calling_calls
		WHERE id = $1 AND direction = 'OUTBOUND'
		FOR UPDATE
	`, callID).Scan(
		&practiceID,
		&state,
		&entryPoint,
		&currentAttemptID,
		&staffControlID,
		&destinationControlID,
		&callSessionID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("lock outbound hangup Call: %w", err)
	}
	clientState, ok := parseOpaqueClientState(fact.ClientState)
	if !ok ||
		clientState.CallID != callID ||
		(clientState.AttemptID != "" &&
			clientState.AttemptID != currentAttemptID) ||
		(callSessionID != "" && fact.CallSessionID != callSessionID) {
		return ErrConflict
	}
	switch clientState.Leg {
	case "staff":
		if staffControlID != "" && staffControlID != fact.CallControlID {
			return ErrConflict
		}
	case "destination":
		if destinationControlID != "" &&
			destinationControlID != fact.CallControlID {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_calls
			SET
				destination_call_control_id = COALESCE(
					destination_call_control_id,
					$2
				),
				destination_call_leg_id = COALESCE(
					destination_call_leg_id,
					$3
				),
				updated_at = $4
			WHERE id = $1
		`, callID, fact.CallControlID, fact.CallLegID, m.now()); err != nil {
			return fmt.Errorf("record terminating destination leg: %w", err)
		}
	default:
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET
			state = 'RECONCILED',
			sent_at = COALESCE(sent_at, $3),
			last_error_code = NULL,
			updated_at = $4
		WHERE call_id = $1
			AND (
				(action = 'HANGUP' AND target_id = $2)
				OR (
					action = 'DIAL_DESTINATION'
					AND $5 = 'destination'
					AND state IN ('SENDING', 'AMBIGUOUS')
				)
			)
			AND state IN ('SENDING', 'SENT', 'AMBIGUOUS')
	`, callID, fact.CallControlID, fact.OccurredAt, m.now(),
		clientState.Leg); err != nil {
		return fmt.Errorf("reconcile outbound hangup evidence: %w", err)
	}
	if state == CallResolved || state == CallFollowUpRequired {
		return tx.Commit(ctx)
	}
	if state == CallNeedsDisposition {
		return tx.Commit(ctx)
	}

	nextState := CallNeedsDisposition
	if state != CallConnected {
		nextState = CallUnanswered
		if entryPoint == CallEntryTask {
			nextState = CallResolved
		}
	}
	termination := outboundTermination(fact.HangupCause)
	if clientState.Leg == "staff" && state != CallConnected {
		termination = "MEDIA_FAILURE"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_connection_attempts
		SET
			ended_at = CASE
				WHEN ended_at IS NULL OR $2 < ended_at THEN $2
				ELSE ended_at
			END,
			provider_termination = $3,
			updated_at = $4
		WHERE id = NULLIF($1, '')::uuid
	`, currentAttemptID, fact.OccurredAt, termination, m.now()); err != nil {
		return fmt.Errorf("end outbound attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET
			state = $2,
			provider_termination = $3,
			ended_at = $4,
			disposition_deadline = CASE
				WHEN $2 = 'NEEDS_DISPOSITION' THEN $6
				ELSE NULL
			END,
			version = version + 1,
			updated_at = $5
		WHERE id = $1
			AND state IN (
				'PREPARING',
				'RINGING',
				'CONNECTED',
				'RECONCILING'
			)
	`, callID, nextState, termination, fact.OccurredAt, m.now(),
		fact.OccurredAt.Add(m.config.DispositionDuration)); err != nil {
		return fmt.Errorf("project outbound termination: %w", err)
	}
	if err := m.restoreOutboundAvailability(
		ctx,
		tx,
		callID,
		m.now(),
	); err != nil {
		return err
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		"call.terminated",
		"",
		fact.EventID,
		"",
		opaqueReference(fact.CallLegID),
		termination,
		fact.OccurredAt,
	); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit outbound hangup projection: %w", err)
	}
	return nil
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
	case "":
		return "FAILED"
	default:
		return "FAILED"
	}
}

func (m *Module) applyOutboundDestinationFact(
	ctx context.Context,
	fact ProviderFact,
	callID string,
) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin outbound destination projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, err := claimProviderFact(ctx, tx, fact, m.now())
	if err != nil {
		return err
	}
	if !claimed {
		return tx.Commit(ctx)
	}
	var practiceID, callSessionID string
	var state CallState
	if err := tx.QueryRow(ctx, `
		SELECT practice_id::text, state, COALESCE(call_session_id, '')
		FROM human_calling_calls
		WHERE id = $1 AND direction = 'OUTBOUND'
		FOR UPDATE
	`, callID).Scan(
		&practiceID,
		&state,
		&callSessionID,
	); err != nil {
		return fmt.Errorf("lock outbound destination Call: %w", err)
	}
	if callSessionID != "" && callSessionID != fact.CallSessionID {
		return ErrConflict
	}
	if state != CallRinging &&
		state != CallReconciling &&
		state != CallConnected {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_calls
		SET
			destination_call_control_id = COALESCE(
				destination_call_control_id,
				$2
			),
			destination_call_leg_id = COALESCE(destination_call_leg_id, $3),
			version = version + 1,
			updated_at = $4
		WHERE id = $1
	`, callID, fact.CallControlID, fact.CallLegID, m.now()); err != nil {
		return fmt.Errorf("project outbound destination leg: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET
			state = 'RECONCILED',
			sent_at = COALESCE(sent_at, $2),
			last_error_code = NULL,
			updated_at = $2
		WHERE call_id = $1
			AND action = 'DIAL_DESTINATION'
			AND state IN ('SENDING', 'AMBIGUOUS')
	`, callID, fact.OccurredAt); err != nil {
		return fmt.Errorf("reconcile outbound destination Dial: %w", err)
	}
	kind := "provider.destination_leg.initiated"
	if fact.Type == FactCallAnswered {
		kind = "provider.destination_leg.answered"
	}
	if err := appendTimeline(
		ctx,
		tx,
		callID,
		practiceID,
		kind,
		"",
		fact.EventID,
		"",
		opaqueReference(fact.CallLegID),
		"",
		fact.OccurredAt,
	); err != nil {
		return err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit outbound destination projection: %w", err)
	}
	return nil
}
