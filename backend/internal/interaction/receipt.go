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
)

type receiptState string

const (
	receiptPending     receiptState = "PENDING"
	receiptProjected   receiptState = "PROJECTED"
	receiptQuarantined receiptState = "QUARANTINED"
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

// ProcessNextReceipt projects one durable receipt left pending by an interrupted
// or transiently failed HTTP ingestion attempt.
func (m *Module) ProcessNextReceipt(ctx context.Context) (bool, error) {
	if m.database == nil {
		return false, ErrInvalidInput
	}
	var (
		receipt acceptedReceipt
		payload storedReceiptPayload
		raw     []byte
	)
	err := m.database.QueryRow(ctx, `
		SELECT
			id::text,
			service_subject,
			practice_id::text,
			location_id::text,
			source_call_id,
			state,
			payload
		FROM ai_interaction_receipts
		WHERE state = 'PENDING'
			AND kind IN ('START', 'OUTCOME_CHECKPOINT', 'CLOSEOUT')
		ORDER BY received_at, id
		LIMIT 1
	`).Scan(
		&receipt.ID,
		&receipt.ServiceSubject,
		&receipt.PracticeID,
		&receipt.LocationID,
		&receipt.SourceCallID,
		&receipt.State,
		&raw,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load pending AI Interaction receipt: %w", err)
	}
	if json.Unmarshal(raw, &payload) != nil {
		return true, m.quarantinePendingReceipt(ctx, receipt.ID, "INVALID_RECEIPT")
	}
	command := IngestCommand{
		Service: access.ServiceIdentity{
			Subject: receipt.ServiceSubject,
		},
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
	if stage == 0 || !validCommand(command) ||
		command.SourceCallID != receipt.SourceCallID {
		return true, m.quarantinePendingReceipt(ctx, receipt.ID, "INVALID_RECEIPT")
	}
	_, _, err = m.projectReceipt(
		ctx,
		receipt,
		command,
		stage,
		m.now().UTC().Truncate(time.Microsecond),
	)
	if errors.Is(err, ErrConflict) {
		return true, nil
	}
	return true, err
}

func receiptPayload(command IngestCommand) ([]byte, [32]byte, error) {
	payload, err := json.Marshal(storedReceiptPayload{
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
	})
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
	voiceAuthorization, err := m.access.LockServiceVoiceAuthorization(
		ctx,
		tx,
		command.Service,
		command.OfficePhone,
		access.ServiceCapabilityIngestAIInteraction,
	)
	if err != nil {
		if errors.Is(err, access.ErrDenied) {
			return access.ServiceAuthorization{}, ErrDenied
		}
		return access.ServiceAuthorization{}, fmt.Errorf("authorize AI Interaction access: %w", err)
	}
	if command.OfficeKey == "" {
		return voiceAuthorization, nil
	}
	officeAuthorization, err := m.access.LockServiceAuthorization(
		ctx,
		tx,
		command.Service,
		command.OfficeKey,
		access.ServiceCapabilityIngestAIInteraction,
	)
	if err != nil {
		if errors.Is(err, access.ErrDenied) {
			return access.ServiceAuthorization{}, ErrDenied
		}
		return access.ServiceAuthorization{}, fmt.Errorf("authorize AI Interaction office: %w", err)
	}
	if officeAuthorization.PracticeID != voiceAuthorization.PracticeID ||
		officeAuthorization.LocationID != voiceAuthorization.LocationID {
		return access.ServiceAuthorization{}, ErrDenied
	}
	return officeAuthorization, nil
}

func (m *Module) projectReceipt(
	ctx context.Context,
	receipt acceptedReceipt,
	command IngestCommand,
	stage LifecycleStage,
	projectedAt time.Time,
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
	if receipt.State == receiptQuarantined {
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
		if errors.Is(err, ErrConflict) {
			return m.quarantineReceipt(ctx, tx, receipt.ID, "EVIDENCE_CONFLICT")
		}
		return Interaction{}, "", err
	}
	if err := save(ctx, tx, current, found); err != nil {
		return Interaction{}, "", err
	}
	attentionChanged, err := syncOutcomeAttention(ctx, tx, current, projectedAt)
	if err != nil {
		return Interaction{}, "", err
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
	if attentionChanged || recoveryCompleted > 0 {
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
		WHERE id = $1 AND state = 'PENDING'
	`, receipt.ID, current.ID, projectedAt); err != nil {
		return Interaction{}, "", fmt.Errorf("complete AI Interaction receipt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Interaction{}, "", fmt.Errorf("commit AI Interaction projection: %w", err)
	}
	return current, status, nil
}

func syncOutcomeAttention(
	ctx context.Context,
	tx pgx.Tx,
	interaction Interaction,
	createdAt time.Time,
) (bool, error) {
	reviewable := interaction.AppointmentAction == AppointmentBooked ||
		interaction.AppointmentAction == AppointmentCancelled ||
		interaction.AppointmentAction == AppointmentRescheduled ||
		interaction.Status == CallFailed ||
		interaction.Status == CallEscalated ||
		interaction.AppointmentOutcome == OutcomePartial
	attentionAt := outcomeAttentionAt(interaction)
	removed, err := tx.Exec(ctx, `
		DELETE FROM ai_interaction_attention
		WHERE interaction_id = $1
			AND reviewed_at IS NULL
			AND (NOT $2 OR outcome_occurred_at <> $3)
	`, interaction.ID, reviewable, attentionAt)
	if err != nil {
		return false, fmt.Errorf("clear obsolete AI Interaction attention: %w", err)
	}
	if !reviewable {
		return removed.RowsAffected() > 0, nil
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO ai_interaction_attention (
			interaction_id,
			user_subject,
			outcome_occurred_at,
			created_at
		)
		SELECT
			$1,
			operational_scope.user_subject,
			$2,
			$3
		FROM access_operational_scopes operational_scope
		WHERE operational_scope.practice_id = $4
			AND (
				operational_scope.location_scope = 'ALL'
				OR EXISTS (
					SELECT 1
					FROM access_membership_locations location_grant
					WHERE location_grant.membership_id = operational_scope.membership_id
						AND location_grant.practice_id = operational_scope.practice_id
						AND location_grant.location_id = $5
				)
			)
		ON CONFLICT (interaction_id, user_subject, outcome_occurred_at)
		DO NOTHING
	`, interaction.ID, attentionAt, createdAt,
		interaction.PracticeID, interaction.LocationID)
	if err != nil {
		return false, fmt.Errorf("seed AI Interaction attention: %w", err)
	}
	return removed.RowsAffected() > 0 || tag.RowsAffected() > 0, nil
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
