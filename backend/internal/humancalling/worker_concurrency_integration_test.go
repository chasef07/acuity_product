package humancalling_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWorkerSerializesCommandsPerCallWithoutBlockingOtherWork(t *testing.T) {
	setupPool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	seedWorkerConcurrencyFixture(t, setupPool, now)
	pool := openTwoConnectionWorkerPool(t)

	provider := newSerialCallProvider()
	newWorker := func() *humancalling.Module {
		return humancalling.New(
			pool,
			nil,
			provider,
			humancalling.Config{},
			func() time.Time { return now },
		)
	}
	firstWorker := newWorker()
	secondWorker := newWorker()
	receiptWorker := newWorker()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstResult := make(chan error, 1)
	go func() {
		processed, err := firstWorker.ProcessNextCommand(ctx)
		if err == nil && !processed {
			err = fmt.Errorf("first worker processed no command")
		}
		firstResult <- err
	}()
	awaitSignal(t, provider.firstCallAStarted, "first Call A command")

	secondResult := make(chan error, 1)
	go func() {
		processed, err := secondWorker.ProcessNextCommand(ctx)
		if err == nil && !processed {
			err = fmt.Errorf("second worker processed no command")
		}
		secondResult <- err
	}()
	receiptResult := make(chan error, 1)
	go func() {
		processed, err := receiptWorker.ProcessNextReceipt(ctx)
		if err == nil && !processed {
			err = fmt.Errorf("receipt worker processed no receipt")
		}
		receiptResult <- err
	}()

	select {
	case target := <-provider.overlap:
		close(provider.releaseFirstCallA)
		t.Fatalf("same-Call provider commands overlapped at %s", target)
	case <-provider.callBStarted:
	case <-ctx.Done():
		close(provider.releaseFirstCallA)
		t.Fatal("Call B command did not run while Call A provider request was blocked")
	}
	if err := <-receiptResult; err != nil {
		close(provider.releaseFirstCallA)
		t.Fatalf("receipt projection while Call A was blocked: %v", err)
	}

	close(provider.releaseFirstCallA)
	if err := <-firstResult; err != nil {
		t.Fatalf("first Call A command: %v", err)
	}
	if err := <-secondResult; err != nil {
		t.Fatalf("Call B command: %v", err)
	}
	if processed, err := firstWorker.ProcessNextCommand(ctx); err != nil || !processed {
		t.Fatalf("second Call A command after release: processed=%t err=%v", processed, err)
	}
	awaitSignal(t, provider.secondCallAStarted, "second Call A command")
	select {
	case target := <-provider.overlap:
		t.Fatalf("same-Call provider commands overlapped at %s", target)
	default:
	}

	var sentCommands, unknownReceipts int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM human_calling_provider_commands WHERE state = 'SENT'),
			(SELECT count(*) FROM human_calling_provider_receipts WHERE state = 'UNKNOWN')
	`).Scan(&sentCommands, &unknownReceipts); err != nil {
		t.Fatalf("read worker concurrency outcomes: %v", err)
	}
	if sentCommands != 3 || unknownReceipts != 1 {
		t.Fatalf(
			"outcomes = %d sent commands, %d unknown receipts; want 3 and 1",
			sentCommands,
			unknownReceipts,
		)
	}
}

func TestMixedRevisionClaimRaceKeepsOneActiveCommandPerCall(t *testing.T) {
	setupPool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	seedWorkerConcurrencyFixture(t, setupPool, now)
	pool := openTwoConnectionWorkerPool(t)
	provider := newSerialCallProvider()
	worker := humancalling.New(
		pool,
		nil,
		provider,
		humancalling.Config{},
		func() time.Time { return now },
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	oldWorkerClaim, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin old-worker claim: %v", err)
	}
	defer func() { _ = oldWorkerClaim.Rollback(ctx) }()
	var oldCommandID string
	if err := oldWorkerClaim.QueryRow(ctx, `
		SELECT id::text
		FROM human_calling_provider_commands
		WHERE state = 'PENDING' AND next_attempt_at <= $1
		ORDER BY created_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, now).Scan(&oldCommandID); err != nil {
		t.Fatalf("old worker select command: %v", err)
	}
	if _, err := oldWorkerClaim.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'SENDING', attempts = attempts + 1, updated_at = $2
		WHERE id = $1
	`, oldCommandID, now); err != nil {
		t.Fatalf("old worker mark command sending: %v", err)
	}

	type claimResult struct {
		processed bool
		err       error
	}
	result := make(chan claimResult, 1)
	go func() {
		processed, err := worker.ProcessNextCommand(ctx)
		result <- claimResult{processed: processed, err: err}
	}()
	awaitCommandClaimConflict(t, setupPool)
	if err := oldWorkerClaim.Commit(ctx); err != nil {
		t.Fatalf("commit old-worker claim: %v", err)
	}
	raceResult := <-result
	if raceResult.err != nil || raceResult.processed {
		t.Fatalf(
			"new worker claim race: processed=%t err=%v",
			raceResult.processed,
			raceResult.err,
		)
	}
	select {
	case <-provider.secondCallAStarted:
		t.Fatal("new worker called provider for the old worker's active Call")
	default:
	}

	if processed, err := worker.ProcessNextCommand(ctx); err != nil || !processed {
		t.Fatalf("process different Call after claim conflict: processed=%t err=%v", processed, err)
	}
	awaitSignal(t, provider.callBStarted, "Call B command after mixed-revision conflict")

	if _, err := setupPool.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'SENT', sent_at = $2, updated_at = $2
		WHERE id = $1 AND state = 'SENDING'
	`, oldCommandID, now); err != nil {
		t.Fatalf("finish old-worker provider command: %v", err)
	}
	if processed, err := worker.ProcessNextCommand(ctx); err != nil || !processed {
		t.Fatalf("process next same-Call command: processed=%t err=%v", processed, err)
	}
	awaitSignal(t, provider.secondCallAStarted, "Call A command after old worker finished")
}

func openTwoConnectionWorkerPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse two-connection pool config: %v", err)
	}
	config.MinConns = 0
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open two-connection pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func awaitCommandClaimConflict(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := pool.QueryRow(context.Background(), `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
					AND pid <> pg_backend_pid()
					AND state = 'active'
					AND wait_event_type = 'Lock'
					AND query LIKE '%UPDATE human_calling_provider_commands%'
					AND query LIKE '%RETURNING payload%'
			)
		`).Scan(&blocked); err != nil {
			t.Fatalf("inspect command claim conflict: %v", err)
		}
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("new worker did not wait on the mixed-revision command conflict")
}

type serialCallProvider struct {
	mu                     sync.Mutex
	activeCalls            map[string]bool
	firstCallAStarted      chan struct{}
	secondCallAStarted     chan struct{}
	callBStarted           chan struct{}
	releaseFirstCallA      chan struct{}
	overlap                chan string
	firstCallAStartedOnce  sync.Once
	secondCallAStartedOnce sync.Once
	callBStartedOnce       sync.Once
}

func newSerialCallProvider() *serialCallProvider {
	return &serialCallProvider{
		activeCalls:        make(map[string]bool),
		firstCallAStarted:  make(chan struct{}),
		secondCallAStarted: make(chan struct{}),
		callBStarted:       make(chan struct{}),
		releaseFirstCallA:  make(chan struct{}),
		overlap:            make(chan string, 1),
	}
}

func (provider *serialCallProvider) Execute(
	ctx context.Context,
	command humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	callKey := "call-a"
	if command.TargetID == "call-b-command" {
		callKey = "call-b"
	}
	provider.mu.Lock()
	if provider.activeCalls[callKey] {
		select {
		case provider.overlap <- command.TargetID:
		default:
		}
	}
	provider.activeCalls[callKey] = true
	provider.mu.Unlock()
	defer func() {
		provider.mu.Lock()
		provider.activeCalls[callKey] = false
		provider.mu.Unlock()
	}()

	switch command.TargetID {
	case "call-a-first":
		provider.firstCallAStartedOnce.Do(func() { close(provider.firstCallAStarted) })
		select {
		case <-provider.releaseFirstCallA:
		case <-ctx.Done():
			return humancalling.ProviderResult{}, ctx.Err()
		}
	case "call-a-second":
		provider.secondCallAStartedOnce.Do(func() { close(provider.secondCallAStarted) })
	case "call-b-command":
		provider.callBStartedOnce.Do(func() { close(provider.callBStarted) })
	}
	return humancalling.ProviderResult{}, nil
}

func awaitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func seedWorkerConcurrencyFixture(
	t *testing.T,
	pool *pgxpool.Pool,
	now time.Time,
) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES (
			'00000000-0000-0000-0000-000000000401',
			'worker-concurrency-practice',
			'Worker Concurrency Practice'
		);
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES (
			'00000000-0000-0000-0000-000000000402',
			'00000000-0000-0000-0000-000000000401',
			'worker-concurrency-location',
			'Worker Concurrency Location'
		);
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, token_hash, expires_at, created_at
		)
		VALUES
			(
				'00000000-0000-0000-0000-000000000411',
				'worker-concurrency', '00000000-0000-0000-0000-000000000401',
				'00000000-0000-0000-0000-000000000402', 'source-call-a',
				'worker-concurrency-a', '\x01', '\x11',
				$1::timestamptz + interval '1 hour', $1
			),
			(
				'00000000-0000-0000-0000-000000000412',
				'worker-concurrency', '00000000-0000-0000-0000-000000000401',
				'00000000-0000-0000-0000-000000000402', 'source-call-b',
				'worker-concurrency-b', '\x02', '\x12',
				$1::timestamptz + interval '1 hour', $1
			);
		INSERT INTO human_calling_calls (
			id, handoff_id, practice_id, location_id, state, offer_deadline,
			caller_call_control_id, caller_call_leg_id, call_session_id,
			created_at, updated_at
		)
		VALUES
			(
				'00000000-0000-0000-0000-000000000421',
				'00000000-0000-0000-0000-000000000411',
				'00000000-0000-0000-0000-000000000401',
				'00000000-0000-0000-0000-000000000402', 'OFFERING',
				$1::timestamptz + interval '1 minute',
				'caller-control-a', 'caller-leg-a',
				'caller-session-a', $1, $1
			),
			(
				'00000000-0000-0000-0000-000000000422',
				'00000000-0000-0000-0000-000000000412',
				'00000000-0000-0000-0000-000000000401',
				'00000000-0000-0000-0000-000000000402', 'OFFERING',
				$1::timestamptz + interval '1 minute',
				'caller-control-b', 'caller-leg-b',
				'caller-session-b', $1, $1
			);
		INSERT INTO human_calling_provider_commands (
			id, call_id, action, target_id, next_attempt_at, created_at, updated_at
		)
		VALUES
			(
				'00000000-0000-0000-0000-000000000431',
				'00000000-0000-0000-0000-000000000421',
				'HANGUP', 'call-a-first', $1, $1, $1
			),
			(
				'00000000-0000-0000-0000-000000000432',
				'00000000-0000-0000-0000-000000000421',
				'HANGUP', 'call-a-second', $1,
				$1::timestamptz + interval '1 microsecond', $1
			),
			(
				'00000000-0000-0000-0000-000000000433',
				'00000000-0000-0000-0000-000000000422',
				'HANGUP', 'call-b-command', $1,
				$1::timestamptz + interval '2 microseconds', $1
			);
		INSERT INTO human_calling_provider_receipts (
			event_id, event_type, occurred_at, received_at, signature_timestamp,
			raw_body, next_attempt_at
		)
		VALUES (
			'worker-concurrency-receipt',
			'call.unknown',
			$1,
			$1,
			0,
			convert_to(
				'{"data":{"record_type":"event","event_type":"call.unknown","id":"worker-concurrency-receipt","occurred_at":"2026-07-29T12:00:00Z","payload":{}}}',
				'UTF8'
			),
			$1
		)
	`, pgx.QueryExecModeSimpleProtocol, now); err != nil {
		t.Fatalf("seed worker concurrency fixture: %v", err)
	}
}
