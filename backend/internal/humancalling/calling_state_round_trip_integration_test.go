package humancalling_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConditionalCallingStateReadUsesThreeDatabaseRoundTrips(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 22, 16, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	trips := &roundTripCounter{}
	config := db.As(testdb.Portal).Config()
	config.MaxConns = 1
	config.ConnConfig.Tracer = trips
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	database, err := productpostgres.NewExecutor(pool, productpostgres.ExecutorConfig{
		AcquireTimeout: time.Second, OperationTimeout: 5 * time.Second,
		StatementTimeout: 2 * time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	accessModule := access.New(database, clock)
	_, identities := provisionConcurrentStaff(t, db.Owner, accessModule, now, "round-trips", 1)
	staff := identities[0]
	calling := humancalling.New(database, accessModule, nil, humancalling.Config{}, clock)
	readyConcurrentStaff(t, calling, identities, "round-trips-session")

	state, _, err := calling.ReadCallingStateConditionally(ctx, staff, "")
	if err != nil || !state.Softphone.Owner {
		t.Fatalf("warm Calling state read: owner=%t err=%v", state.Softphone.Owner, err)
	}
	trips.count.Store(0)
	if _, err := accessModule.DiscoverActorReadOnly(ctx, staff); err != nil {
		t.Fatal(err)
	}
	discovery := trips.count.Swap(0)
	_, notModified, err := calling.ReadCallingStateConditionally(ctx, staff, state.ETag)
	if err != nil || !notModified {
		t.Fatalf("conditional Calling state read: notModified=%t err=%v", notModified, err)
	}
	read := trips.count.Load() - discovery
	t.Logf("conditional Calling state read: %d round trips for access discovery, %d after it", discovery, read)
	if read != 3 {
		t.Fatalf("conditional Calling state read made %d database round trips after access discovery, want 3", read)
	}
}

type roundTripCounter struct {
	count atomic.Int64
}

func (counter *roundTripCounter) TraceQueryStart(
	ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData,
) context.Context {
	counter.count.Add(1)
	return ctx
}

func (*roundTripCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (counter *roundTripCounter) TraceBatchStart(
	ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData,
) context.Context {
	counter.count.Add(1)
	return ctx
}

func (*roundTripCounter) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}

func (*roundTripCounter) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}
