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

func TestCallingStateValidatorIgnoresCallsOutsideSelectedLocationScope(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.September, 1, 13, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	identity := access.Identity{
		Subject:       "calling-state-selected-staff",
		Email:         "staff@calling-state-selected.test",
		EmailVerified: true,
	}
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "calling-state-selected-test",
		Practices: []access.PracticeProvision{{
			Key:  "calling-state-selected",
			Name: "Calling State Selected",
			Locations: []access.LocationProvision{
				{Key: "allowed", Name: "Allowed"},
				{Key: "hidden", Name: "Hidden"},
			},
			AccessGrants: []access.AccessGrantProvision{{
				Key:                  "selected-staff",
				Email:                identity.Email,
				Role:                 access.RoleStaff,
				LocationScope:        access.LocationScopeSelected,
				SelectedLocationKeys: []string{"allowed"},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision selected Calling state fixture: %v", err)
	}
	discovery, err := accessModule.DiscoverActor(context.Background(), identity)
	if err != nil {
		t.Fatalf("discover selected Calling state actor: %v", err)
	}
	if len(discovery.Practices) != 1 || len(discovery.Practices[0].Locations) != 1 {
		t.Fatalf("selected Calling discovery = %#v", discovery.Practices)
	}
	practiceID := discovery.Practices[0].ID
	allowedLocationID := discovery.Practices[0].Locations[0].ID
	var hiddenLocationID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text
		FROM access_locations
		WHERE practice_id = $1 AND name = 'Hidden'
	`, practiceID).Scan(&hiddenLocationID); err != nil {
		t.Fatalf("read hidden Location: %v", err)
	}
	var allowedCallID, hiddenCallID string
	if err := pool.QueryRow(context.Background(), `
		WITH allowed_call AS (
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point,
				created_at, updated_at
			) VALUES ($1, $2, 'INBOUND', 'STANDALONE', $4, $4)
			RETURNING id
		), hidden_call AS (
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point,
				created_at, updated_at
			) VALUES ($1, $3, 'INBOUND', 'STANDALONE', $4, $4)
			RETURNING id
		)
		SELECT allowed_call.id::text, hidden_call.id::text
		FROM allowed_call, hidden_call
	`, practiceID, allowedLocationID, hiddenLocationID, now).Scan(
		&allowedCallID, &hiddenCallID,
	); err != nil {
		t.Fatalf("seed selected Calling state Calls: %v", err)
	}
	calling := New(pool, accessModule, nil, Config{}, func() time.Time { return now })
	before, err := calling.readCallingStateETag(
		context.Background(), identity.Subject, discovery,
	)
	if err != nil {
		t.Fatalf("read selected Calling state validator: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_calls
		SET version = version + 1, updated_at = $2
		WHERE id = $1
	`, hiddenCallID, now.Add(time.Second)); err != nil {
		t.Fatalf("update hidden Call: %v", err)
	}
	afterHidden, err := calling.readCallingStateETag(
		context.Background(), identity.Subject, discovery,
	)
	if err != nil {
		t.Fatalf("read validator after hidden Call update: %v", err)
	}
	if afterHidden != before {
		t.Fatalf("hidden Location changed Calling state validator: before=%s after=%s",
			before, afterHidden)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_calls
		SET version = version + 1, updated_at = $2
		WHERE id = $1
	`, allowedCallID, now.Add(2*time.Second)); err != nil {
		t.Fatalf("update allowed Call: %v", err)
	}
	afterAllowed, err := calling.readCallingStateETag(
		context.Background(), identity.Subject, discovery,
	)
	if err != nil {
		t.Fatalf("read validator after allowed Call update: %v", err)
	}
	if afterAllowed == before {
		t.Fatal("allowed Location did not change Calling state validator")
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
