package interaction

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type receiptState string

const (
	receiptPending     receiptState = "PENDING"
	receiptProjected   receiptState = "PROJECTED"
	receiptQuarantined receiptState = "QUARANTINED"
	receiptRetired     receiptState = "RETIRED"
)

type acceptedReceipt struct {
	ID                  string
	ServiceSubject      string
	PracticeID          string
	LocationID          string
	SourceCallID        string
	State               receiptState
	InteractionID       string
	ProjectionErrorCode string
}

type storedReceiptPayload struct {
	Kind            MessageKind          `json:"kind"`
	OfficeKey       string               `json:"officeKey,omitempty"`
	SourceCallID    string               `json:"sourceCallId"`
	CallerPhone     string               `json:"callerPhone"`
	OfficePhone     string               `json:"officePhone"`
	StartedAt       time.Time            `json:"startedAt"`
	EndedAt         *time.Time           `json:"endedAt,omitempty"`
	Status          CallStatus           `json:"status"`
	Summary         string               `json:"summary,omitempty"`
	Transcript      json.RawMessage      `json:"transcript,omitempty"`
	Appointment     *AppointmentEvidence `json:"appointmentOutcome,omitempty"`
	CloseoutPayload json.RawMessage      `json:"closeoutPayload,omitempty"`
}

const (
	maxReceiptProjectionAttempts = 12
	firstReceiptRetryDelay       = 5 * time.Second
	maxReceiptRetryDelay         = 30 * time.Minute
	receiptRetryExhausted        = "PROJECTION_RETRY_EXHAUSTED"
	receiptFailureRecordTimeout  = 5 * time.Second
)

func newStoredReceiptPayload(command IngestCommand) storedReceiptPayload {
	return storedReceiptPayload{
		Kind:            command.Kind,
		OfficeKey:       command.OfficeKey,
		SourceCallID:    command.SourceCallID,
		CallerPhone:     command.CallerPhone,
		OfficePhone:     command.OfficePhone,
		StartedAt:       command.StartedAt,
		EndedAt:         command.EndedAt,
		Status:          command.Status,
		Summary:         command.Summary,
		Transcript:      command.Transcript,
		Appointment:     command.Appointment,
		CloseoutPayload: command.CloseoutPayload,
	}
}

func receiptCommand(receipt acceptedReceipt, raw []byte) (IngestCommand, LifecycleStage, bool) {
	var payload storedReceiptPayload
	if json.Unmarshal(raw, &payload) != nil {
		return IngestCommand{}, 0, false
	}
	command := IngestCommand{
		Service:         access.ServiceIdentity{Subject: receipt.ServiceSubject},
		Kind:            payload.Kind,
		OfficeKey:       payload.OfficeKey,
		SourceCallID:    payload.SourceCallID,
		CallerPhone:     payload.CallerPhone,
		OfficePhone:     payload.OfficePhone,
		StartedAt:       payload.StartedAt,
		EndedAt:         payload.EndedAt,
		Status:          payload.Status,
		Summary:         payload.Summary,
		Transcript:      payload.Transcript,
		Appointment:     payload.Appointment,
		CloseoutPayload: payload.CloseoutPayload,
	}
	normalizeCommand(&command)
	stage := messageLifecycleStage(command.Kind)
	return command, stage, stage != 0 && validCommand(command) && command.SourceCallID == receipt.SourceCallID
}

func (m *Module) ProcessNextReceipt(ctx context.Context) (bool, error) {
	if m.database == nil {
		return false, errUnavailable
	}
	receipt, raw, attempts, claimed, err := m.claimReceipt(ctx)
	if err != nil || !claimed {
		return false, err
	}
	command, stage, valid := receiptCommand(receipt, raw)
	if !valid {
		return true, m.quarantinePendingReceipt(ctx, receipt.ID, "INVALID_RECEIPT")
	}
	_, _, err = m.projectReceipt(
		ctx,
		receipt,
		command,
		stage,
		m.now().UTC().Truncate(time.Microsecond),
	)
	if err == nil || errors.Is(err, ErrConflict) {
		return true, nil
	}
	if recordErr := m.recordReceiptFailure(ctx, receipt.ID, attempts, err); recordErr != nil {
		return true, errors.Join(err, recordErr)
	}
	return true, fmt.Errorf("project AI Interaction receipt attempt %d: %w", attempts, err)
}

func (m *Module) claimReceipt(ctx context.Context) (acceptedReceipt, []byte, int, bool, error) {
	now := m.now().UTC().Truncate(time.Microsecond)
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return acceptedReceipt{}, nil, 0, false, fmt.Errorf("begin AI Interaction receipt claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var receipt acceptedReceipt
	var raw []byte
	var attempts int
	err = tx.QueryRow(ctx, `
		SELECT
			id::text,
			service_subject,
			practice_id::text,
			location_id::text,
			source_call_id,
			state,
			payload,
			projection_attempts
		FROM ai_interaction_receipts
		WHERE state = 'PENDING'
			AND kind IN ('START', 'OUTCOME_CHECKPOINT', 'CLOSEOUT')
			AND (next_attempt_at IS NULL OR next_attempt_at <= $1)
		ORDER BY received_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, now).Scan(
		&receipt.ID,
		&receipt.ServiceSubject,
		&receipt.PracticeID,
		&receipt.LocationID,
		&receipt.SourceCallID,
		&receipt.State,
		&raw,
		&attempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return acceptedReceipt{}, nil, 0, false, nil
	}
	if err != nil {
		return acceptedReceipt{}, nil, 0, false, fmt.Errorf("claim pending AI Interaction receipt: %w", err)
	}
	attempts++
	if _, err := tx.Exec(ctx, `
		UPDATE ai_interaction_receipts
		SET projection_attempts = $2, last_attempt_at = $3, next_attempt_at = $4
		WHERE id = $1
	`, receipt.ID, attempts, now, now.Add(receiptRetryDelay(attempts))); err != nil {
		return acceptedReceipt{}, nil, 0, false, fmt.Errorf("count AI Interaction receipt attempt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return acceptedReceipt{}, nil, 0, false, fmt.Errorf("commit AI Interaction receipt claim: %w", err)
	}
	return receipt, raw, attempts, true, nil
}

func receiptRetryDelay(attempt int) time.Duration {
	delay := firstReceiptRetryDelay
	for current := 1; current < attempt && delay < maxReceiptRetryDelay; current++ {
		delay *= 2
	}
	return min(delay, maxReceiptRetryDelay)
}

func receiptFailureCode(err error) string {
	var databaseErr *pgconn.PgError
	switch {
	case errors.As(err, &databaseErr):
		return "DATABASE_" + databaseErr.Code
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "TIMEOUT"
	default:
		return "PROJECTION_FAILED"
	}
}

func (m *Module) recordReceiptFailure(ctx context.Context, receiptID string, attempts int, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), receiptFailureRecordTimeout)
	defer cancel()
	if _, err := m.database.Exec(ctx, `
		UPDATE ai_interaction_receipts
		SET
			last_error_code = $3,
			state = CASE WHEN $4 THEN 'QUARANTINED' ELSE state END,
			projection_error_code = CASE WHEN $4 THEN $5 ELSE projection_error_code END
		WHERE id = $1 AND state = 'PENDING' AND projection_attempts = $2
	`, receiptID, attempts, receiptFailureCode(cause), attempts >= maxReceiptProjectionAttempts, receiptRetryExhausted); err != nil {
		return fmt.Errorf("record AI Interaction receipt failure: %w", err)
	}
	return nil
}

func receiptPayload(command IngestCommand) ([]byte, [32]byte, error) {
	payload, err := json.Marshal(newStoredReceiptPayload(command))
	if err != nil {
		return nil, [32]byte{}, err
	}
	return payload, sha256.Sum256(payload), nil
}

func (m *Module) acceptReceipt(
	ctx context.Context,
	command IngestCommand,
	payload []byte,
	fingerprint [32]byte,
	receivedAt time.Time,
) (acceptedReceipt, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return acceptedReceipt{}, fmt.Errorf("begin AI Interaction receipt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.authorizeReceipt(ctx, tx, command)
	if err != nil {
		return acceptedReceipt{}, err
	}
	receipt := acceptedReceipt{
		ServiceSubject: command.Service.Subject,
		PracticeID:     authorization.PracticeID,
		LocationID:     authorization.LocationID,
		SourceCallID:   command.SourceCallID,
		State:          receiptPending,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO ai_interaction_receipts (
			service_subject, practice_id, location_id, source_call_id,
			kind, payload_fingerprint, payload, received_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (practice_id, source_call_id, payload_fingerprint) DO NOTHING
		RETURNING id::text
	`,
		receipt.ServiceSubject,
		receipt.PracticeID,
		receipt.LocationID,
		receipt.SourceCallID,
		command.Kind,
		fingerprint[:],
		payload,
		receivedAt,
	).Scan(&receipt.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return acceptedReceipt{}, fmt.Errorf("record AI Interaction receipt: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `
			SELECT
				id::text,
				service_subject,
				location_id::text,
				state,
				COALESCE(interaction_id::text, ''),
				COALESCE(projection_error_code, '')
			FROM ai_interaction_receipts
			WHERE practice_id = $1
				AND source_call_id = $2
				AND payload_fingerprint = $3
			FOR UPDATE
		`, receipt.PracticeID, receipt.SourceCallID, fingerprint[:]).Scan(
			&receipt.ID,
			&receipt.ServiceSubject,
			&receipt.LocationID,
			&receipt.State,
			&receipt.InteractionID,
			&receipt.ProjectionErrorCode,
		); err != nil {
			return acceptedReceipt{}, fmt.Errorf("load duplicate AI Interaction receipt: %w", err)
		}
		if receipt.ServiceSubject != command.Service.Subject ||
			receipt.LocationID != authorization.LocationID {
			return acceptedReceipt{}, ErrConflict
		}
		if _, err := tx.Exec(ctx, `
			UPDATE ai_interaction_receipts
			SET duplicate_count = duplicate_count + 1
			WHERE id = $1
		`, receipt.ID); err != nil {
			return acceptedReceipt{}, fmt.Errorf("count duplicate AI Interaction receipt: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return acceptedReceipt{}, fmt.Errorf("commit AI Interaction receipt: %w", err)
	}
	return receipt, nil
}

func (m *Module) authorizeReceipt(
	ctx context.Context,
	tx pgx.Tx,
	command IngestCommand,
) (access.ServiceAuthorization, error) {
	var authorization access.ServiceAuthorization
	var err error
	if command.OfficeKey != "" {
		authorization, err = m.access.LockServiceAuthorization(
			ctx, tx, command.Service, command.OfficeKey,
			access.ServiceCapabilityIngestAIInteraction,
		)
	} else {
		authorization, err = m.access.LockServiceVoiceAuthorization(
			ctx, tx, command.Service, command.OfficePhone,
			access.ServiceCapabilityIngestAIInteraction,
		)
	}
	if errors.Is(err, access.ErrDenied) {
		return access.ServiceAuthorization{}, ErrDenied
	}
	if err != nil {
		return access.ServiceAuthorization{}, fmt.Errorf("authorize AI Interaction receipt: %w", err)
	}
	return authorization, nil
}

func (m *Module) projectReceipt(
	ctx context.Context,
	receipt acceptedReceipt,
	command IngestCommand,
	stage LifecycleStage,
	projectedAt time.Time,
) (Interaction, UpsertStatus, error) {
	return m.projectReceiptWithRecovery(ctx, receipt, command, stage, projectedAt, nil)
}

func (m *Module) projectReceiptWithRecovery(
	ctx context.Context,
	receipt acceptedReceipt,
	command IngestCommand,
	stage LifecycleStage,
	projectedAt time.Time,
	operator *access.Identity,
) (Interaction, UpsertStatus, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Interaction{}, "", fmt.Errorf("begin AI Interaction projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `
		SELECT
			state,
			COALESCE(interaction_id::text, ''),
			COALESCE(projection_error_code, '')
		FROM ai_interaction_receipts
		WHERE id = $1
		FOR UPDATE
	`, receipt.ID).Scan(
		&receipt.State,
		&receipt.InteractionID,
		&receipt.ProjectionErrorCode,
	); err != nil {
		return Interaction{}, "", fmt.Errorf("lock AI Interaction receipt: %w", err)
	}
	var authorization access.Authorization
	if operator != nil {
		authorization, err = m.authorize(ctx, tx, *operator, receipt.PracticeID, receipt.LocationID, true, audienceOperator)
		if err != nil {
			return Interaction{}, "", err
		}
	}
	if receipt.State == receiptQuarantined && (operator == nil || receipt.ProjectionErrorCode != "SOURCE_CONFLICT") {
		return Interaction{}, "", ErrConflict
	}
	if receipt.State == receiptRetired {
		return Interaction{}, "", ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))
	`, receipt.PracticeID, receipt.SourceCallID); err != nil {
		return Interaction{}, "", fmt.Errorf("lock AI Interaction source: %w", err)
	}
	current, found, err := lockBySourceCall(
		ctx,
		tx,
		receipt.PracticeID,
		receipt.SourceCallID,
	)
	if err != nil {
		return Interaction{}, "", err
	}
	if receipt.State == receiptProjected {
		if !found || current.ID != receipt.InteractionID {
			return Interaction{}, "", fmt.Errorf("projected AI Interaction receipt is inconsistent")
		}
		if err := tx.Commit(ctx); err != nil {
			return Interaction{}, "", fmt.Errorf("commit projected AI Interaction receipt read: %w", err)
		}
		return current, StatusUpdated, nil
	}
	if operator != nil {
		if !found || receipt.State != receiptQuarantined ||
			current.ServiceSubject != receipt.ServiceSubject ||
			current.LocationID != receipt.LocationID || current.Phone != command.CallerPhone ||
			current.OfficePhone != command.OfficePhone ||
			current.StartedAt.Equal(command.StartedAt) ||
			(command.EndedAt != nil && command.EndedAt.Before(current.StartedAt)) {
			return Interaction{}, "", ErrConflict
		}
		command.StartedAt = current.StartedAt
	}
	status := StatusUpdated
	if !found {
		current = Interaction{
			ID:                 uuid.NewString(),
			ServiceSubject:     receipt.ServiceSubject,
			PracticeID:         receipt.PracticeID,
			LocationID:         receipt.LocationID,
			SourceCallID:       receipt.SourceCallID,
			Phone:              command.CallerPhone,
			OfficePhone:        command.OfficePhone,
			StartedAt:          command.StartedAt,
			AppointmentOutcome: OutcomeIndeterminate,
			CreatedAt:          projectedAt,
			UpdatedAt:          projectedAt,
		}
		status = StatusCreated
	} else if current.ServiceSubject != receipt.ServiceSubject ||
		current.LocationID != receipt.LocationID ||
		current.Phone != command.CallerPhone ||
		current.OfficePhone != command.OfficePhone ||
		!current.StartedAt.Equal(command.StartedAt) {
		return m.quarantineReceipt(ctx, tx, receipt.ID, "SOURCE_CONFLICT")
	}
	if err := applyMessage(&current, command, stage, projectedAt); err != nil {
		if operator != nil {
			return Interaction{}, "", err
		}
		if errors.Is(err, ErrConflict) {
			return m.quarantineReceipt(ctx, tx, receipt.ID, "EVIDENCE_CONFLICT")
		}
		return Interaction{}, "", err
	}
	if err := save(ctx, tx, current, found); err != nil {
		return Interaction{}, "", err
	}
	if current.AppointmentOccurredAt != nil && current.AppointmentAction != "" {
		if err := m.work.EnsureAppointmentReview(ctx, tx, current.ID, current.PracticeID, current.LocationID, current.Phone, current.SourceCallID, string(current.AppointmentAction), appointmentReviewMessage(current), *current.AppointmentOccurredAt); err != nil {
			return Interaction{}, "", err
		}
	}
	recoveryCompleted := int64(0)
	if current.AppointmentOccurredAt != nil &&
		current.AppointmentOutcome == OutcomeBooking &&
		resultStatus(current.BookingResult) == "booked" {
		recoveryCompleted, err = m.work.ResolveRecoveryTasks(
			ctx,
			tx,
			work.ResolveRecoveryTasksCommand{
				PracticeID: current.PracticeID,
				Phone:      current.Phone,
				OccurredAt: *current.AppointmentOccurredAt,
				Kind:       work.RecoveryResolutionBooking,
				SourceID:   current.ID,
			},
		)
		if err != nil {
			return Interaction{}, "", err
		}
	}
	if reviewableOutcome(current) || recoveryCompleted > 0 || operator != nil {
		if _, err := m.access.RecordWorkspaceChange(
			ctx,
			tx,
			current.PracticeID,
		); err != nil {
			return Interaction{}, "", err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ai_interaction_receipts
		SET
			state = 'PROJECTED',
			interaction_id = $2,
			projection_error_code = NULL,
			projected_at = $3
		WHERE id = $1 AND state = $4
	`, receipt.ID, current.ID, projectedAt, receipt.State); err != nil {
		return Interaction{}, "", fmt.Errorf("complete AI Interaction receipt: %w", err)
	}
	if operator != nil {
		if err := m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{
			Action: "ai_interaction.source_clock_recovered", ResourceType: "ai_interaction_receipt",
			ResourceID: receipt.ID, ResourceVersion: 1, OccurredAt: projectedAt,
		}); err != nil {
			return Interaction{}, "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Interaction{}, "", fmt.Errorf("commit AI Interaction projection: %w", err)
	}
	return current, status, nil
}

func reviewableOutcome(interaction Interaction) bool {
	return interaction.AppointmentAction == AppointmentBooked ||
		interaction.AppointmentAction == AppointmentCancelled ||
		interaction.AppointmentAction == AppointmentRescheduled ||
		interaction.Status == CallFailed ||
		interaction.Status == CallEscalated ||
		interaction.AppointmentOutcome == OutcomePartial
}

func (m *Module) quarantineReceipt(
	ctx context.Context,
	tx pgx.Tx,
	receiptID string,
	errorCode string,
) (Interaction, UpsertStatus, error) {
	if _, err := tx.Exec(ctx, `
		UPDATE ai_interaction_receipts
		SET state = 'QUARANTINED', projection_error_code = $2
		WHERE id = $1 AND state = 'PENDING'
	`, receiptID, errorCode); err != nil {
		return Interaction{}, "", fmt.Errorf("quarantine AI Interaction receipt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Interaction{}, "", fmt.Errorf("commit quarantined AI Interaction receipt: %w", err)
	}
	return Interaction{}, "", ErrConflict
}

func (m *Module) quarantinePendingReceipt(
	ctx context.Context,
	receiptID string,
	errorCode string,
) error {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin invalid AI Interaction receipt quarantine: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, _, err = m.quarantineReceipt(ctx, tx, receiptID, errorCode)
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}
