package humancalling

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCallingStateValidatorUsesActivePracticeIndexAtProductionCardinality(t *testing.T) {
	ownerPool := testdb.Open(t)
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(ownerPool, func() time.Time { return now })
	identity := access.Identity{
		Subject:       "calling-state-capacity-staff",
		Email:         "staff@calling-state-capacity.test",
		EmailVerified: true,
	}
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "calling-state-capacity-test",
		Practices: []access.PracticeProvision{{
			Key:       "calling-state-capacity",
			Name:      "Calling State Capacity",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "staff",
				Email:         identity.Email,
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision Calling state capacity fixture: %v", err)
	}
	discovery, err := accessModule.DiscoverActor(context.Background(), identity)
	if err != nil {
		t.Fatalf("discover Calling state capacity actor: %v", err)
	}
	practiceID := discovery.Practices[0].ID
	locationID := discovery.Practices[0].Locations[0].ID
	if _, err := ownerPool.Exec(context.Background(), `
		INSERT INTO human_calling_calls (
			practice_id, location_id, direction, entry_point,
			terminal_outcome, ended_at, created_at, updated_at
		)
		SELECT $1, $2, 'INBOUND', 'STANDALONE', 'RESOLVED',
			$3::timestamptz - interval '1 day',
			$3::timestamptz - interval '1 day',
			$3::timestamptz - interval '1 day'
		FROM generate_series(1, 2447)
	`, practiceID, locationID, now); err != nil {
		t.Fatalf("seed production-cardinality historical Calls: %v", err)
	}
	if _, err := ownerPool.Exec(context.Background(), `
		INSERT INTO human_calling_calls (
			practice_id, location_id, direction, entry_point, created_at, updated_at
		)
		SELECT $1, $2, 'INBOUND', 'STANDALONE', $3, $3
		FROM generate_series(1, 3)
	`, practiceID, locationID, now); err != nil {
		t.Fatalf("seed active Calls: %v", err)
	}
	if _, err := ownerPool.Exec(context.Background(), `ANALYZE human_calling_calls`); err != nil {
		t.Fatalf("analyze production-cardinality Calls: %v", err)
	}

	tracer := &callingStateValidatorTracer{}
	config, err := pgxpool.ParseConfig(ownerPool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse Calling state validator pool: %v", err)
	}
	config.MaxConns = 1
	config.MinConns = 0
	config.ConnConfig.Tracer = tracer
	statePool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open Calling state validator pool: %v", err)
	}
	t.Cleanup(statePool.Close)
	calling := New(statePool, nil, nil, Config{}, func() time.Time { return now })
	if _, err := calling.readCallingStateETag(
		context.Background(),
		identity.Subject,
		discovery,
	); err != nil {
		t.Fatalf("read production-cardinality Calling state validator: %v", err)
	}
	query, arguments, ok := tracer.Captured()
	if !ok {
		t.Fatal("Calling state validator SQL was not captured")
	}

	rows, err := ownerPool.Query(
		context.Background(),
		"EXPLAIN (COSTS OFF) "+query,
		arguments...,
	)
	if err != nil {
		t.Fatalf("explain Calling state validator: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan Calling state validator plan: %v", err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate Calling state validator plan: %v", err)
	}
	if !strings.Contains(plan.String(), "human_calling_state_active_practice_idx") {
		t.Fatalf("Calling state validator lost its active-Practice index:\n%s", plan.String())
	}
}

type callingStateValidatorTracer struct {
	query     string
	arguments []any
}

func (tracer *callingStateValidatorTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	if strings.Contains(data.SQL, "WITH relevant_call_ids AS MATERIALIZED") {
		tracer.query = data.SQL
		tracer.arguments = append([]any(nil), data.Args...)
	}
	return ctx
}

func (*callingStateValidatorTracer) TraceQueryEnd(
	context.Context,
	*pgx.Conn,
	pgx.TraceQueryEndData,
) {
}

func (tracer *callingStateValidatorTracer) Captured() (string, []any, bool) {
	return tracer.query, append([]any(nil), tracer.arguments...), tracer.query != ""
}
