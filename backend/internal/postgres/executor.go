package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Database is the PostgreSQL interface used by domain modules. Executor is the
// production adapter; pgxpool.Pool remains a useful local test adapter.
type Database interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type ExecutorConfig struct {
	AcquireTimeout   time.Duration
	OperationTimeout time.Duration
	StatementTimeout time.Duration
}

type Cause string

const (
	CauseAcquireTimeout   Cause = "acquire_timeout"
	CauseStatementTimeout Cause = "statement_timeout"
	CauseLockTimeout      Cause = "lock_timeout"
	CauseSerialization    Cause = "serialization"
	CauseDeadlock         Cause = "deadlock"
	CauseConnection       Cause = "connection"
	CauseCanceled         Cause = "canceled"
	CauseOther            Cause = "other"
)

type executionError struct {
	cause Cause
	err   error
}

func (err *executionError) Error() string {
	return "database execution failed: " + string(err.cause)
}

func (err *executionError) Unwrap() error { return err.err }

func CauseOf(err error) Cause {
	if err == nil {
		return ""
	}
	var classified *executionError
	if errors.As(err, &classified) {
		return classified.cause
	}
	if errors.Is(err, context.Canceled) {
		return CauseCanceled
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return CauseOther
	}
	switch postgresError.Code {
	case "40001":
		return CauseSerialization
	case "40P01":
		return CauseDeadlock
	case "55P03":
		return CauseLockTimeout
	case "57014":
		message := strings.ToLower(postgresError.Message)
		if strings.Contains(message, "lock timeout") {
			return CauseLockTimeout
		}
		if strings.Contains(message, "statement timeout") {
			return CauseStatementTimeout
		}
	}
	if strings.HasPrefix(postgresError.Code, "08") {
		return CauseConnection
	}
	return CauseOther
}

type acquiredConnection interface {
	Database
	Release()
}

type acquirer interface {
	Acquire(context.Context) (acquiredConnection, error)
}

type poolAcquirer struct{ pool *pgxpool.Pool }

func (pool poolAcquirer) Acquire(ctx context.Context) (acquiredConnection, error) {
	return pool.pool.Acquire(ctx)
}

// Executor owns connection acquisition and every PostgreSQL operation's
// deadlines. Domain modules still compose their business transactions using
// pgx.Tx; the wrapped transaction owns the real commit, rollback, and release.
type Executor struct {
	pool     acquirer
	config   ExecutorConfig
	observer observability.Observer
}

func NewExecutor(
	pool *pgxpool.Pool,
	config ExecutorConfig,
	observer observability.Observer,
) (*Executor, error) {
	if pool == nil {
		return nil, errors.New("database pool is required")
	}
	return newExecutor(poolAcquirer{pool: pool}, config, observer)
}

func newExecutor(
	pool acquirer,
	config ExecutorConfig,
	observer observability.Observer,
) (*Executor, error) {
	if pool == nil || config.AcquireTimeout <= 0 ||
		config.OperationTimeout <= 0 || config.StatementTimeout <= 0 {
		return nil, errors.New("positive database execution deadlines are required")
	}
	return &Executor{pool: pool, config: config, observer: observer}, nil
}

func (executor *Executor) Exec(
	ctx context.Context,
	sql string,
	arguments ...any,
) (pgconn.CommandTag, error) {
	connection, operationContext, finish, err := executor.acquire(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	defer finish()
	statementContext, cancel := executor.statementContext(operationContext)
	defer cancel()
	started := time.Now()
	tag, err := connection.Exec(statementContext, sql, arguments...)
	return tag, executor.finishOperation(err, statementContext, started)
}

func (executor *Executor) Query(
	ctx context.Context,
	sql string,
	arguments ...any,
) (pgx.Rows, error) {
	connection, operationContext, finish, err := executor.acquire(ctx)
	if err != nil {
		return nil, err
	}
	statementContext, cancel := executor.statementContext(operationContext)
	started := time.Now()
	rows, err := connection.Query(statementContext, sql, arguments...)
	if err != nil {
		cancel()
		finish()
		return nil, executor.finishOperation(err, statementContext, started)
	}
	return &ownedRows{
		Rows: rows,
		finish: func() {
			_ = executor.finishOperation(rows.Err(), statementContext, started)
			cancel()
			finish()
		},
	}, nil
}

func (executor *Executor) QueryRow(
	ctx context.Context,
	sql string,
	arguments ...any,
) pgx.Row {
	connection, operationContext, finish, err := executor.acquire(ctx)
	if err != nil {
		return errorRow{err: err}
	}
	statementContext, cancel := executor.statementContext(operationContext)
	return &ownedRow{
		row:     connection.QueryRow(statementContext, sql, arguments...),
		ctx:     statementContext,
		started: time.Now(),
		finish: func(err error, started time.Time) error {
			cancel()
			finish()
			return executor.finishOperation(err, statementContext, started)
		},
	}
}

func (executor *Executor) BeginTx(
	ctx context.Context,
	options pgx.TxOptions,
) (pgx.Tx, error) {
	connection, operationContext, finish, err := executor.acquire(ctx)
	if err != nil {
		return nil, err
	}
	statementContext, cancel := executor.statementContext(operationContext)
	started := time.Now()
	transaction, err := connection.BeginTx(statementContext, options)
	cancel()
	if err != nil {
		finish()
		return nil, executor.finishOperation(err, statementContext, started)
	}
	if _, err := transaction.Exec(
		operationContext,
		`SELECT set_config('statement_timeout', $1, true)`,
		fmt.Sprintf("%dms", max(executor.config.StatementTimeout.Milliseconds(), 1)),
	); err != nil {
		_ = transaction.Rollback(operationContext)
		finish()
		return nil, executor.finishOperation(err, operationContext, started)
	}
	executor.record("", time.Since(started))
	return &ownedTx{
		Tx:       transaction,
		executor: executor,
		ctx:      operationContext,
		finish:   finish,
	}, nil
}

func (executor *Executor) acquire(
	ctx context.Context,
) (acquiredConnection, context.Context, func(), error) {
	acquireContext, cancelAcquire := context.WithTimeout(ctx, executor.config.AcquireTimeout)
	started := time.Now()
	connection, err := executor.pool.Acquire(acquireContext)
	deadlineReached := errors.Is(acquireContext.Err(), context.DeadlineExceeded)
	cancelAcquire()
	if err != nil {
		cause := CauseOf(err)
		if deadlineReached {
			cause = CauseAcquireTimeout
		}
		executor.record(cause, time.Since(started))
		return nil, nil, nil, &executionError{cause: cause, err: err}
	}
	operationContext, cancelOperation := context.WithTimeout(ctx, executor.config.OperationTimeout)
	var once sync.Once
	finish := func() {
		once.Do(func() {
			cancelOperation()
			connection.Release()
		})
	}
	return connection, operationContext, finish, nil
}

func (executor *Executor) statementContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, executor.config.StatementTimeout)
}

func (executor *Executor) finishOperation(
	err error,
	ctx context.Context,
	started time.Time,
) error {
	if err == nil {
		executor.record("", time.Since(started))
		return nil
	}
	cause := CauseOf(err)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && cause == CauseOther {
		cause = CauseStatementTimeout
	}
	executor.record(cause, time.Since(started))
	return &executionError{cause: cause, err: err}
}

func (executor *Executor) record(cause Cause, duration time.Duration) {
	observability.Record(
		executor.observer,
		observability.DatabaseExecuted(observability.DatabaseCause(cause), duration),
	)
}

type errorRow struct{ err error }

func (row errorRow) Scan(...any) error { return row.err }

type ownedRow struct {
	row     pgx.Row
	ctx     context.Context
	started time.Time
	finish  func(error, time.Time) error
}

func (row *ownedRow) Scan(destinations ...any) error {
	return row.finish(row.row.Scan(destinations...), row.started)
}

type ownedRows struct {
	pgx.Rows
	once   sync.Once
	finish func()
}

func (rows *ownedRows) Close() {
	rows.Rows.Close()
	rows.once.Do(rows.finish)
}

func (rows *ownedRows) Next() bool {
	next := rows.Rows.Next()
	if !next {
		rows.once.Do(rows.finish)
	}
	return next
}

type ownedTx struct {
	pgx.Tx
	executor  *Executor
	ctx       context.Context
	finish    func()
	once      sync.Once
	finalized atomic.Bool
}

func (transaction *ownedTx) Begin(context.Context) (pgx.Tx, error) {
	started := time.Now()
	nested, err := transaction.Tx.Begin(transaction.ctx)
	if err != nil {
		return nil, transaction.executor.finishOperation(err, transaction.ctx, started)
	}
	transaction.executor.record("", time.Since(started))
	return &ownedTx{
		Tx: nested, executor: transaction.executor, ctx: transaction.ctx,
		finish: func() {},
	}, nil
}

func (transaction *ownedTx) Commit(context.Context) error {
	if transaction.finalized.Swap(true) {
		return transaction.Tx.Commit(transaction.ctx)
	}
	started := time.Now()
	err := transaction.Tx.Commit(transaction.ctx)
	transaction.once.Do(transaction.finish)
	return transaction.executor.finishOperation(err, transaction.ctx, started)
}

func (transaction *ownedTx) Rollback(context.Context) error {
	if transaction.finalized.Swap(true) {
		return transaction.Tx.Rollback(transaction.ctx)
	}
	started := time.Now()
	err := transaction.Tx.Rollback(transaction.ctx)
	transaction.once.Do(transaction.finish)
	return transaction.executor.finishOperation(err, transaction.ctx, started)
}

func (transaction *ownedTx) CopyFrom(
	_ context.Context,
	tableName pgx.Identifier,
	columnNames []string,
	rowSource pgx.CopyFromSource,
) (int64, error) {
	started := time.Now()
	count, err := transaction.Tx.CopyFrom(transaction.ctx, tableName, columnNames, rowSource)
	return count, transaction.executor.finishOperation(err, transaction.ctx, started)
}

func (transaction *ownedTx) Prepare(
	_ context.Context,
	name string,
	sql string,
) (*pgconn.StatementDescription, error) {
	started := time.Now()
	description, err := transaction.Tx.Prepare(transaction.ctx, name, sql)
	return description, transaction.executor.finishOperation(err, transaction.ctx, started)
}

func (transaction *ownedTx) Exec(
	_ context.Context,
	sql string,
	arguments ...any,
) (pgconn.CommandTag, error) {
	started := time.Now()
	tag, err := transaction.Tx.Exec(transaction.ctx, sql, arguments...)
	return tag, transaction.executor.finishOperation(err, transaction.ctx, started)
}

func (transaction *ownedTx) Query(
	_ context.Context,
	sql string,
	arguments ...any,
) (pgx.Rows, error) {
	started := time.Now()
	rows, err := transaction.Tx.Query(transaction.ctx, sql, arguments...)
	if err != nil {
		return nil, transaction.executor.finishOperation(err, transaction.ctx, started)
	}
	return &ownedRows{Rows: rows, finish: func() {
		transaction.executor.record(CauseOf(rows.Err()), time.Since(started))
	}}, nil
}

func (transaction *ownedTx) QueryRow(
	_ context.Context,
	sql string,
	arguments ...any,
) pgx.Row {
	return &ownedRow{
		row:     transaction.Tx.QueryRow(transaction.ctx, sql, arguments...),
		ctx:     transaction.ctx,
		started: time.Now(),
		finish: func(err error, started time.Time) error {
			return transaction.executor.finishOperation(err, transaction.ctx, started)
		},
	}
}

func (transaction *ownedTx) SendBatch(_ context.Context, batch *pgx.Batch) pgx.BatchResults {
	return transaction.Tx.SendBatch(transaction.ctx, batch)
}

var _ Database = (*Executor)(nil)
var _ pgx.Tx = (*ownedTx)(nil)
