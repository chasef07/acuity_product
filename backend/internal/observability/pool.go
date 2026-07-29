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
	if errors.Is(data.Err, context.Canceled) ||
		errors.Is(data.Err, context.DeadlineExceeded) {
		outcome = PoolAcquireTimeout
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
