package humancalling_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestProviderReceiptAuditAttributesTerminalFailuresWithoutIdentifiers(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 11, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	const (
		practiceID = "00000000-0000-0000-0000-000000000801"
		locationID = "00000000-0000-0000-0000-000000000802"
		handoffID  = "00000000-0000-0000-0000-000000000803"
		callID     = "00000000-0000-0000-0000-000000000804"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES ($1, 'receipt-audit', 'Receipt Audit')
	`, practiceID); err != nil {
		t.Fatalf("seed receipt audit Practice: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES ($2, $1, 'main', 'Main')
	`, practiceID, locationID); err != nil {
		t.Fatalf("seed receipt audit Location: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, expires_at
		) VALUES ($1, 'receipt-audit', $2, $3, 'source', 'receipt-audit',
			'\x01', $4)
	`, handoffID, practiceID, locationID, now.Add(time.Hour)); err != nil {
		t.Fatalf("seed receipt audit handoff: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_calls (
			id, source_handoff_id, practice_id, location_id, caller_phone
		) VALUES ($1, $2, $3, $4, '+17275550123')
	`, callID, handoffID, practiceID, locationID); err != nil {
		t.Fatalf("seed receipt audit Call: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_provider_receipts (
			event_id, call_id, event_type, occurred_at, received_at,
			signature_timestamp, raw_body, state, projection_attempts,
			projection_error_code, last_attempt_at, next_attempt_at,
			projected_at, quarantined_at
		) VALUES
			('attached-secret-event', $1, 'call.answered', $2, $2, 1,
			 '\x736563726574', 'QUARANTINED', 10,
			 'PROJECTION_APPLY_FACT_RETRY', $3, $3, $3, $3),
			('orphan-secret-event', NULL, 'call.hangup', $4, $4, 1,
			 '\x70726976617465', 'QUARANTINED', 12,
			 'PROJECTION_RETRY_EXHAUSTED', $3, $3, $3, $3),
			('pending-secret-event', NULL, 'call.initiated', $5, $5, 1,
			 '\x70656e64696e67', 'PENDING', 0,
			 'WAITING_FOR_RELATED_FACT_SLOW_RETRY', NULL, $3, NULL, NULL),
			('retry-secret-event', NULL, 'call.answered', $5, $5, 1,
			 '\x7265747279', 'PENDING', 1,
			 'PROJECTION_APPLY_FACT_RETRY', $5, $3, NULL, NULL),
			('attached-failure-secret', $1, 'call.answered', $6, $6, 1,
			 '\x61747461636865642d70726976617465', 'PENDING', 0,
			 NULL, NULL, $6, NULL, NULL),
			('orphan-failure-secret', NULL, 'call.hangup', $7, $7, 1,
			 '\x6f727068616e2d70726976617465', 'PENDING', 0,
			 NULL, NULL, $7, NULL, NULL)
	`, callID, now.Add(-2*time.Minute), now, now.Add(-5*time.Minute),
		now.Add(-30*time.Second), now.Add(-4*time.Minute),
		now.Add(-3*time.Minute)); err != nil {
		t.Fatalf("seed provider receipts: %v", err)
	}

	var metrics bytes.Buffer
	calling := humancalling.New(pool, nil, nil, humancalling.Config{
		Observer: observability.NewLogger(
			observability.RuntimeWorker,
			"worker-receipt-audit-test",
			slog.New(slog.NewJSONHandler(&metrics, nil)),
		),
	}, func() time.Time {
		return now
	})
	for range 2 {
		processed, err := calling.ProcessNextReceipt(ctx)
		if err != nil || !processed {
			t.Fatalf("process terminal receipt failure: processed=%t err=%v", processed, err)
		}
	}
	audit, err := calling.AuditProviderReceipts(ctx)
	if err != nil {
		t.Fatalf("audit provider receipts: %v", err)
	}
	if len(audit.States) != 3 || audit.States[0].State != "FAILED" ||
		audit.States[0].Receipts != 2 || audit.States[0].OldestAgeSeconds != 240 ||
		audit.States[0].NewestAgeSeconds != 180 ||
		audit.States[1].State != "PENDING" || audit.States[1].Receipts != 2 ||
		audit.States[1].OldestAgeSeconds != 30 ||
		audit.States[2].State != "QUARANTINED" || audit.States[2].Receipts != 2 ||
		audit.States[2].OldestAgeSeconds != 300 {
		t.Fatalf("receipt state audit = %#v", audit.States)
	}
	if len(audit.Failures) != 2 ||
		audit.Failures[0].EventType != "call.answered" ||
		audit.Failures[0].ErrorCode != "INVALID_PROVIDER_EVENT" ||
		!audit.Failures[0].AttachedToCall || audit.Failures[0].Receipts != 1 ||
		audit.Failures[0].MinAttempts != 1 || audit.Failures[0].MaxAttempts != 1 ||
		audit.Failures[1].EventType != "call.hangup" ||
		audit.Failures[1].ErrorCode != "INVALID_PROVIDER_EVENT" ||
		audit.Failures[1].AttachedToCall || audit.Failures[1].Receipts != 1 ||
		audit.Failures[1].MinAttempts != 1 || audit.Failures[1].MaxAttempts != 1 {
		t.Fatalf("receipt failure audit = %#v", audit.Failures)
	}
	if len(audit.Quarantine) != 2 ||
		audit.Quarantine[0].EventType != "call.answered" ||
		audit.Quarantine[0].ErrorCode != "PROJECTION_APPLY_FACT_RETRY" ||
		!audit.Quarantine[0].AttachedToCall || audit.Quarantine[0].Receipts != 1 ||
		audit.Quarantine[0].MinAttempts != 10 || audit.Quarantine[0].MaxAttempts != 10 ||
		audit.Quarantine[1].EventType != "call.hangup" ||
		audit.Quarantine[1].ErrorCode != "PROJECTION_RETRY_EXHAUSTED" ||
		audit.Quarantine[1].AttachedToCall || audit.Quarantine[1].Receipts != 1 ||
		audit.Quarantine[1].MinAttempts != 12 || audit.Quarantine[1].MaxAttempts != 12 {
		t.Fatalf("receipt quarantine audit = %#v", audit.Quarantine)
	}
	encoded, err := json.Marshal(audit)
	if err != nil {
		t.Fatalf("encode receipt audit: %v", err)
	}
	for _, secret := range []string{
		"attached-secret-event",
		"orphan-secret-event",
		"secret",
		"private",
		"pending",
		"retry-secret-event",
		"attached-failure-secret",
		"orphan-failure-secret",
		"attached-private",
		"orphan-private",
		callID,
	} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("receipt audit exposed %q: %s", secret, encoded)
		}
	}
	if err := calling.ReportReceiptQueue(ctx); err != nil {
		t.Fatalf("report provider receipt queue: %v", err)
	}
	for _, field := range []string{
		`"depth":2`,
		`"projection_retry_depth":1`,
		`"related_fact_depth":1`,
		`"quarantined_depth":2`,
	} {
		if !strings.Contains(metrics.String(), field) {
			t.Fatalf("receipt queue metric omitted %s: %s", field, metrics.String())
		}
	}
}

func TestProviderReceiptAuditBoundsUnexpectedErrorCodes(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 17, 11, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_provider_receipts (
			event_id, event_type, occurred_at, received_at,
			signature_timestamp, raw_body, state, projection_attempts,
			projection_error_code, last_attempt_at, next_attempt_at,
			projected_at
		) VALUES (
			'unexpected-code-event', 'call.answered', $1, $1,
			1, '\x707269766174652d7261772d626f6479', 'FAILED', 1,
			'UNEXPECTED_INTERNAL_CODE', $1, $1, $1
		), (
			'another-unexpected-code-event', 'call.answered', $2, $2,
			1, '\x616e6f746865722d707269766174652d626f6479', 'FAILED', 2,
			'ANOTHER_UNEXPECTED_CODE', $2, $2, $2
		)
	`, now.Add(-time.Minute), now.Add(-30*time.Second)); err != nil {
		t.Fatalf("seed unexpected receipt error code: %v", err)
	}

	calling := humancalling.New(pool, nil, nil, humancalling.Config{}, func() time.Time {
		return now
	})
	audit, err := calling.AuditProviderReceipts(ctx)
	if err != nil {
		t.Fatalf("audit unexpected receipt error code: %v", err)
	}
	if len(audit.Failures) != 1 ||
		audit.Failures[0].ErrorCode != "UNCLASSIFIED" ||
		audit.Failures[0].Receipts != 2 ||
		audit.Failures[0].MinAttempts != 1 || audit.Failures[0].MaxAttempts != 2 {
		t.Fatalf("bounded receipt failure audit = %#v", audit.Failures)
	}
	encoded, err := json.Marshal(audit)
	if err != nil {
		t.Fatalf("encode unexpected receipt error code audit: %v", err)
	}
	for _, private := range []string{
		"unexpected-code-event",
		"another-unexpected-code-event",
		"UNEXPECTED_INTERNAL_CODE",
		"ANOTHER_UNEXPECTED_CODE",
		"private-raw-body",
		"another-private-body",
	} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("receipt failure audit exposed unbounded value %q", private)
		}
	}
}
