package humancalling

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-telnyx/telnyx-go/v4"
	"github.com/team-telnyx/telnyx-go/v4/option"
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
	maxRelatedFactWait            = 24 * time.Hour
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

func (m *Module) ReceiveWebhook(
	ctx context.Context,
	raw []byte,
	timestampHeader string,
	signatureHeader string,
) (WebhookReceipt, error) {
	event, err := unwrapTelnyxWebhook(
		raw,
		timestampHeader,
		signatureHeader,
		m.config.WebhookPublicKeys,
	)
	if err != nil {
		return WebhookReceipt{}, ErrInvalidWebhook
	}
	timestamp, _ := strconv.ParseInt(timestampHeader, 10, 64)

	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return WebhookReceipt{}, fmt.Errorf("begin provider receipt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result := WebhookReceipt{
		EventID:   event.Data.ID,
		EventType: event.Data.EventType,
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
		event.Data.OccurredAt,
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
			result.EventType != event.Data.EventType {
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

func unwrapTelnyxWebhook(
	raw []byte,
	timestampHeader string,
	signatureHeader string,
	publicKeys [][]byte,
) (*telnyx.UnwrapWebhookEventUnion, error) {
	if len(raw) == 0 || len(raw) > 256*1024 || len(publicKeys) == 0 {
		return nil, ErrInvalidWebhook
	}
	headers := http.Header{}
	headers.Set("telnyx-timestamp", timestampHeader)
	headers.Set("telnyx-signature-ed25519", signatureHeader)
	webhooks := telnyx.NewWebhookService()
	for _, publicKey := range publicKeys {
		if len(publicKey) == 0 {
			continue
		}
		event, err := webhooks.Unwrap(
			raw,
			headers,
			option.WithPublicKey(base64.StdEncoding.EncodeToString(publicKey)),
		)
		if err == nil && validTelnyxEvent(
			event.Data.RecordType,
			event.Data.ID,
			event.Data.EventType,
			event.Data.OccurredAt,
			event.Data.JSON.Payload.Valid(),
		) {
			return event, nil
		}
	}
	return nil, ErrInvalidWebhook
}

func validTelnyxEvent(
	recordType string,
	eventID string,
	eventType string,
	occurredAt time.Time,
	payloadPresent bool,
) bool {
	return recordType == "event" && eventID != "" && len(eventID) <= 200 &&
		eventType != "" && len(eventType) <= 200 && !occurredAt.IsZero() &&
		payloadPresent
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
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
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
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
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
		if errorCode == "WAITING_FOR_RELATED_FACT" &&
			!receivedAt.Add(maxRelatedFactWait).After(completedAt) {
			state = ReceiptQuarantined
			errorCode = "RELATED_FACT_TIMEOUT"
		} else if projectionAttempts >= fastReceiptProjectionAttempts {
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
	tag, err := m.database.Exec(ctx, `
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
		m.recordReceiptProcessed(state, errorCode, receivedAt, now, completedAt)
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
		if err == nil {
			err = m.wakeRelatedReceipts(ctx, eventID, fact)
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
	case errors.Is(err, errRelatedFactPending):
		return ReceiptPending, "WAITING_FOR_RELATED_FACT"
	case errors.Is(err, errTerminalOrObsoleteProviderFact):
		return ReceiptFailed, "TERMINAL_OR_OBSOLETE_PROVIDER_FACT"
	case errors.Is(err, ErrConflict):
		return ReceiptPending, "PROJECTION_RETRY"
	default:
		return ReceiptPending, "PROJECTION_RETRY"
	}
}

// wakeRelatedReceipts brings only exact Call or provider-leg matches forward;
// the existing retry policy still owns every unresolved receipt's 24-hour bound.
func (m *Module) wakeRelatedReceipts(
	ctx context.Context,
	eventID string,
	fact ProviderFact,
) error {
	_, err := m.database.Exec(ctx, `
		WITH current_receipt AS (
			SELECT call_id
			FROM human_calling_provider_receipts
			WHERE event_id = $1 AND call_id IS NOT NULL
		)
		UPDATE human_calling_provider_receipts related
		SET
			call_id = COALESCE(related.call_id, current_receipt.call_id),
			next_attempt_at = LEAST(related.next_attempt_at, $2)
		FROM current_receipt
		WHERE related.state = 'PENDING'
			AND related.projection_error_code IN (
				'WAITING_FOR_RELATED_FACT',
				'WAITING_FOR_RELATED_FACT_SLOW_RETRY'
			)
			AND (
				related.call_id = current_receipt.call_id
				OR (
					related.call_id IS NULL
					AND $3 <> '' AND $4 <> '' AND $5 <> ''
					AND convert_from(related.raw_body, 'UTF8')::jsonb
						#>> '{data,payload,call_control_id}' = $3
					AND convert_from(related.raw_body, 'UTF8')::jsonb
						#>> '{data,payload,call_leg_id}' = $4
					AND convert_from(related.raw_body, 'UTF8')::jsonb
						#>> '{data,payload,call_session_id}' = $5
				)
			)
	`, eventID, m.now(), fact.CallControlID, fact.CallLegID, fact.CallSessionID)
	if err != nil {
		return fmt.Errorf("wake related provider receipts: %w", err)
	}
	return nil
}

func (m *Module) rememberRejectedProviderLeg(
	ctx context.Context,
	fact ProviderFact,
) error {
	if fact.CallControlID == "" || fact.CallLegID == "" || fact.CallSessionID == "" {
		return nil
	}
	_, err := m.database.Exec(ctx, `
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
	if err := m.database.QueryRow(ctx, `
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
	if rejected {
		return true, nil
	}
	return m.restoreRejectedProviderLeg(ctx, fact)
}

func (m *Module) restoreRejectedProviderLeg(
	ctx context.Context,
	fact ProviderFact,
) (bool, error) {
	var rejected bool
	if err := m.database.QueryRow(ctx, `
		WITH historical_rejection AS (
			SELECT
				receipt.event_id,
				COALESCE(
					receipt.projected_at,
					receipt.last_attempt_at,
					receipt.received_at
				) AS rejected_at
			FROM human_calling_provider_receipts receipt
			WHERE receipt.event_type = 'call.initiated'
				AND receipt.state = 'FAILED'
				AND receipt.projection_error_code = 'HANDOFF_REJECTED'
				AND convert_from(receipt.raw_body, 'UTF8')::jsonb
					#>> '{data,payload,call_control_id}' = $1
				AND convert_from(receipt.raw_body, 'UTF8')::jsonb
					#>> '{data,payload,call_leg_id}' = $2
				AND convert_from(receipt.raw_body, 'UTF8')::jsonb
					#>> '{data,payload,call_session_id}' = $3
			ORDER BY receipt.received_at, receipt.event_id
			LIMIT 1
		), remembered AS (
			INSERT INTO human_calling_rejected_provider_legs (
				call_control_id,
				call_leg_id,
				call_session_id,
				initiated_event_id,
				rejected_at
			)
			SELECT $1, $2, $3, event_id, rejected_at
			FROM historical_rejection
			ON CONFLICT DO NOTHING
			RETURNING 1
		)
		SELECT EXISTS (SELECT 1 FROM historical_rejection)
			OR EXISTS (SELECT 1 FROM remembered)
	`, fact.CallControlID, fact.CallLegID, fact.CallSessionID).Scan(&rejected); err != nil {
		return false, fmt.Errorf("restore rejected provider leg: %w", err)
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
		tag, err := m.database.Exec(ctx, `
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
	if _, err := m.database.Exec(ctx, `
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

func normalizeTelnyxFact(raw []byte) (ProviderFact, bool, error) {
	webhooks := telnyx.NewWebhookService()
	event, err := webhooks.UnsafeUnwrap(raw)
	if err != nil {
		return ProviderFact{}, false, err
	}
	if !validTelnyxEvent(
		event.Data.RecordType,
		event.Data.ID,
		event.Data.EventType,
		event.Data.OccurredAt,
		event.Data.JSON.Payload.Valid(),
	) {
		return ProviderFact{}, false, ErrInvalidWebhook
	}
	fact := ProviderFact{
		EventID:    event.Data.ID,
		Type:       FactType(event.Data.EventType),
		OccurredAt: event.Data.OccurredAt,
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
	payload := event.Data.Payload
	fact.CallControlID = payload.CallControlID
	fact.CallLegID = payload.CallLegID
	fact.CallSessionID = payload.CallSessionID
	fact.ConnectionID = payload.ConnectionID
	fact.ClientState = payload.ClientState
	fact.From = payload.From.OfString
	fact.To = payload.To.OfString
	fact.HangupCause = payload.HangupCause
	fact.TerminationSource = payload.HangupSource
	fact.SIPCause = payload.SipHangupCause
	fact.PlaybackStatus = payload.Status
	if payload.JSON.CallQualityStats.Valid() {
		encoded, err := json.Marshal(payload.CallQualityStats)
		if err != nil || json.Unmarshal(encoded, &fact.CallQualityStats) != nil {
			return ProviderFact{}, false, ErrInvalidWebhook
		}
	}
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
