package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestExecutorUsesIndependentAcquireAndStatementDeadlines(t *testing.T) {
	pool := openExecutorPool(t, 1)
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
	config.MaxConns = maximum
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	// These tests measure operation/permit deadlines in milliseconds, not a
	// cold PostgreSQL connection's setup time under other integration work.
	readyContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pool.Ping(readyContext); err != nil {
		t.Fatalf("prepare test pool: %v", err)
	}
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
