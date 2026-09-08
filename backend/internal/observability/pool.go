package observability

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolTracer records acquisition pressure without inspecting SQL or arguments.
// It implements pgx.QueryTracer only because pgx uses that interface as the
// configuration slot for optional pool tracers.
type PoolTracer struct {
	observer Observer
}

// PoolAcquireTimeoutCause distinguishes the pool acquisition budget from the
// enclosing operation deadline. The database executor sets this timeout cause.
var PoolAcquireTimeoutCause = errors.New("database pool acquisition deadline exceeded")

type acquireStartedAt struct{}

func NewPoolTracer(observer Observer) *PoolTracer {
	return &PoolTracer{observer: observer}
}

func (tracer *PoolTracer) TraceAcquireStart(
	ctx context.Context,
	_ *pgxpool.Pool,
	_ pgxpool.TraceAcquireStartData,
) context.Context {
	return context.WithValue(ctx, acquireStartedAt{}, time.Now())
}

func (tracer *PoolTracer) TraceAcquireEnd(
	ctx context.Context,
	_ *pgxpool.Pool,
	data pgxpool.TraceAcquireEndData,
) {
	startedAt, ok := ctx.Value(acquireStartedAt{}).(time.Time)
	if !ok {
		startedAt = time.Now()
	}
	outcome := PoolAcquireSucceeded
	if data.Err != nil && (errors.Is(data.Err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		outcome = PoolAcquireOperationTimeout
		if errors.Is(context.Cause(ctx), PoolAcquireTimeoutCause) {
			outcome = PoolAcquireTimeout
		}
	} else if errors.Is(data.Err, context.Canceled) {
		outcome = PoolAcquireCanceled
	} else if data.Err != nil {
		outcome = PoolAcquireFailed
	}
	Record(
		tracer.observer,
		DatabasePoolAcquired(outcome, time.Since(startedAt)),
	)
}

func (*PoolTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	_ pgx.TraceQueryStartData,
) context.Context {
	return ctx
}

func (*PoolTracer) TraceQueryEnd(
	context.Context,
	*pgx.Conn,
	pgx.TraceQueryEndData,
) {
}

var _ pgx.QueryTracer = (*PoolTracer)(nil)
var _ pgxpool.AcquireTracer = (*PoolTracer)(nil)
