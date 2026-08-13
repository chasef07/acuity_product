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

type ProviderReceiptQuarantineAudit struct {
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
	CheckedAt  time.Time                        `json:"checkedAt"`
	States     []ProviderReceiptStateAudit      `json:"states"`
	Quarantine []ProviderReceiptQuarantineAudit `json:"quarantine"`
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
		Quarantine: []ProviderReceiptQuarantineAudit{},
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

	rows, err = tx.Query(ctx, `
		SELECT
			event_type,
			COALESCE(projection_error_code, ''),
			call_id IS NOT NULL,
			count(*),
			min(projection_attempts),
			max(projection_attempts),
			GREATEST(0, EXTRACT(EPOCH FROM ($1 - min(received_at)))::bigint),
			GREATEST(0, EXTRACT(EPOCH FROM ($1 - max(received_at)))::bigint)
		FROM human_calling_provider_receipts
		WHERE state = 'QUARANTINED'
		GROUP BY event_type, projection_error_code, call_id IS NOT NULL
		ORDER BY event_type, projection_error_code, call_id IS NOT NULL DESC
	`, checkedAt)
	if err != nil {
		return ProviderReceiptAudit{}, fmt.Errorf("read provider receipt quarantine: %w", err)
	}
	for rows.Next() {
		var group ProviderReceiptQuarantineAudit
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
			rows.Close()
			return ProviderReceiptAudit{}, fmt.Errorf("scan provider receipt quarantine: %w", err)
		}
		audit.Quarantine = append(audit.Quarantine, group)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ProviderReceiptAudit{}, fmt.Errorf("iterate provider receipt quarantine: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return ProviderReceiptAudit{}, fmt.Errorf("commit provider receipt audit: %w", err)
	}
	return audit, nil
}
