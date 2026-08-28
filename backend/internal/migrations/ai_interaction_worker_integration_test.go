package migrations_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWorkerProjectsCloseoutAndPreservesHistoricalSummaryReceipt(t *testing.T) {
	pool := testdb.Open(t)
	createDatabaseRoles(t, pool)
	applyDatabaseGrants(t, pool)

	ctx := context.Background()
	now := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	var practiceID, locationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_practices (provisioning_key, name)
		VALUES ('interaction-worker-grant', 'Interaction Worker Grant')
		RETURNING id::text
	`).Scan(&practiceID); err != nil {
		t.Fatalf("seed Practice: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_locations (practice_id, provisioning_key, name)
		VALUES ($1, 'main', 'Main')
		RETURNING id::text
	`, practiceID).Scan(&locationID); err != nil {
		t.Fatalf("seed Location: %v", err)
	}

	const sourceCallID = "worker-grant-call"
	startedAt := now.Add(-time.Minute)
	if _, err := pool.Exec(ctx, `
		INSERT INTO ai_interactions (
			service_subject, practice_id, location_id, source_call_id,
			phone, office_phone, started_at, status, lifecycle_stage,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'IN_PROGRESS', 1, $8, $8)
	`, "abita-agent", practiceID, locationID, sourceCallID,
		"+17275550111", "+17275550112", startedAt, startedAt); err != nil {
		t.Fatalf("seed AI Interaction: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"kind":         "CLOSEOUT",
		"sourceCallId": sourceCallID,
		"callerPhone":  "+17275550111",
		"officePhone":  "+17275550112",
		"startedAt":    startedAt,
		"endedAt":      now,
		"status":       "COMPLETED",
		"summary":      "Worker projection completed.",
		"transcript":   map[string]any{"items": []map[string]any{{"role": "user"}}},
		"closeoutPayload": map[string]any{
			"callId":        sourceCallID,
			"sessionReport": map[string]any{"items": []map[string]any{{"role": "user"}}},
		},
	})
	if err != nil {
		t.Fatalf("encode receipt payload: %v", err)
	}
	fingerprint := sha256.Sum256(payload)
	if _, err := pool.Exec(ctx, `
		INSERT INTO ai_interaction_receipts (
			service_subject, practice_id, location_id, source_call_id,
			kind, payload_fingerprint, payload, received_at
		) VALUES ($1, $2, $3, $4, 'CLOSEOUT', $5, $6, $7)
	`, "abita-agent", practiceID, locationID, sourceCallID,
		fingerprint[:], payload, now); err != nil {
		t.Fatalf("seed pending receipt: %v", err)
	}

	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse worker database config: %v", err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, "SET ROLE acuity_worker")
		return err
	}
	workerPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open worker pool: %v", err)
	}
	t.Cleanup(workerPool.Close)
	module := interaction.New(
		workerPool,
		access.New(workerPool, func() time.Time { return now }),
		func() time.Time { return now },
	)

	t.Run("projects CLOSEOUT", func(t *testing.T) {
		processed, err := module.ProcessNextReceipt(ctx)
		if err != nil || !processed {
			t.Fatalf("project pending receipt as acuity_worker = %t, %v", processed, err)
		}
		processed, err = module.ProcessNextReceipt(ctx)
		if err != nil || processed {
			t.Fatalf("pending receipt remains after projection = %t, %v", processed, err)
		}
		var status interaction.CallStatus
		var transcript, closeoutPayload json.RawMessage
		if err := pool.QueryRow(ctx, `
			SELECT status, transcript, closeout_payload
			FROM ai_interactions
			WHERE practice_id = $1 AND source_call_id = $2
		`, practiceID, sourceCallID).Scan(&status, &transcript, &closeoutPayload); err != nil {
			t.Fatalf("read projected CLOSEOUT: %v", err)
		}
		if status != interaction.CallCompleted ||
			!json.Valid(transcript) || !json.Valid(closeoutPayload) {
			t.Fatalf("projected CLOSEOUT = (%q, %s, %s)", status, transcript, closeoutPayload)
		}
	})

	t.Run("preserves historical SUMMARY receipt", func(t *testing.T) {
		historicalPayload, err := json.Marshal(map[string]any{
			"kind":           "SUMMARY",
			"sourceCallId":   "historical-summary-call",
			"callerPhone":    "+17275550113",
			"officePhone":    "+17275550112",
			"startedAt":      startedAt,
			"endedAt":        now,
			"status":         "COMPLETED",
			"summaryPayload": map[string]any{"phase": "summary"},
		})
		if err != nil {
			t.Fatalf("encode historical SUMMARY receipt: %v", err)
		}
		historicalFingerprint := sha256.Sum256(historicalPayload)
		var historicalReceiptID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO ai_interaction_receipts (
				service_subject, practice_id, location_id, source_call_id,
				kind, payload_fingerprint, payload, received_at
			) VALUES ($1, $2, $3, $4, 'SUMMARY', $5, $6, $7)
			RETURNING id::text
		`, "abita-agent", practiceID, locationID, "historical-summary-call",
			historicalFingerprint[:], historicalPayload, now.Add(time.Second)).Scan(
			&historicalReceiptID,
		); err != nil {
			t.Fatalf("seed historical SUMMARY receipt: %v", err)
		}
		var storedPayloadBefore []byte
		if err := pool.QueryRow(ctx, `
			SELECT payload
			FROM ai_interaction_receipts
			WHERE id = $1
		`, historicalReceiptID).Scan(&storedPayloadBefore); err != nil {
			t.Fatalf("read historical SUMMARY receipt before worker: %v", err)
		}
		processed, err := module.ProcessNextReceipt(ctx)
		if err != nil || processed {
			t.Fatalf("historical SUMMARY receipt was selected = %t, %v", processed, err)
		}
		var receiptState, projectionErrorCode string
		var storedPayload []byte
		if err := pool.QueryRow(ctx, `
			SELECT state, COALESCE(projection_error_code, ''), payload
			FROM ai_interaction_receipts
			WHERE id = $1
		`, historicalReceiptID).Scan(
			&receiptState,
			&projectionErrorCode,
			&storedPayload,
		); err != nil {
			t.Fatalf("read preserved historical SUMMARY receipt: %v", err)
		}
		if receiptState != "PENDING" || projectionErrorCode != "" ||
			!bytes.Equal(storedPayload, storedPayloadBefore) {
			t.Fatalf("historical SUMMARY receipt changed = (%q, %q, %s)",
				receiptState, projectionErrorCode, storedPayload)
		}
	})
}
