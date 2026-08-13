package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/observability"
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

func TestExecutorClassifiesTimeoutWhileReadingRows(t *testing.T) {
	var output bytes.Buffer
	pool := &scriptedPool{
		query: func(ctx context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return &deadlineRows{ctx: ctx}, nil
		},
	}
	executor, err := newExecutor(scriptedAcquirer{connection: pool}, ExecutorConfig{
		AcquireTimeout:   10 * time.Millisecond,
		OperationTimeout: 100 * time.Millisecond,
		StatementTimeout: 25 * time.Millisecond,
	}, observability.NewLogger(
		observability.RuntimePortalAPI,
		"portal-api-test",
		slog.New(slog.NewJSONHandler(&output, nil)),
	))
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	rows, err := executor.Query(context.Background(), "SELECT pg_sleep(1)")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if rows.Next() {
		t.Fatal("deadline rows returned a result")
	}
	if !errors.Is(rows.Err(), context.DeadlineExceeded) {
		t.Fatalf("rows error = %v, want deadline exceeded", rows.Err())
	}

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
		t.Fatalf("decode database execution metric: %v", err)
	}
	if entry["cause"] != string(CauseStatementTimeout) {
		t.Fatalf("row iteration cause = %q, want %q", entry["cause"], CauseStatementTimeout)
	}
}

type scriptedPool struct {
	queryRow func(context.Context, string, ...any) pgx.Row
	query    func(context.Context, string, ...any) (pgx.Rows, error)
}

func (pool *scriptedPool) Release() {}

type scriptedAcquirer struct{ connection acquiredConnection }

func (acquirer scriptedAcquirer) Acquire(context.Context) (acquiredConnection, error) {
	return acquirer.connection, nil
}

func (pool *scriptedPool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (pool *scriptedPool) Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error) {
	if pool.query != nil {
		return pool.query(ctx, sql, arguments...)
	}
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

type deadlineRows struct {
	ctx context.Context
	err error
}

func (rows *deadlineRows) Close()                                       {}
func (rows *deadlineRows) Err() error                                   { return rows.err }
func (rows *deadlineRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (rows *deadlineRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (rows *deadlineRows) Scan(...any) error                            { return errors.New("no current row") }
func (rows *deadlineRows) Values() ([]any, error)                       { return nil, errors.New("no current row") }
func (rows *deadlineRows) RawValues() [][]byte                          { return nil }
func (rows *deadlineRows) Conn() *pgx.Conn                              { return nil }

func (rows *deadlineRows) Next() bool {
	<-rows.ctx.Done()
	rows.err = rows.ctx.Err()
	return false
}
