package humancalling

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/telnyxsignature"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ReceiptState string

const (
	ReceiptPending     ReceiptState = "PENDING"
	ReceiptProcessing  ReceiptState = "PROCESSING"
	ReceiptApplied     ReceiptState = "APPLIED"
	ReceiptUnknown     ReceiptState = "UNKNOWN"
	ReceiptFailed      ReceiptState = "FAILED"
	ReceiptQuarantined ReceiptState = "QUARANTINED"

	fastReceiptProjectionAttempts = 10
	maxFastReceiptRetryDelay      = time.Minute
	slowRelatedFactRetryDelay     = 15 * time.Minute
)

type WebhookReceipt struct {
	EventID        string
	EventType      string
	State          ReceiptState
	Duplicate      bool
	DuplicateCount int
}

type RequeueQuarantinedReceiptCommand struct {
	Identity         access.Identity
	PracticeID       string
	EventID          string
	ReceiptReference string
}

type telnyxEnvelope struct {
	Data struct {
		RecordType string          `json:"record_type"`
		EventType  string          `json:"event_type"`
		ID         string          `json:"id"`
		OccurredAt time.Time       `json:"occurred_at"`
		Payload    json.RawMessage `json:"payload"`
	} `json:"data"`
}

type telnyxVoicePayload struct {
	CallControlID      string         `json:"call_control_id"`
	CallLegID          string         `json:"call_leg_id"`
	CallSessionID      string         `json:"call_session_id"`
	ConnectionID       string         `json:"connection_id"`
	ClientState        string         `json:"client_state"`
	From               string         `json:"from"`
	To                 string         `json:"to"`
	HangupCause        string         `json:"hangup_cause"`
	HangupSource       string         `json:"hangup_source"`
	SIPHangupCause     string         `json:"sip_hangup_cause"`
	Status             string         `json:"status"`
	CallQualityStats   map[string]any `json:"call_quality_stats"`
	RecordingID        string         `json:"recording_id"`
	RecordingStartedAt time.Time      `json:"recording_started_at"`
	RecordingEndedAt   time.Time      `json:"recording_ended_at"`
}

func (m *Module) ReceiveWebhook(
	ctx context.Context,
	raw []byte,
	timestampHeader string,
	signatureHeader string,
) (WebhookReceipt, error) {
	verifier, valid := telnyxsignature.New(
		m.config.WebhookPublicKeys, m.config.WebhookTolerance, m.now,
	)
	if len(raw) == 0 || len(raw) > 256*1024 || !valid {
		return WebhookReceipt{}, ErrInvalidWebhook
	}
	if !verifier.Verify(raw, timestampHeader, signatureHeader) {
		return WebhookReceipt{}, ErrInvalidWebhook
	}
	timestamp, _ := strconv.ParseInt(timestampHeader, 10, 64)
	envelope, err := decodeTelnyxEnvelope(raw)
	if err != nil {
		return WebhookReceipt{}, ErrInvalidWebhook
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return WebhookReceipt{}, fmt.Errorf("begin provider receipt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result := WebhookReceipt{
		EventID:   envelope.Data.ID,
		EventType: envelope.Data.EventType,
		State:     ReceiptPending,
	}
	var inserted string
	err = tx.QueryRow(ctx, `
		INSERT INTO human_calling_provider_receipts (
			event_id, event_type, occurred_at, received_at,
			signature_timestamp, raw_body, next_attempt_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $4)
		ON CONFLICT (event_id) DO NOTHING
		RETURNING event_id
	`,
		result.EventID,
		result.EventType,
		envelope.Data.OccurredAt,
		m.now(),
		timestamp,
		raw,
	).Scan(&inserted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return WebhookReceipt{}, fmt.Errorf("commit provider receipt: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var existingRaw []byte
		if err := tx.QueryRow(ctx, `
			SELECT event_type, raw_body, state, duplicate_count
			FROM human_calling_provider_receipts
			WHERE event_id = $1
			FOR UPDATE
		`, result.EventID).Scan(
			&result.EventType,
			&existingRaw,
			&result.State,
			&result.DuplicateCount,
		); err != nil {
			return WebhookReceipt{}, fmt.Errorf("load duplicate provider receipt: %w", err)
		}
		if !bytes.Equal(existingRaw, raw) ||
			result.EventType != envelope.Data.EventType {
			return WebhookReceipt{}, ErrInvalidWebhook
		}
		result.Duplicate = true
		result.DuplicateCount++
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_receipts
			SET duplicate_count = duplicate_count + 1
			WHERE event_id = $1
		`, result.EventID); err != nil {
			return WebhookReceipt{}, fmt.Errorf("count duplicate provider receipt: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return WebhookReceipt{}, fmt.Errorf("commit provider receipt transaction: %w", err)
	}
	return result, nil
}

// RequeueQuarantinedReceipt schedules persisted, previously verified evidence
// for replay by a Platform Operator under their own identity.
func (m *Module) RequeueQuarantinedReceipt(
	ctx context.Context,
	command RequeueQuarantinedReceiptCommand,
) (WebhookReceipt, error) {
	eventID := strings.TrimSpace(command.EventID)
	receiptReference := strings.TrimSpace(command.ReceiptReference)
	if m.access == nil ||
		strings.TrimSpace(command.PracticeID) == "" ||
		(eventID == "") == (receiptReference == "") {
		return WebhookReceipt{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return WebhookReceipt{}, fmt.Errorf(
			"begin quarantined provider receipt requeue: %w",
			err,
		)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var locationID string
	if err := tx.QueryRow(ctx, `
		SELECT id::text
		FROM access_locations
		WHERE practice_id::text = $1
		ORDER BY id
		LIMIT 1
	`, command.PracticeID).Scan(&locationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WebhookReceipt{}, ErrDenied
		}
		return WebhookReceipt{}, fmt.Errorf(
			"load provider receipt Practice authorization location: %w",
			err,
		)
	}
	authorization, err := m.access.LockMutationAuthorization(
		ctx,
		tx,
		command.Identity,
		command.PracticeID,
		locationID,
	)
	if err != nil {
		return WebhookReceipt{}, ErrDenied
	}
	if !authorization.PlatformOperator {
		return WebhookReceipt{}, ErrDenied
	}
	if receiptReference != "" {
		eventID, err = m.resolveQuarantinedReceiptReference(
			ctx,
			tx,
			command.PracticeID,
			receiptReference,
		)
		if err != nil {
			return WebhookReceipt{}, err
		}
	}

	var result WebhookReceipt
	var state ReceiptState
	var projectionAttempts int64
	err = tx.QueryRow(ctx, `
		SELECT
			receipt.event_id,
			receipt.event_type,
			receipt.state,
			receipt.duplicate_count,
			receipt.projection_attempts
		FROM human_calling_provider_receipts receipt
		JOIN human_calling_calls call ON call.id = receipt.call_id
		WHERE receipt.event_id = $1
			AND call.practice_id::text = $2
		FOR UPDATE OF receipt
	`, eventID, command.PracticeID).Scan(
		&result.EventID,
		&result.EventType,
		&state,
		&result.DuplicateCount,
		&projectionAttempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookReceipt{}, fmt.Errorf(
			"%w: provider receipt is outside the authorized Practice",
			ErrConflict,
		)
	}
	if err != nil {
		return WebhookReceipt{}, fmt.Errorf(
			"lock quarantined provider receipt: %w",
			err,
		)
	}
	if state != ReceiptQuarantined {
		return WebhookReceipt{}, fmt.Errorf(
			"%w: provider receipt is not quarantined",
			ErrConflict,
		)
	}
	requeuedAt := m.now()
	err = tx.QueryRow(ctx, `
		UPDATE human_calling_provider_receipts
		SET
			state = 'PENDING',
			projection_attempts = 0,
			projection_error_code = 'MANUALLY_REQUEUED',
			processing_started_at = NULL,
			last_attempt_at = NULL,
			next_attempt_at = $2,
			projected_at = NULL,
			quarantined_at = NULL
		WHERE event_id = $1 AND state = 'QUARANTINED'
		RETURNING state
	`, eventID, requeuedAt).Scan(&result.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookReceipt{}, fmt.Errorf(
			"%w: provider receipt is not quarantined",
			ErrConflict,
		)
	}
	if err != nil {
		return WebhookReceipt{}, fmt.Errorf(
			"requeue quarantined provider receipt: %w",
			err,
		)
	}
	if err := m.access.AuditOperatorMutation(
		ctx,
		tx,
		authorization,
		access.OperatorMutationAudit{
			Action:          "provider_receipt.requeued",
			ResourceType:    "provider_receipt",
			ResourceID:      eventID,
			ResourceVersion: projectionAttempts,
			OccurredAt:      requeuedAt,
		},
	); err != nil {
		return WebhookReceipt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WebhookReceipt{}, fmt.Errorf(
			"commit quarantined provider receipt requeue: %w",
			err,
		)
	}
	return result, nil
}

func (m *Module) resolveQuarantinedReceiptReference(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	reference string,
) (string, error) {
	rows, err := tx.Query(ctx, `
		SELECT receipt.event_id
		FROM human_calling_provider_receipts receipt
		JOIN human_calling_calls call ON call.id = receipt.call_id
		WHERE call.practice_id::text = $1
			AND receipt.state = 'QUARANTINED'
	`, practiceID)
	if err != nil {
		return "", fmt.Errorf("list quarantined provider receipts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			return "", fmt.Errorf("scan quarantined provider receipt: %w", err)
		}
		if m.receiptRecoveryReference(eventID) == reference {
			return eventID, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate quarantined provider receipts: %w", err)
	}
	return "", fmt.Errorf("%w: provider receipt recovery reference is unavailable", ErrConflict)
}

func (m *Module) ProcessNextReceipt(ctx context.Context) (bool, error) {
	now := m.now()
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin provider receipt claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var eventID string
	var raw []byte
	var receivedAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT event_id, raw_body, received_at
		FROM human_calling_provider_receipts
		WHERE next_attempt_at <= $1
			AND (
				state = 'PENDING'
				OR (
					state = 'PROCESSING'
					AND processing_started_at <= $1 - interval '30 seconds'
				)
			)
		ORDER BY next_attempt_at, received_at, event_id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, now).Scan(&eventID, &raw, &receivedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return false, fmt.Errorf("commit empty provider receipt claim: %w", err)
			}
			return false, nil
		}
		return false, fmt.Errorf("claim provider receipt: %w", err)
	}
	var projectionAttempts int
	if err := tx.QueryRow(ctx, `
		UPDATE human_calling_provider_receipts
		SET
			state = 'PROCESSING',
			projection_attempts = projection_attempts + 1,
			processing_started_at = $2,
			last_attempt_at = $2
		WHERE event_id = $1
		RETURNING projection_attempts
	`, eventID, now).Scan(&projectionAttempts); err != nil {
		return false, fmt.Errorf("mark provider receipt processing: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit provider receipt claim: %w", err)
	}

	state, errorCode := m.replayProviderReceipt(ctx, eventID, raw)
	completedAt := m.now()
	nextAttemptAt := completedAt
	if state == ReceiptPending {
		if projectionAttempts >= fastReceiptProjectionAttempts {
			switch errorCode {
			case "WAITING_FOR_RELATED_FACT":
				errorCode = "WAITING_FOR_RELATED_FACT_SLOW_RETRY"
				nextAttemptAt = completedAt.Add(slowRelatedFactRetryDelay)
			default:
				state = ReceiptQuarantined
				errorCode = "PROJECTION_RETRY_EXHAUSTED"
			}
		} else {
			nextAttemptAt = completedAt.Add(receiptRetryDelay(projectionAttempts))
		}
	}
	tag, err := m.pool.Exec(ctx, `
		UPDATE human_calling_provider_receipts
		SET
			state = $2,
			projection_error_code = NULLIF($3, ''),
			processing_started_at = NULL,
			next_attempt_at = $4,
			projected_at = CASE
				WHEN $2 IN ('APPLIED', 'UNKNOWN', 'FAILED', 'QUARANTINED') THEN $5
				ELSE projected_at
			END,
			quarantined_at = CASE
				WHEN $2 = 'QUARANTINED' THEN $5
				ELSE NULL
			END
		WHERE event_id = $1
			AND state = 'PROCESSING'
			AND projection_attempts = $6
	`, eventID, state, errorCode, nextAttemptAt, completedAt, projectionAttempts)
	if err != nil {
		return true, fmt.Errorf("record provider receipt projection: %w", err)
	}
	if tag.RowsAffected() == 1 {
		m.recordReceiptProcessed(state, receivedAt, now, completedAt)
	}
	return true, nil
}

func (m *Module) replayProviderReceipt(
	ctx context.Context,
	eventID string,
	raw []byte,
) (ReceiptState, string) {
	fact, known, err := normalizeTelnyxFact(raw)
	if err != nil {
		return ReceiptFailed, "INVALID_PROVIDER_EVENT"
	}
	if !known {
		return ReceiptUnknown, ""
	}
	err = m.attachReceiptCall(ctx, eventID, fact)
	if err == nil {
		err = m.ApplyProviderFact(ctx, fact)
		if err == nil {
			err = m.attachReceiptCall(ctx, eventID, fact)
		}
	}
	if errors.Is(err, ErrInvalidHandoff) {
		if err := m.rememberRejectedProviderLeg(ctx, fact); err != nil {
			return ReceiptPending, "PROJECTION_RETRY"
		}
		return ReceiptFailed, "HANDOFF_REJECTED"
	}
	if err != nil && rejectedHandoffLifecycle(fact.Type) {
		rejected, lookupErr := m.providerLegWasRejected(ctx, fact)
		if lookupErr != nil {
			return ReceiptPending, "PROJECTION_RETRY"
		}
		if rejected {
			return ReceiptFailed, "RELATED_HANDOFF_REJECTED"
		}
	}
	switch {
	case err == nil:
		return ReceiptApplied, ""
	case errors.Is(err, ErrConflict):
		return ReceiptPending, "WAITING_FOR_RELATED_FACT"
	default:
		return ReceiptPending, "PROJECTION_RETRY"
	}
}

func (m *Module) rememberRejectedProviderLeg(
	ctx context.Context,
	fact ProviderFact,
) error {
	if fact.CallControlID == "" || fact.CallLegID == "" || fact.CallSessionID == "" {
		return nil
	}
	_, err := m.pool.Exec(ctx, `
		INSERT INTO human_calling_rejected_provider_legs (
			call_control_id,
			call_leg_id,
			call_session_id,
			initiated_event_id,
			rejected_at
		)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT DO NOTHING
	`, fact.CallControlID, fact.CallLegID, fact.CallSessionID, fact.EventID, m.now())
	if err != nil {
		return fmt.Errorf("remember rejected provider leg: %w", err)
	}
	return nil
}

func (m *Module) providerLegWasRejected(
	ctx context.Context,
	fact ProviderFact,
) (bool, error) {
	if fact.CallControlID == "" || fact.CallLegID == "" || fact.CallSessionID == "" {
		return false, nil
	}
	var rejected bool
	if err := m.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM human_calling_rejected_provider_legs
			WHERE call_control_id = $1
				AND call_leg_id = $2
				AND call_session_id = $3
		)
	`, fact.CallControlID, fact.CallLegID, fact.CallSessionID).Scan(&rejected); err != nil {
		return false, fmt.Errorf("find rejected provider leg: %w", err)
	}
	return rejected, nil
}

func rejectedHandoffLifecycle(factType FactType) bool {
	switch factType {
	case FactCallAnswered, FactCallBridged, FactCallHangup:
		return true
	default:
		return false
	}
}

func receiptRetryDelay(attempt int) time.Duration {
	delay := time.Second
	for current := 1; current < attempt && delay < maxFastReceiptRetryDelay; current++ {
		delay *= 2
	}
	if delay > maxFastReceiptRetryDelay {
		return maxFastReceiptRetryDelay
	}
	return delay
}

func (m *Module) attachReceiptCall(
	ctx context.Context,
	eventID string,
	fact ProviderFact,
) error {
	if clientState, ok := parseCallLegClientState(fact.ClientState); ok {
		tag, err := m.pool.Exec(ctx, `
			UPDATE human_calling_provider_receipts receipt
			SET call_id = call.id
			FROM human_calling_calls call
			WHERE receipt.event_id = $1
				AND receipt.call_id IS NULL
				AND call.id = $2
		`, eventID, clientState.CallID)
		if err != nil {
			return fmt.Errorf("attach opaque provider receipt Call: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
	}
	if fact.CallControlID == "" || fact.CallLegID == "" {
		return nil
	}
	if _, err := m.pool.Exec(ctx, `
		UPDATE human_calling_provider_receipts receipt
		SET call_id = leg.call_id
		FROM human_calling_call_legs leg
		WHERE receipt.event_id = $1
			AND receipt.call_id IS NULL
			AND leg.provider_call_control_id = $2
			AND leg.provider_call_leg_id = $3
	`, eventID, fact.CallControlID, fact.CallLegID); err != nil {
		return fmt.Errorf("attach correlated provider receipt Call: %w", err)
	}
	return nil
}

func decodeTelnyxEnvelope(raw []byte) (telnyxEnvelope, error) {
	var envelope telnyxEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return telnyxEnvelope{}, err
	}
	if envelope.Data.RecordType != "event" ||
		envelope.Data.ID == "" ||
		len(envelope.Data.ID) > 200 ||
		envelope.Data.EventType == "" ||
		len(envelope.Data.EventType) > 200 ||
		envelope.Data.OccurredAt.IsZero() ||
		len(envelope.Data.Payload) == 0 {
		return telnyxEnvelope{}, ErrInvalidWebhook
	}
	return envelope, nil
}

func normalizeTelnyxFact(raw []byte) (ProviderFact, bool, error) {
	envelope, err := decodeTelnyxEnvelope(raw)
	if err != nil {
		return ProviderFact{}, false, err
	}
	fact := ProviderFact{
		EventID:    envelope.Data.ID,
		Type:       FactType(envelope.Data.EventType),
		OccurredAt: envelope.Data.OccurredAt,
	}
	switch fact.Type {
	case FactCallInitiated,
		FactCallAnswered,
		FactCallBridged,
		FactCallHangup,
		FactPlaybackStarted,
		FactPlaybackEnded,
		FactSpeakStarted,
		FactSpeakEnded,
		FactRecordingSaved,
		FactRecordingError:
	default:
		return fact, false, nil
	}
	var payload telnyxVoicePayload
	if err := json.Unmarshal(envelope.Data.Payload, &payload); err != nil {
		return ProviderFact{}, false, err
	}
	fact.CallControlID = payload.CallControlID
	fact.CallLegID = payload.CallLegID
	fact.CallSessionID = payload.CallSessionID
	fact.ConnectionID = payload.ConnectionID
	fact.ClientState = payload.ClientState
	fact.From = payload.From
	fact.To = payload.To
	fact.HangupCause = payload.HangupCause
	fact.TerminationSource = payload.HangupSource
	fact.SIPCause = payload.SIPHangupCause
	fact.PlaybackStatus = payload.Status
	fact.CallQualityStats = payload.CallQualityStats
	fact.RecordingID = payload.RecordingID
	fact.RecordingStartedAt = payload.RecordingStartedAt
	fact.RecordingEndedAt = payload.RecordingEndedAt
	if fact.Type == FactSpeakEnded &&
		fact.PlaybackStatus != "completed" &&
		fact.PlaybackStatus != "call_hangup" &&
		fact.PlaybackStatus != "cancelled_amd" {
		return ProviderFact{}, false, ErrInvalidWebhook
	}
	if (fact.Type != FactRecordingSaved && fact.Type != FactRecordingError &&
		fact.CallControlID == "") ||
		fact.CallLegID == "" ||
		fact.CallSessionID == "" {
		return ProviderFact{}, false, ErrInvalidWebhook
	}
	if fact.ClientState != "" {
		state, ok := parseCallLegClientState(fact.ClientState)
		if !ok || !validUUID(state.CallID) || !validUUID(state.CallLegID) ||
			(state.Role != "CALLER" && state.Role != "STAFF" &&
				state.Role != "DESTINATION") {
			return ProviderFact{}, false, ErrInvalidWebhook
		}
	}
	return fact, true, nil
}

func validUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func claimProviderFact(
	ctx context.Context,
	tx pgx.Tx,
	fact ProviderFact,
	appliedAt time.Time,
) (bool, error) {
	var eventID string
	err := tx.QueryRow(ctx, `
		INSERT INTO human_calling_projected_facts (event_id, event_type, applied_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (event_id) DO NOTHING
		RETURNING event_id
	`, fact.EventID, fact.Type, appliedAt).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim provider fact projection: %w", err)
	}
	return true, nil
}
