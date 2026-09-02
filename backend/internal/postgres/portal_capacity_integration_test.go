package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/admission"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/jackc/pgx/v5"
)

func TestPortalCapacityFollowsRowsAndTransactionLifetimes(t *testing.T) {
	pool := openExecutorPool(t, 4)
	executor, err := productpostgres.NewPortalExecutor(pool, productpostgres.ExecutorConfig{
		AcquireTimeout: 50 * time.Millisecond, OperationTimeout: time.Second, StatementTimeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	row := executor.QueryRow(ctx, "SELECT 1")
	transaction, err := executor.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := executor.Exec(ctx, "SELECT 1"); productpostgres.CauseOf(err) != productpostgres.CauseAdmissionRejected {
		t.Fatalf("nested background acquisition = %v, want admission rejection", err)
	}
	syncContext := admission.WithClass(ctx, admission.CallingSync)
	rows, err := executor.Query(syncContext, "SELECT generate_series(1, 2)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if _, err := executor.Exec(syncContext, "SELECT 1"); productpostgres.CauseOf(err) != productpostgres.CauseAdmissionRejected {
		t.Fatalf("excess Calling sync acquisition = %v, want admission rejection", err)
	}
	controlContext := admission.WithClass(ctx, admission.CallingControl)
	if _, err := executor.Exec(controlContext, "SELECT 1"); err != nil {
		t.Fatalf("live command lost reserved capacity: %v", err)
	}
	if acquired := pool.Stat().AcquiredConns(); acquired != 3 {
		t.Fatalf("held connections = %d, want background row + transaction + Calling sync rows", acquired)
	}
	var value int
	if err := row.Scan(&value); err != nil || value != 1 {
		t.Fatalf("consume background row: value=%d err=%v", value, err)
	}
	if _, err := executor.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatalf("consumed row did not release background capacity: %v", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("completed work retained %d connections", acquired)
	}

	canceled, cancel := context.WithCancel(ctx)
	active, err := executor.BeginTx(canceled, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = active.Rollback(ctx)
	if _, err := executor.Exec(canceled, "SELECT 1"); productpostgres.CauseOf(err) != productpostgres.CauseCanceled {
		t.Fatalf("canceled acquisition cause = %v", err)
	}
	first, err := executor.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(ctx) }()
	second, err := executor.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("cancellation leaked background capacity: %v", err)
	}
	_ = second.Rollback(ctx)
}

func TestPortalExecutorRejectsPoolsWithoutSeparateCallingHeadroom(t *testing.T) {
	for _, maximum := range []int32{1, 2} {
		pool := openExecutorPool(t, maximum)
		config := productpostgres.ExecutorConfig{
			AcquireTimeout: time.Second, OperationTimeout: time.Second, StatementTimeout: time.Second,
		}
		if _, err := productpostgres.NewPortalExecutor(pool, config, nil); err == nil {
			t.Fatalf("portal pool with %d connections accepted without both reservations", maximum)
		}
		if _, err := productpostgres.NewExecutor(pool, config, nil); err != nil {
			t.Fatalf("default runtime pool with %d connections changed behavior: %v", maximum, err)
		}
	}
}

func TestQueryRowTimingIncludesDatabaseWaitBeforeScan(t *testing.T) {
	for _, inTransaction := range []bool{false, true} {
		name := "executor"
		if inTransaction {
			name = "transaction"
		}
		t.Run(name, func(t *testing.T) {
			pool := openExecutorPool(t, 1)
			var output bytes.Buffer
			executor, err := productpostgres.NewExecutor(pool, productpostgres.ExecutorConfig{
				AcquireTimeout: time.Second, OperationTimeout: time.Second, StatementTimeout: 40 * time.Millisecond,
			}, observability.NewLogger(observability.RuntimePortalAPI, "timing-test",
				slog.New(slog.NewJSONHandler(&output, nil))))
			if err != nil {
				t.Fatal(err)
			}
			var query interface {
				QueryRow(context.Context, string, ...any) pgx.Row
			} = executor
			if inTransaction {
				transaction, err := executor.BeginTx(context.Background(), pgx.TxOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = transaction.Rollback(context.Background()) }()
				query = transaction
			}
			var value int
			err = query.QueryRow(context.Background(), "SELECT 1 FROM pg_sleep(0.25)").Scan(&value)
			if productpostgres.CauseOf(err) != productpostgres.CauseStatementTimeout {
				t.Fatalf("slow QueryRow cause = %v, want statement timeout", err)
			}
			var event struct {
				Cause   string  `json:"cause"`
				Seconds float64 `json:"seconds"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event); err != nil {
				t.Fatal(err)
			}
			if event.Cause != "statement_timeout" || event.Seconds < 0.03 {
				t.Fatalf("QueryRow omitted database wait: cause=%s seconds=%.6f", event.Cause, event.Seconds)
			}
			t.Logf("QueryRow timeout recorded %.3f seconds", event.Seconds)
		})
	}
}
