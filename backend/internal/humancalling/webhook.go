package humancalling

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

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
	CallControlID      string `json:"call_control_id"`
	CallLegID          string `json:"call_leg_id"`
	CallSessionID      string `json:"call_session_id"`
	ClientState        string `json:"client_state"`
	To                 string `json:"to"`
	HangupCause        string `json:"hangup_cause"`
	RecordingID        string `json:"recording_id"`
	RecordingObjectKey string `json:"recording_object_key"`
	RecordingURLs      struct {
		WAV string `json:"wav"`
	} `json:"recording_urls"`
	CustomHeaders []telnyxCustomHeader `json:"custom_headers"`
}

type telnyxCustomHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (m *Module) ReceiveWebhook(
	ctx context.Context,
	raw []byte,
	timestampHeader string,
	signatureHeader string,
) (WebhookReceipt, error) {
	if len(raw) == 0 ||
		len(raw) > 256*1024 ||
		len(m.config.WebhookPublicKey) != ed25519.PublicKeySize {
		return WebhookReceipt{}, ErrInvalidWebhook
	}
	timestamp, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil {
		return WebhookReceipt{}, ErrInvalidWebhook
	}
	sentAt := time.Unix(timestamp, 0)
	age := m.now().Sub(sentAt)
	if age < -m.config.WebhookTolerance || age > m.config.WebhookTolerance {
		return WebhookReceipt{}, ErrInvalidWebhook
	}
	signature, err := base64.StdEncoding.DecodeString(signatureHeader)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return WebhookReceipt{}, ErrInvalidWebhook
	}
	signed := make([]byte, 0, len(timestampHeader)+1+len(raw))
	signed = append(signed, timestampHeader...)
	signed = append(signed, '|')
	signed = append(signed, raw...)
	if !ed25519.Verify(m.config.WebhookPublicKey, signed, signature) {
		return WebhookReceipt{}, ErrInvalidWebhook
	}
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
// for replay. Callers must enforce operator authorization and audit.
func (m *Module) RequeueQuarantinedReceipt(
	ctx context.Context,
	eventID string,
) (WebhookReceipt, error) {
	if strings.TrimSpace(eventID) == "" {
		return WebhookReceipt{}, ErrInvalidInput
	}
	var result WebhookReceipt
	err := m.pool.QueryRow(ctx, `
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
		RETURNING event_id, event_type, state, duplicate_count
	`, eventID, m.now()).Scan(
		&result.EventID,
		&result.EventType,
		&result.State,
		&result.DuplicateCount,
	)
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
	return result, nil
}

func (m *Module) ProcessNextReceipt(ctx context.Context) (bool, error) {
	now := m.now()
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin provider receipt claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var eventID, eventType string
	var raw []byte
	var receivedAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT event_id, event_type, raw_body, received_at
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
	`, now).Scan(&eventID, &eventType, &raw, &receivedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return false, fmt.Errorf("commit empty provider receipt claim: %w", err)
			}
			return false, nil
		}
		return false, fmt.Errorf("claim provider receipt: %w", err)
	}
	if eventType == string(FactCallHangup) &&
		now.Before(receivedAt.Add(2*time.Second)) {
		if _, err := tx.Exec(ctx, `
			UPDATE human_calling_provider_receipts
			SET
				projection_error_code = 'WAITING_FOR_RELATED_FACT',
				next_attempt_at = $2
			WHERE event_id = $1
		`, eventID, receivedAt.Add(2*time.Second)); err != nil {
			return false, fmt.Errorf("defer provider hangup receipt: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit deferred provider hangup receipt: %w", err)
		}
		m.recordReceiptProcessed(ReceiptPending, receivedAt, now, m.now())
		return true, nil
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
	}
	switch {
	case err == nil:
		return ReceiptApplied, ""
	case errors.Is(err, ErrConflict):
		return ReceiptPending, "WAITING_FOR_RELATED_FACT"
	case errors.Is(err, ErrInvalidHandoff):
		return ReceiptFailed, "HANDOFF_REJECTED"
	default:
		return ReceiptPending, "PROJECTION_RETRY"
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
	if clientState, ok := parseOpaqueClientState(fact.ClientState); ok {
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
	if fact.CallSessionID == "" ||
		fact.CallControlID == "" ||
		fact.CallLegID == "" {
		return nil
	}
	if _, err := m.pool.Exec(ctx, `
		UPDATE human_calling_provider_receipts receipt
		SET call_id = matched.call_id
		FROM (
			SELECT call.id AS call_id
			FROM human_calling_calls call
			WHERE call.call_session_id = $2
				AND (
					(
						call.caller_call_control_id = $3
						AND call.caller_call_leg_id = $4
					)
					OR EXISTS (
						SELECT 1
						FROM human_calling_connection_attempts attempt
						WHERE attempt.call_id = call.id
							AND attempt.staff_call_control_id = $3
							AND attempt.staff_call_leg_id = $4
					)
				)
			ORDER BY call.created_at, call.id
			LIMIT 1
		) matched
		WHERE receipt.event_id = $1
			AND receipt.call_id IS NULL
	`, eventID, fact.CallSessionID, fact.CallControlID, fact.CallLegID); err != nil {
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
	fact.ClientState = payload.ClientState
	fact.To = payload.To
	fact.HandoffToken, err = handoffTokenFromHeaders(payload.CustomHeaders)
	if err != nil {
		return ProviderFact{}, false, err
	}
	fact.HangupCause = payload.HangupCause
	fact.RecordingID = payload.RecordingID
	fact.RecordingObjectKey = payload.RecordingObjectKey
	if fact.CallControlID == "" ||
		fact.CallLegID == "" ||
		fact.CallSessionID == "" {
		return ProviderFact{}, false, ErrInvalidWebhook
	}
	if fact.ClientState != "" {
		state, ok := parseOpaqueClientState(fact.ClientState)
		if !ok ||
			state.Version != 1 ||
			(state.Leg != "caller" &&
				state.Leg != "staff" &&
				state.Leg != "recording") ||
			!validUUID(state.CallID) ||
			(state.AttemptID != "" && !validUUID(state.AttemptID)) {
			return ProviderFact{}, false, ErrInvalidWebhook
		}
	}
	if fact.Type == FactRecordingSaved {
		fact.RecordingBucket, fact.RecordingObjectKey = gcsObject(
			payload.RecordingURLs.WAV,
		)
	}
	return fact, true, nil
}

func handoffTokenFromHeaders(headers []telnyxCustomHeader) (string, error) {
	const name = "X-Acuity-Handoff-Token"
	var token string
	for _, header := range headers {
		if !strings.EqualFold(header.Name, name) {
			continue
		}
		if token != "" {
			return "", ErrInvalidWebhook
		}
		token = strings.TrimSpace(header.Value)
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || len(decoded) != sha256.Size {
			return "", ErrInvalidWebhook
		}
	}
	return token, nil
}

func validUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func gcsObject(value string) (string, string) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "gs" || parsed.Host == "" {
		return "", ""
	}
	key := strings.TrimPrefix(parsed.EscapedPath(), "/")
	decoded, err := url.PathUnescape(key)
	if err != nil || decoded == "" {
		return "", ""
	}
	return parsed.Host, decoded
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
