package humancalling

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type ProviderReceiptStateAudit struct {
	State            ReceiptState `json:"state"`
	Receipts         int64        `json:"receipts"`
	OldestAgeSeconds int64        `json:"oldestAgeSeconds"`
	NewestAgeSeconds int64        `json:"newestAgeSeconds"`
}

type ProviderReceiptOutcomeAudit struct {
	EventType        string `json:"eventType"`
	ErrorCode        string `json:"errorCode"`
	AttachedToCall   bool   `json:"attachedToCall"`
	Receipts         int64  `json:"receipts"`
	MinAttempts      int64  `json:"minAttempts"`
	MaxAttempts      int64  `json:"maxAttempts"`
	OldestAgeSeconds int64  `json:"oldestAgeSeconds"`
	NewestAgeSeconds int64  `json:"newestAgeSeconds"`
}

type ProviderReceiptAudit struct {
	CheckedAt  time.Time                     `json:"checkedAt"`
	States     []ProviderReceiptStateAudit   `json:"states"`
	Failures   []ProviderReceiptOutcomeAudit `json:"failures"`
	Quarantine []ProviderReceiptOutcomeAudit `json:"quarantine"`
}

// providerReceiptAuditErrorCodes is the bounded public audit vocabulary.
// Stored values outside it collapse into UNCLASSIFIED before aggregation.
var providerReceiptAuditErrorCodes = []string{
	"HANDOFF_REJECTED",
	"INVALID_PROVIDER_EVENT",
	projectionApplyFactConflict,
	projectionApplyFactRetry,
	projectionAttachCallRetry,
	projectionLookupRejectedLegRetry,
	projectionRecordRejectedLegRetry,
	"PROJECTION_RETRY_EXHAUSTED",
	projectionWakeRelatedRetry,
	"RELATED_FACT_TIMEOUT",
	"RELATED_HANDOFF_REJECTED",
	"TERMINAL_OR_OBSOLETE_PROVIDER_FACT",
}

// AuditProviderReceipts returns aggregate durable queue evidence without
// exposing receipt, Call, provider, phone, or raw webhook identifiers.
func (m *Module) AuditProviderReceipts(ctx context.Context) (ProviderReceiptAudit, error) {
	if m.database == nil {
		return ProviderReceiptAudit{}, ErrInvalidInput
	}
	checkedAt := m.now().UTC().Truncate(time.Microsecond)
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return ProviderReceiptAudit{}, fmt.Errorf("begin provider receipt audit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	audit := ProviderReceiptAudit{
		CheckedAt:  checkedAt,
		States:     []ProviderReceiptStateAudit{},
		Failures:   []ProviderReceiptOutcomeAudit{},
		Quarantine: []ProviderReceiptOutcomeAudit{},
	}
	rows, err := tx.Query(ctx, `
		SELECT
			state,
			count(*),
			GREATEST(0, EXTRACT(EPOCH FROM ($1 - min(received_at)))::bigint),
			GREATEST(0, EXTRACT(EPOCH FROM ($1 - max(received_at)))::bigint)
		FROM human_calling_provider_receipts
		GROUP BY state
		ORDER BY state
	`, checkedAt)
	if err != nil {
		return ProviderReceiptAudit{}, fmt.Errorf("read provider receipt states: %w", err)
	}
	for rows.Next() {
		var state ProviderReceiptStateAudit
		if err := rows.Scan(
			&state.State,
			&state.Receipts,
			&state.OldestAgeSeconds,
			&state.NewestAgeSeconds,
		); err != nil {
			rows.Close()
			return ProviderReceiptAudit{}, fmt.Errorf("scan provider receipt state: %w", err)
		}
		audit.States = append(audit.States, state)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ProviderReceiptAudit{}, fmt.Errorf("iterate provider receipt states: %w", err)
	}
	rows.Close()

	audit.Failures, err = readProviderReceiptOutcomeGroups(
		ctx, tx, checkedAt, ReceiptFailed,
	)
	if err != nil {
		return ProviderReceiptAudit{}, fmt.Errorf("read provider receipt failures: %w", err)
	}
	audit.Quarantine, err = readProviderReceiptOutcomeGroups(
		ctx, tx, checkedAt, ReceiptQuarantined,
	)
	if err != nil {
		return ProviderReceiptAudit{}, fmt.Errorf("read provider receipt quarantine: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ProviderReceiptAudit{}, fmt.Errorf("commit provider receipt audit: %w", err)
	}
	return audit, nil
}

func readProviderReceiptOutcomeGroups(
	ctx context.Context,
	tx pgx.Tx,
	checkedAt time.Time,
	state ReceiptState,
) ([]ProviderReceiptOutcomeAudit, error) {
	if state != ReceiptFailed && state != ReceiptQuarantined {
		return nil, ErrInvalidInput
	}
	groups := []ProviderReceiptOutcomeAudit{}
	rows, err := tx.Query(ctx, `
		WITH bounded AS (
			SELECT
				event_type,
				CASE
					WHEN projection_error_code = ANY($3::text[])
						THEN projection_error_code
					ELSE 'UNCLASSIFIED'
				END AS error_code,
				call_id IS NOT NULL AS attached_to_call,
				projection_attempts,
				received_at
			FROM human_calling_provider_receipts
			WHERE state = $2
		)
		SELECT
			event_type,
			error_code,
			attached_to_call,
			count(*),
			min(projection_attempts),
			max(projection_attempts),
			GREATEST(0, EXTRACT(EPOCH FROM ($1 - min(received_at)))::bigint),
			GREATEST(0, EXTRACT(EPOCH FROM ($1 - max(received_at)))::bigint)
		FROM bounded
		GROUP BY event_type, error_code, attached_to_call
		ORDER BY event_type, error_code, attached_to_call DESC
	`, checkedAt, state, providerReceiptAuditErrorCodes)
	if err != nil {
		return nil, fmt.Errorf("query provider receipt outcome groups: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var group ProviderReceiptOutcomeAudit
		if err := rows.Scan(
			&group.EventType,
			&group.ErrorCode,
			&group.AttachedToCall,
			&group.Receipts,
			&group.MinAttempts,
			&group.MaxAttempts,
			&group.OldestAgeSeconds,
			&group.NewestAgeSeconds,
		); err != nil {
			return nil, fmt.Errorf("scan provider receipt outcome group: %w", err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider receipt outcome groups: %w", err)
	}
	return groups, nil
}
