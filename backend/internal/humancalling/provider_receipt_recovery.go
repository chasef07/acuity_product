package humancalling

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

type ProviderReceiptCandidateQuery struct {
	Identity   access.Identity
	PracticeID string
	EventType  string
	ErrorCode  string
}

type ProviderReceiptCandidate struct {
	PracticeID          string `json:"practiceId"`
	CallID              string `json:"callId"`
	ReceiptReference    string `json:"receiptReference"`
	EventType           string `json:"eventType"`
	ErrorCode           string `json:"errorCode"`
	Attempts            int64  `json:"attempts"`
	AgeSeconds          int64  `json:"ageSeconds"`
	RemainingGroupCount int64  `json:"remainingGroupCount"`
}

type ProviderReceiptRecoveryStatusQuery struct {
	Identity         access.Identity
	PracticeID       string
	ReceiptReference string
}

type ResolveUnreplayableProviderReceiptCommand struct {
	Identity         access.Identity
	PracticeID       string
	ReceiptReference string
}

type ProviderReceiptResolution struct {
	ReceiptReference string       `json:"receiptReference"`
	State            ReceiptState `json:"state"`
}

type ProviderReceiptStateCount struct {
	State string `json:"state"`
	Count int64  `json:"count"`
}

type ProviderReceiptRecoveryStatus struct {
	PracticeID              string                      `json:"practiceId"`
	CallID                  string                      `json:"callId"`
	ReceiptReference        string                      `json:"receiptReference"`
	EventType               string                      `json:"eventType"`
	ErrorCode               string                      `json:"errorCode"`
	State                   ReceiptState                `json:"state"`
	Attempts                int64                       `json:"attempts"`
	AgeSeconds              int64                       `json:"ageSeconds"`
	DuplicateCount          int64                       `json:"duplicateCount"`
	CallState               CallState                   `json:"callState"`
	CallVersion             int64                       `json:"callVersion"`
	CallLegStates           []ProviderReceiptStateCount `json:"callLegStates"`
	CommandStates           []ProviderReceiptStateCount `json:"commandStates"`
	ActiveReceiptCount      int64                       `json:"activeReceiptCount"`
	QuarantinedReceiptCount int64                       `json:"quarantinedReceiptCount"`
	RequeueAuditCount       int64                       `json:"requeueAuditCount"`
	ResolutionAuditCount    int64                       `json:"resolutionAuditCount"`
}

var providerReceiptRecoveryEventTypes = map[string]struct{}{
	string(FactCallInitiated):   {},
	string(FactCallAnswered):    {},
	string(FactCallBridged):     {},
	string(FactCallHangup):      {},
	string(FactPlaybackStarted): {},
	string(FactPlaybackEnded):   {},
	string(FactSpeakStarted):    {},
	string(FactSpeakEnded):      {},
	string(FactRecordingSaved):  {},
	string(FactRecordingError):  {},
}

func validProviderReceiptRecoveryGroup(eventType string, errorCode string) bool {
	if _, ok := providerReceiptRecoveryEventTypes[eventType]; !ok {
		return false
	}
	for _, bounded := range providerReceiptAuditErrorCodes {
		if errorCode == bounded {
			return true
		}
	}
	return false
}

func boundedProviderReceiptRecoveryErrorCode(value string) string {
	if value == "" || value == "MANUALLY_REQUEUED" {
		return value
	}
	for _, bounded := range providerReceiptAuditErrorCodes {
		if value == bounded {
			return value
		}
	}
	return "UNCLASSIFIED"
}

// SelectProviderReceiptCandidate bridges one audited, attached quarantine group
// to one safe recovery reference without exposing provider or receipt identity.
func (m *Module) SelectProviderReceiptCandidate(
	ctx context.Context,
	query ProviderReceiptCandidateQuery,
) (ProviderReceiptCandidate, error) {
	query.PracticeID = strings.TrimSpace(query.PracticeID)
	query.EventType = strings.TrimSpace(query.EventType)
	query.ErrorCode = strings.TrimSpace(query.ErrorCode)
	if m.database == nil || m.access == nil || query.PracticeID == "" ||
		!validProviderReceiptRecoveryGroup(query.EventType, query.ErrorCode) {
		return ProviderReceiptCandidate{}, ErrInvalidInput
	}
	discovery, err := m.access.DiscoverActor(ctx, query.Identity)
	if err != nil || !discovery.PlatformOperator {
		return ProviderReceiptCandidate{}, ErrDenied
	}

	checkedAt := m.now().UTC().Truncate(time.Microsecond)
	var candidate ProviderReceiptCandidate
	var eventID string
	err = m.database.QueryRow(ctx, `
		WITH candidates AS (
			SELECT
				receipt.event_id,
				receipt.call_id::text AS call_id,
				receipt.event_type,
				receipt.projection_error_code,
				receipt.projection_attempts,
				receipt.received_at,
				count(*) OVER () AS group_count
			FROM human_calling_provider_receipts receipt
			JOIN human_calling_calls call ON call.id = receipt.call_id
			WHERE call.practice_id::text = $1
				AND receipt.state = 'QUARANTINED'
				AND receipt.event_type = $2
				AND receipt.projection_error_code = $3
		)
		SELECT
			event_id,
			call_id,
			event_type,
			projection_error_code,
			projection_attempts,
			GREATEST(0, EXTRACT(EPOCH FROM ($4 - received_at))::bigint),
			group_count - 1
		FROM candidates
		ORDER BY received_at, event_id
		LIMIT 1
	`, query.PracticeID, query.EventType, query.ErrorCode, checkedAt).Scan(
		&eventID,
		&candidate.CallID,
		&candidate.EventType,
		&candidate.ErrorCode,
		&candidate.Attempts,
		&candidate.AgeSeconds,
		&candidate.RemainingGroupCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderReceiptCandidate{}, fmt.Errorf(
			"%w: attached provider receipt quarantine group is empty",
			ErrConflict,
		)
	}
	if err != nil {
		return ProviderReceiptCandidate{}, fmt.Errorf(
			"select provider receipt recovery candidate: %w",
			err,
		)
	}
	if candidate.Attempts <= 0 {
		return ProviderReceiptCandidate{}, fmt.Errorf(
			"%w: quarantined provider receipt has no projection attempt evidence",
			ErrConflict,
		)
	}
	candidate.PracticeID = query.PracticeID
	candidate.ReceiptReference = m.receiptRecoveryReference(eventID)
	return candidate, nil
}

// ReadProviderReceiptRecoveryStatus follows one opaque attached receipt across
// state changes and returns only bounded receipt and aggregate Call evidence.
func (m *Module) ReadProviderReceiptRecoveryStatus(
	ctx context.Context,
	query ProviderReceiptRecoveryStatusQuery,
) (ProviderReceiptRecoveryStatus, error) {
	query.PracticeID = strings.TrimSpace(query.PracticeID)
	query.ReceiptReference = strings.TrimSpace(query.ReceiptReference)
	if m.database == nil || m.access == nil || query.PracticeID == "" ||
		query.ReceiptReference == "" {
		return ProviderReceiptRecoveryStatus{}, ErrInvalidInput
	}
	discovery, err := m.access.DiscoverActor(ctx, query.Identity)
	if err != nil || !discovery.PlatformOperator {
		return ProviderReceiptRecoveryStatus{}, ErrDenied
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return ProviderReceiptRecoveryStatus{}, fmt.Errorf(
			"begin provider receipt recovery status: %w", err,
		)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	eventID, err := m.resolveAttachedReceiptReference(
		ctx, tx, query.PracticeID, query.ReceiptReference,
	)
	if err != nil {
		return ProviderReceiptRecoveryStatus{}, err
	}

	checkedAt := m.now().UTC().Truncate(time.Microsecond)
	status := ProviderReceiptRecoveryStatus{
		PracticeID: query.PracticeID, ReceiptReference: query.ReceiptReference,
		CallLegStates: []ProviderReceiptStateCount{},
		CommandStates: []ProviderReceiptStateCount{},
	}
	var storedErrorCode string
	err = tx.QueryRow(ctx, `
		SELECT
			receipt.call_id::text,
			receipt.event_type,
			COALESCE(receipt.projection_error_code, ''),
			receipt.state,
			receipt.projection_attempts,
			GREATEST(0, EXTRACT(EPOCH FROM ($3 - receipt.received_at))::bigint),
			receipt.duplicate_count,
			call.version,
			count(*) FILTER (
				WHERE practice_receipt.state IN ('PENDING', 'PROCESSING')
			) AS active_receipts,
			count(*) FILTER (
				WHERE practice_receipt.state = 'QUARANTINED'
			) AS quarantined_receipts
		FROM human_calling_provider_receipts receipt
		JOIN human_calling_calls call ON call.id = receipt.call_id
		LEFT JOIN human_calling_provider_receipts practice_receipt
			ON practice_receipt.call_id IN (
				SELECT practice_call.id
				FROM human_calling_calls practice_call
				WHERE practice_call.practice_id::text = $2
			)
		WHERE receipt.event_id = $1 AND call.practice_id::text = $2
		GROUP BY receipt.event_id, receipt.call_id, receipt.event_type,
			receipt.projection_error_code, receipt.state,
			receipt.projection_attempts, receipt.received_at,
			receipt.duplicate_count, call.version
	`, eventID, query.PracticeID, checkedAt).Scan(
		&status.CallID,
		&status.EventType,
		&storedErrorCode,
		&status.State,
		&status.Attempts,
		&status.AgeSeconds,
		&status.DuplicateCount,
		&status.CallVersion,
		&status.ActiveReceiptCount,
		&status.QuarantinedReceiptCount,
	)
	if err != nil {
		return ProviderReceiptRecoveryStatus{}, fmt.Errorf(
			"read provider receipt recovery status: %w", err,
		)
	}
	projection, err := m.loadCallProjection(ctx, tx, status.CallID)
	if err != nil {
		return ProviderReceiptRecoveryStatus{}, fmt.Errorf(
			"read provider receipt recovery Call state: %w", err,
		)
	}
	status.CallState = projection.call.State
	status.ErrorCode = boundedProviderReceiptRecoveryErrorCode(storedErrorCode)
	if err := tx.QueryRow(ctx, `
		SELECT
			count(*) FILTER (
				WHERE action = 'provider_receipt.requeued'
			),
			count(*) FILTER (
				WHERE action = 'provider_receipt.resolved_unreplayable'
			)
		FROM access_audit_events
		WHERE practice_id::text = $1
			AND actor_type = 'PLATFORM_OPERATOR'
			AND details->>'resourceType' = 'provider_receipt'
			AND details->>'resourceId' = $2
	`, query.PracticeID, eventID).Scan(
		&status.RequeueAuditCount,
		&status.ResolutionAuditCount,
	); err != nil {
		return ProviderReceiptRecoveryStatus{}, fmt.Errorf(
			"read provider receipt recovery audits: %w", err,
		)
	}
	status.CallLegStates, err = readRecoveryStateCounts(ctx, tx, `
		SELECT state, count(*)
		FROM human_calling_call_legs
		WHERE call_id = $1
		GROUP BY state
		ORDER BY state
	`, status.CallID)
	if err != nil {
		return ProviderReceiptRecoveryStatus{}, fmt.Errorf(
			"read provider receipt recovery CallLeg states: %w", err,
		)
	}
	status.CommandStates, err = readRecoveryStateCounts(ctx, tx, `
		SELECT state, count(*)
		FROM human_calling_provider_commands
		WHERE call_id = $1
		GROUP BY state
		ORDER BY state
	`, status.CallID)
	if err != nil {
		return ProviderReceiptRecoveryStatus{}, fmt.Errorf(
			"read provider receipt recovery command states: %w", err,
		)
	}
	if err := tx.Commit(ctx); err != nil {
		return ProviderReceiptRecoveryStatus{}, fmt.Errorf(
			"commit provider receipt recovery status: %w", err,
		)
	}
	return status, nil
}

func (m *Module) resolveAttachedReceiptReference(
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
	`, practiceID)
	if err != nil {
		return "", fmt.Errorf("list attached provider receipts: %w", err)
	}
	defer rows.Close()
	matched := ""
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			return "", fmt.Errorf("scan attached provider receipt: %w", err)
		}
		if m.receiptRecoveryReference(eventID) != reference {
			continue
		}
		if matched != "" {
			return "", fmt.Errorf("%w: provider receipt recovery reference is ambiguous", ErrConflict)
		}
		matched = eventID
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate attached provider receipts: %w", err)
	}
	if matched == "" {
		return "", fmt.Errorf("%w: provider receipt recovery reference is unavailable", ErrConflict)
	}
	return matched, nil
}

func readRecoveryStateCounts(
	ctx context.Context,
	tx pgx.Tx,
	query string,
	callID string,
) ([]ProviderReceiptStateCount, error) {
	rows, err := tx.Query(ctx, query, callID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := []ProviderReceiptStateCount{}
	for rows.Next() {
		var count ProviderReceiptStateCount
		if err := rows.Scan(&count.State, &count.Count); err != nil {
			return nil, err
		}
		counts = append(counts, count)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return counts, nil
}

// ResolveUnreplayableProviderReceipt terminally classifies exactly one
// attached quarantine while preserving its stored receipt and projection
// failure evidence. The state change and operator audit commit atomically.
func (m *Module) ResolveUnreplayableProviderReceipt(
	ctx context.Context,
	command ResolveUnreplayableProviderReceiptCommand,
) (ProviderReceiptResolution, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.ReceiptReference = strings.TrimSpace(command.ReceiptReference)
	if m.database == nil || m.access == nil || command.PracticeID == "" ||
		command.ReceiptReference == "" {
		return ProviderReceiptResolution{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProviderReceiptResolution{}, fmt.Errorf(
			"begin unreplayable provider receipt resolution: %w", err,
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
			return ProviderReceiptResolution{}, ErrDenied
		}
		return ProviderReceiptResolution{}, fmt.Errorf(
			"load provider receipt resolution authorization location: %w", err,
		)
	}
	authorization, err := m.access.LockMutationAuthorization(
		ctx, tx, command.Identity, command.PracticeID, locationID,
	)
	if err != nil || !authorization.PlatformOperator {
		return ProviderReceiptResolution{}, ErrDenied
	}
	eventID, err := m.resolveQuarantinedReceiptReference(
		ctx, tx, command.PracticeID, command.ReceiptReference,
	)
	if err != nil {
		return ProviderReceiptResolution{}, err
	}
	var state ReceiptState
	var projectionAttempts int64
	err = tx.QueryRow(ctx, `
		SELECT receipt.state, receipt.projection_attempts
		FROM human_calling_provider_receipts receipt
		JOIN human_calling_calls call ON call.id = receipt.call_id
		WHERE receipt.event_id = $1 AND call.practice_id::text = $2
		FOR UPDATE OF receipt
	`, eventID, command.PracticeID).Scan(&state, &projectionAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderReceiptResolution{}, fmt.Errorf(
			"%w: provider receipt is outside the authorized Practice", ErrConflict,
		)
	}
	if err != nil {
		return ProviderReceiptResolution{}, fmt.Errorf(
			"lock unreplayable provider receipt: %w", err,
		)
	}
	if state != ReceiptQuarantined {
		return ProviderReceiptResolution{}, fmt.Errorf(
			"%w: provider receipt is not quarantined", ErrConflict,
		)
	}
	resolvedAt := m.now()
	result := ProviderReceiptResolution{
		ReceiptReference: command.ReceiptReference,
	}
	err = tx.QueryRow(ctx, `
		UPDATE human_calling_provider_receipts
		SET
			state = 'FAILED',
			processing_started_at = NULL,
			next_attempt_at = $2,
			quarantined_at = NULL
		WHERE event_id = $1 AND state = 'QUARANTINED'
		RETURNING state
	`, eventID, resolvedAt).Scan(&result.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderReceiptResolution{}, fmt.Errorf(
			"%w: provider receipt is not quarantined", ErrConflict,
		)
	}
	if err != nil {
		return ProviderReceiptResolution{}, fmt.Errorf(
			"resolve unreplayable provider receipt: %w", err,
		)
	}
	if err := m.access.AuditOperatorMutation(
		ctx,
		tx,
		authorization,
		access.OperatorMutationAudit{
			Action:          "provider_receipt.resolved_unreplayable",
			ResourceType:    "provider_receipt",
			ResourceID:      eventID,
			ResourceVersion: projectionAttempts,
			OccurredAt:      resolvedAt,
		},
	); err != nil {
		return ProviderReceiptResolution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProviderReceiptResolution{}, fmt.Errorf(
			"commit unreplayable provider receipt resolution: %w", err,
		)
	}
	return result, nil
}
