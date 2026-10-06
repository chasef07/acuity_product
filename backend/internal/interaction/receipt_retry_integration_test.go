package interaction

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedRetryLocation(t *testing.T, pool *pgxpool.Pool, key string) (string, string) {
	t.Helper()
	var practice, location string
	if err := pool.QueryRow(context.Background(), `INSERT INTO access_practices(provisioning_key,name) VALUES($1,'Synthetic') RETURNING id::text`, key).Scan(&practice); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practice).Scan(&location); err != nil {
		t.Fatal(err)
	}
	return practice, location
}

func seedRetryReceipt(t *testing.T, pool *pgxpool.Pool, practice, location, source string, receivedAt time.Time) string {
	t.Helper()
	ended := receivedAt.Add(-time.Minute)
	raw, err := json.Marshal(storedReceiptPayload{Kind: MessageCloseout, SourceCallID: source, CallerPhone: "+15555550123", OfficePhone: "+15555550100", StartedAt: ended.Add(-time.Hour), EndedAt: &ended, Status: CallCompleted, CloseoutPayload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(raw)
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO ai_interaction_receipts(service_subject,practice_id,location_id,source_call_id,kind,payload_fingerprint,payload,received_at) VALUES('synthetic',$1,$2,$3,'CLOSEOUT',$4,$5,$6) RETURNING id::text`, practice, location, source, fingerprint[:], raw, receivedAt).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPoisonReceiptRetriesWithoutBlockingThenQuarantinesVisibly(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	practice, location := seedRetryLocation(t, pool, "poison-receipt")
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION synthetic_poison_projection() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.source_call_id = 'synthetic-poison' THEN
				RAISE EXCEPTION 'synthetic projection failure' USING ERRCODE = 'P0001';
			END IF;
			RETURN NEW;
		END;
		$$;
		CREATE TRIGGER synthetic_poison_projection BEFORE INSERT ON ai_interactions
		FOR EACH ROW EXECUTE FUNCTION synthetic_poison_projection();
	`); err != nil {
		t.Fatal(err)
	}
	poison := seedRetryReceipt(t, pool, practice, location, "synthetic-poison", now.Add(-2*time.Hour))
	healthy := seedRetryReceipt(t, pool, practice, location, "synthetic-healthy", now.Add(-time.Hour))
	clock := now
	module := New(pool, access.New(pool, func() time.Time { return clock }), func() time.Time { return clock })
	readReceipt := func(id string) (state, errorCode, lastError string, attempts int, next *time.Time) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT state, COALESCE(projection_error_code,''), COALESCE(last_error_code,''), projection_attempts, next_attempt_at FROM ai_interaction_receipts WHERE id=$1`, id).Scan(&state, &errorCode, &lastError, &attempts, &next); err != nil {
			t.Fatal(err)
		}
		return
	}

	if processed, err := module.ProcessNextReceipt(ctx); !processed || err == nil {
		t.Fatalf("poison attempt = %t %v, want visible failure", processed, err)
	}
	state, _, lastError, attempts, next := readReceipt(poison)
	if state != "PENDING" || lastError != "DATABASE_P0001" || attempts != 1 || next == nil || !next.Equal(now.Add(receiptRetryDelay(1))) {
		t.Fatalf("poison after first attempt: %s %s %d %v", state, lastError, attempts, next)
	}
	if processed, err := module.ProcessNextReceipt(ctx); !processed || err != nil {
		t.Fatalf("healthy receipt blocked behind poison: %t %v", processed, err)
	}
	if state, _, _, _, _ := readReceipt(healthy); state != "PROJECTED" {
		t.Fatalf("healthy receipt state = %s", state)
	}
	if processed, err := module.ProcessNextReceipt(ctx); processed || err != nil {
		t.Fatalf("poison retried before its backoff: %t %v", processed, err)
	}

	for attempt := 2; attempt <= maxReceiptProjectionAttempts; attempt++ {
		clock = clock.Add(maxReceiptRetryDelay)
		if processed, err := module.ProcessNextReceipt(ctx); !processed || err == nil {
			t.Fatalf("attempt %d = %t %v", attempt, processed, err)
		}
	}
	state, errorCode, lastError, attempts, _ := readReceipt(poison)
	if state != "QUARANTINED" || errorCode != receiptRetryExhausted || lastError != "DATABASE_P0001" || attempts != maxReceiptProjectionAttempts {
		t.Fatalf("exhausted poison: %s %s %s %d", state, errorCode, lastError, attempts)
	}
	clock = clock.Add(maxReceiptRetryDelay)
	if processed, err := module.ProcessNextReceipt(ctx); processed || err != nil {
		t.Fatalf("quarantined receipt replayed: %t %v", processed, err)
	}
}

func TestReceiptWorkerSkipsReceiptLockedByAnotherWorker(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	practice, location := seedRetryLocation(t, pool, "locked-receipt")
	locked := seedRetryReceipt(t, pool, practice, location, "synthetic-locked", now.Add(-2*time.Hour))
	free := seedRetryReceipt(t, pool, practice, location, "synthetic-free", now.Add(-time.Hour))
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `SELECT 1 FROM ai_interaction_receipts WHERE id=$1 FOR UPDATE`, locked); err != nil {
		t.Fatal(err)
	}
	module := New(pool, access.New(pool, func() time.Time { return now }), func() time.Time { return now })
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if processed, err := module.ProcessNextReceipt(bounded); !processed || err != nil {
		t.Fatalf("worker waited on a locked receipt: %t %v", processed, err)
	}
	var lockedState, freeState string
	if err := pool.QueryRow(ctx, `SELECT (SELECT state FROM ai_interaction_receipts WHERE id=$1), (SELECT state FROM ai_interaction_receipts WHERE id=$2)`, locked, free).Scan(&lockedState, &freeState); err != nil {
		t.Fatal(err)
	}
	if lockedState != "PENDING" || freeState != "PROJECTED" {
		t.Fatalf("locked=%s free=%s", lockedState, freeState)
	}
}

func TestTimedOutFinalAttemptStillQuarantinesReceipt(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	practice, location := seedRetryLocation(t, pool, "timeout-receipt")
	id := seedRetryReceipt(t, pool, practice, location, "synthetic-timeout", now.Add(-time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE ai_interaction_receipts SET projection_attempts=$2 WHERE id=$1`, id, maxReceiptProjectionAttempts-1); err != nil {
		t.Fatal(err)
	}
	module := New(pool, access.New(pool, func() time.Time { return now }), func() time.Time { return now })
	_, _, attempts, claimed, err := module.claimReceipt(ctx)
	if err != nil || !claimed || attempts != maxReceiptProjectionAttempts {
		t.Fatalf("claim = %t %d %v", claimed, attempts, err)
	}
	expired, cancel := context.WithDeadline(ctx, now)
	cancel()
	if err := module.recordReceiptFailure(expired, id, attempts, expired.Err()); err != nil {
		t.Fatalf("record failure after work deadline: %v", err)
	}
	var state, errorCode, lastError string
	if err := pool.QueryRow(ctx, `SELECT state, COALESCE(projection_error_code,''), COALESCE(last_error_code,'') FROM ai_interaction_receipts WHERE id=$1`, id).Scan(&state, &errorCode, &lastError); err != nil {
		t.Fatal(err)
	}
	if state != "QUARANTINED" || errorCode != receiptRetryExhausted || lastError != "TIMEOUT" {
		t.Fatalf("timed-out final attempt: %s %s %s", state, errorCode, lastError)
	}
}
