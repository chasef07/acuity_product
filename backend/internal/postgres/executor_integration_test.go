package postgres_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestExecutorUsesIndependentAcquireAndStatementDeadlines(t *testing.T) {
	pool := openExecutorPool(t, 1)
	// Measure acquisition from a ready pool, not scheduler-dependent initial
	// connection setup, which is outside this deadline-separation scenario.
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	executor := newTestExecutor(t, pool, 20*time.Millisecond, 250*time.Millisecond, 100*time.Millisecond)

	started := time.Now()
	var value int
	if err := executor.QueryRow(
		context.Background(),
		`SELECT 7 FROM pg_sleep(0.05)`,
	).Scan(&value); err != nil {
		t.Fatalf("query longer than acquisition deadline: %v", err)
	}
	if value != 7 || time.Since(started) < 45*time.Millisecond {
		t.Fatalf("query value=%d elapsed=%s", value, time.Since(started))
	}

	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("occupy pool: %v", err)
	}
	defer connection.Release()
	_, err = executor.Exec(context.Background(), `SELECT 1`)
	if productpostgres.CauseOf(err) != productpostgres.CauseAcquireTimeout {
		t.Fatalf("acquisition cause = %q, want %q", productpostgres.CauseOf(err), productpostgres.CauseAcquireTimeout)
	}
}

func TestExecutorOwnsTransactionDeadlineAndRelease(t *testing.T) {
	pool := openExecutorPool(t, 1)
	executor := newTestExecutor(t, pool, 20*time.Millisecond, 80*time.Millisecond, 60*time.Millisecond)
	if _, err := executor.Exec(context.Background(), `CREATE TEMP TABLE executor_probe (value integer)`); err != nil {
		t.Fatalf("create probe: %v", err)
	}

	tx, err := executor.BeginTx(context.Background(), pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(context.Background(), `INSERT INTO executor_probe VALUES (1)`); err != nil {
		t.Fatalf("insert probe: %v", err)
	}
	if _, err := tx.Exec(context.Background(), `SELECT pg_sleep(0.2)`); productpostgres.CauseOf(err) != productpostgres.CauseStatementTimeout {
		t.Fatalf("transaction statement cause = %q, want %q", productpostgres.CauseOf(err), productpostgres.CauseStatementTimeout)
	}
	if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("rollback transaction: %v", err)
	}

	var count int
	if err := executor.QueryRow(context.Background(), `SELECT count(*) FROM executor_probe`).Scan(&count); err != nil {
		t.Fatalf("query rollback result: %v", err)
	}
	if count != 0 {
		t.Fatalf("rolled-back row count = %d, want 0", count)
	}
}

func openExecutorPool(t *testing.T, maximum int32) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	if !strings.HasSuffix(config.ConnConfig.Database, "_test") {
		t.Fatal("TEST_DATABASE_URL must name a disposable database ending in _test")
	}
	config.MaxConns = maximum
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newTestExecutor(
	t *testing.T,
	pool *pgxpool.Pool,
	acquire time.Duration,
	operation time.Duration,
	statement time.Duration,
) *productpostgres.Executor {
	t.Helper()
	executor, err := productpostgres.NewExecutor(pool, productpostgres.ExecutorConfig{
		AcquireTimeout: acquire, OperationTimeout: operation, StatementTimeout: statement,
	}, nil)
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	return executor
}

// Terminate only the connection owned by this test. A failed transaction must
// stay failed, while its discarded connection must not poison later work.
func TestExecutorRecoversPoolAfterConnectionLossWithoutReplayingTransaction(t *testing.T) {
	pool := openExecutorPool(t, 1)
	executor := newTestExecutor(t, pool, time.Second, 5*time.Second, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := executor.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var originalPID uint32
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&originalPID); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	var terminated bool
	if err := admin.QueryRow(ctx, "SELECT pg_terminate_backend($1)", originalPID).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate synthetic connection: terminated:%t err:%v", terminated, err)
	}
	if _, err := tx.Exec(ctx, "SELECT 1"); productpostgres.CauseOf(err) != productpostgres.CauseConnection {
		t.Fatalf("lost connection cause = %q, err = %v", productpostgres.CauseOf(err), err)
	}
	_ = tx.Rollback(ctx)
	var nextPID uint32
	if err := executor.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&nextPID); err != nil {
		t.Fatal(err)
	}
	if originalPID == nextPID {
		t.Fatal("pool reused terminated connection")
	}
}
