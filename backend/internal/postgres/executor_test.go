package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestExecutorKeepsAcquireAndStatementDeadlinesDistinct(t *testing.T) {
	pool := &scriptedPool{
		queryRow: func(ctx context.Context, _ string, _ ...any) pgx.Row {
			<-ctx.Done()
			return scriptedErrorRow{err: ctx.Err()}
		},
	}
	executor, err := newExecutor(scriptedAcquirer{connection: pool}, ExecutorConfig{
		AcquireTimeout:   10 * time.Millisecond,
		OperationTimeout: 100 * time.Millisecond,
		StatementTimeout: 25 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	started := time.Now()
	err = executor.QueryRow(context.Background(), "SELECT pg_sleep(1)").Scan(new(int))
	elapsed := time.Since(started)
	if CauseOf(err) != CauseStatementTimeout {
		t.Fatalf("statement cause = %q, want %q", CauseOf(err), CauseStatementTimeout)
	}
	if elapsed < 20*time.Millisecond {
		t.Fatalf("statement ended after %s, acquisition timeout leaked into operation", elapsed)
	}
}

func TestExecutorClassifiesOnlySupportedPostgresCauses(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Cause
	}{
		{name: "serialization", err: &pgconn.PgError{Code: "40001"}, want: CauseSerialization},
		{name: "deadlock", err: &pgconn.PgError{Code: "40P01"}, want: CauseDeadlock},
		{name: "lock timeout", err: &pgconn.PgError{Code: "55P03"}, want: CauseLockTimeout},
		{name: "unknown", err: errors.New("patient@example.test"), want: CauseOther},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CauseOf(test.err); got != test.want {
				t.Fatalf("cause = %q, want %q", got, test.want)
			}
		})
	}
}

type scriptedPool struct {
	queryRow func(context.Context, string, ...any) pgx.Row
}

func (pool *scriptedPool) Release() {}

type scriptedAcquirer struct{ connection acquiredConnection }

func (acquirer scriptedAcquirer) Acquire(context.Context) (acquiredConnection, error) {
	return acquirer.connection, nil
}

func (pool *scriptedPool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (pool *scriptedPool) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}

func (pool *scriptedPool) QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row {
	return pool.queryRow(ctx, sql, arguments...)
}

func (pool *scriptedPool) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}

type scriptedErrorRow struct{ err error }

func (row scriptedErrorRow) Scan(...any) error { return row.err }
