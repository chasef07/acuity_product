package humancalling_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReceiptClaimHoldsEachCallSessionBehindItsInFlightReceipt(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Now().UTC()
	currentTime := now
	calling := humancalling.New(pool, access.New(pool, time.Now), nil, humancalling.Config{},
		func() time.Time { return currentTime })
	insertSessionReceipt := newSignedReceiptIngress(t, pool)

	insertSessionReceipt("session-1-first", "session-1", now.Add(-3*time.Second), now.Add(-3*time.Second))
	insertSessionReceipt("session-1-second", "session-1", now.Add(-2*time.Second), now.Add(-2*time.Second))
	insertSessionReceipt("session-2-first", "session-2", now.Add(-time.Second), now.Add(-time.Second))
	if _, err := pool.Exec(ctx, `
		UPDATE human_calling_provider_receipts
		SET state = 'PROCESSING', projection_attempts = 1,
			processing_started_at = $1, last_attempt_at = $1
		WHERE event_id = 'session-1-first'
	`, now); err != nil {
		t.Fatal(err)
	}

	processReceipt(t, calling, true)
	assertReceiptStates(t, pool, map[string]string{
		"session-1-first": "PROCESSING", "session-1-second": "PENDING", "session-2-first": "UNKNOWN",
	})
	processReceipt(t, calling, false)

	currentTime = now.Add(31 * time.Second)
	processReceipt(t, calling, true)
	assertReceiptStates(t, pool, map[string]string{
		"session-1-first": "UNKNOWN", "session-1-second": "PENDING",
	})
	processReceipt(t, calling, true)
	assertReceiptStates(t, pool, map[string]string{"session-1-second": "UNKNOWN"})

	insertSessionReceipt("session-3-backing-off", "session-3", now, currentTime.Add(time.Minute))
	insertSessionReceipt("session-3-ready", "session-3", now.Add(time.Second), currentTime)
	processReceipt(t, calling, true)
	assertReceiptStates(t, pool, map[string]string{
		"session-3-backing-off": "PENDING", "session-3-ready": "UNKNOWN",
	})
}

func TestConcurrentReceiptWorkersApplyEachCallSessionInArrivalOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := testdb.Open(t)
	database := &claimTimingDatabase{Database: pool, claimed: map[string]time.Time{}}
	calling := humancalling.New(database, access.New(pool, time.Now), nil, humancalling.Config{}, time.Now)
	insertSessionReceipt := newSignedReceiptIngress(t, pool)
	const sessions, perSession, workers = 4, 8, 4
	receivedAt := time.Now().Add(-time.Minute)
	for index := range sessions * perSession {
		session := fmt.Sprintf("ordered-session-%d", index%sessions)
		eventID := fmt.Sprintf("%s-receipt-%02d", session, index/sessions)
		at := receivedAt.Add(time.Duration(index) * time.Millisecond)
		insertSessionReceipt(eventID, session, at, at)
	}

	var group sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		group.Go(func() {
			for ctx.Err() == nil {
				processed, err := calling.ProcessNextReceipt(ctx)
				if err != nil {
					errs <- err
					return
				}
				if !processed {
					var open int
					if err := pool.QueryRow(ctx, `
						SELECT count(*) FROM human_calling_provider_receipts
						WHERE state IN ('PENDING', 'PROCESSING')
					`).Scan(&open); err != nil {
						errs <- err
						return
					}
					if open == 0 {
						return
					}
				}
			}
		})
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `
		SELECT event_id, call_session_id, projected_at, projection_attempts
		FROM human_calling_provider_receipts
		ORDER BY received_at, event_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	previousProjected := map[string]time.Time{}
	count := 0
	for rows.Next() {
		var eventID, session string
		var projectedAt time.Time
		var attempts int
		if err := rows.Scan(&eventID, &session, &projectedAt, &attempts); err != nil {
			t.Fatal(err)
		}
		claimedAt := database.claimedAt(eventID)
		count++
		if attempts != 1 {
			t.Errorf("%s projection attempts=%d want 1", eventID, attempts)
		}
		if previous, ok := previousProjected[session]; ok && claimedAt.Before(previous) {
			t.Errorf("%s was claimed at %s before the previous %s receipt finished projecting at %s",
				eventID, claimedAt.Format(time.RFC3339Nano), session, previous.Format(time.RFC3339Nano))
		}
		previousProjected[session] = projectedAt
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != sessions*perSession {
		t.Fatalf("receipts=%d want %d", count, sessions*perSession)
	}
}

func newSignedReceiptIngress(
	t *testing.T,
	pool *pgxpool.Pool,
) func(eventID, session string, receivedAt, nextAttemptAt time.Time) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return func(eventID, session string, receivedAt, nextAttemptAt time.Time) {
		t.Helper()
		ctx := context.Background()
		raw, err := json.Marshal(map[string]any{"data": map[string]any{
			"record_type": "event", "event_type": "call.dtmf.received", "id": eventID,
			"occurred_at": receivedAt.Format(time.RFC3339Nano),
			"payload":     map[string]any{"call_control_id": eventID + "-control", "call_session_id": session},
		}})
		if err != nil {
			t.Fatal(err)
		}
		ingress := humancalling.New(pool, nil, nil, humancalling.Config{WebhookPublicKeys: [][]byte{publicKey}},
			func() time.Time { return receivedAt })
		timestamp := strconv.FormatInt(receivedAt.Unix(), 10)
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, append([]byte(timestamp+"|"), raw...)))
		if receipt, err := ingress.ReceiveWebhook(ctx, raw, timestamp, signature); err != nil || receipt.Duplicate {
			t.Fatalf("signed receipt=%+v error=%v", receipt, err)
		}
		var stored string
		if err := pool.QueryRow(ctx, `
			UPDATE human_calling_provider_receipts SET next_attempt_at = $2
			WHERE event_id = $1
			RETURNING call_session_id
		`, eventID, nextAttemptAt).Scan(&stored); err != nil || stored != session {
			t.Fatalf("stored call session=%q error=%v want %q", stored, err, session)
		}
	}
}

func processReceipt(t *testing.T, calling *humancalling.Module, want bool) {
	t.Helper()
	processed, err := calling.ProcessNextReceipt(context.Background())
	if err != nil || processed != want {
		t.Fatalf("process receipt = %t, %v; want %t", processed, err, want)
	}
}

func assertReceiptStates(t *testing.T, pool *pgxpool.Pool, want map[string]string) {
	t.Helper()
	for eventID, state := range want {
		var got string
		if err := pool.QueryRow(context.Background(), `
			SELECT state FROM human_calling_provider_receipts WHERE event_id = $1
		`, eventID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != state {
			t.Errorf("%s state=%s want %s", eventID, got, state)
		}
	}
}

type claimTimingDatabase struct {
	postgres.Database
	mu      sync.Mutex
	claimed map[string]time.Time
}

func (database *claimTimingDatabase) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	row := database.Database.QueryRow(ctx, sql, args...)
	if !strings.Contains(sql, "RETURNING receipt.event_id") {
		return row
	}
	return claimTimingRow{Row: row, database: database}
}

func (database *claimTimingDatabase) claimedAt(eventID string) time.Time {
	database.mu.Lock()
	defer database.mu.Unlock()
	return database.claimed[eventID]
}

type claimTimingRow struct {
	pgx.Row
	database *claimTimingDatabase
}

func (row claimTimingRow) Scan(destinations ...any) error {
	if err := row.Row.Scan(destinations...); err != nil {
		return err
	}
	claimedAt := time.Now()
	row.database.mu.Lock()
	defer row.database.mu.Unlock()
	row.database.claimed[*destinations[0].(*string)] = claimedAt
	return nil
}
