package humancalling_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReconcileStaleCallsDoesNotStarveRealtimeCommandsAtProductionCardinality(
	t *testing.T,
) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(
		t, accessModule, now, "reconciliation-capacity", 1,
	)

	if _, err := pool.Exec(context.Background(), `
		WITH inserted_calls AS (
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point,
				created_at, updated_at
			)
			SELECT $1, $2, 'INBOUND', 'STANDALONE',
				$3::timestamptz - interval '2 hours',
				$3::timestamptz - interval '2 hours'
			FROM generate_series(1, 2447)
			RETURNING id
		), inserted_legs AS (
			INSERT INTO human_calling_call_legs (
				call_id, role, sequence, state, provider_connection_id,
				provider_call_control_id, provider_call_leg_id,
				provider_call_session_id, created_at, updated_at
			)
			SELECT id, 'CALLER', 1, 'RINGING', 'scale-connection',
				'scale-control-' || row_number() OVER (),
				'scale-leg-' || row_number() OVER (),
				'scale-session-' || row_number() OVER (),
				$3::timestamptz - interval '2 hours',
				$3::timestamptz - interval '2 hours'
			FROM inserted_calls
			RETURNING id, call_id
		), numbered AS (
			SELECT id, call_id, row_number() OVER () AS leg_number
			FROM inserted_legs
		)
		INSERT INTO human_calling_provider_commands (
			call_id, call_leg_id, action, target_id, payload, state,
			created_at, updated_at
		)
		SELECT call_id, id, 'HANGUP_LEG',
			'scale-target-' || leg_number || '-' || ordinal,
			jsonb_build_object('connection_id', 'scale-connection'),
			CASE WHEN ordinal = 1 THEN 'SENDING' ELSE 'SENT' END,
			$3::timestamptz - interval '2 hours' + ordinal * interval '1 millisecond',
			$3::timestamptz - interval '2 hours'
		FROM numbered
		CROSS JOIN LATERAL generate_series(
			1,
			CASE WHEN leg_number <= 668 THEN 3 ELSE 2 END
		) ordinal
	`, authorization.Practice.ID, authorization.Locations[0].ID, now); err != nil {
		t.Fatalf("seed production-sized stale CallLegs: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		WITH realtime_calls AS (
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point,
				created_at, updated_at
			)
			SELECT $1, $2, 'INBOUND', 'STANDALONE', $3, $3
			FROM generate_series(1, 3)
			RETURNING id
		), realtime_legs AS (
			INSERT INTO human_calling_call_legs (
				call_id, role, sequence, state, provider_connection_id,
				provider_call_control_id, provider_call_leg_id,
				provider_call_session_id, created_at, updated_at
			)
			SELECT id, 'CALLER', 1, 'RINGING', 'realtime-connection',
				'realtime-control-' || row_number() OVER (),
				'realtime-leg-' || row_number() OVER (),
				'realtime-session-' || row_number() OVER (), $3, $3
			FROM realtime_calls
			RETURNING id, call_id
		), numbered AS (
			SELECT id, call_id, row_number() OVER () AS command_number
			FROM realtime_legs
		)
		INSERT INTO human_calling_provider_commands (
			call_id, call_leg_id, action, target_id, payload,
			created_at, next_attempt_at
		)
		SELECT call_id, id,
			(ARRAY['ANSWER_CALLER', 'BRIDGE', 'HANGUP_LEG'])[command_number],
			(ARRAY['realtime-answer', 'realtime-bridge', 'realtime-hangup'])[command_number],
			'{}'::jsonb, $3 + command_number * interval '1 millisecond', $3
		FROM numbered
	`, authorization.Practice.ID, authorization.Locations[0].ID, now); err != nil {
		t.Fatalf("seed realtime provider commands: %v", err)
	}
	var callLegs, commands int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM human_calling_call_legs),
			(SELECT count(*) FROM human_calling_provider_commands)
	`).Scan(&callLegs, &commands); err != nil {
		t.Fatal(err)
	}
	if callLegs != 2450 || commands != 5565 {
		t.Fatalf("production shape = %d CallLegs/%d commands, want 2450/5565",
			callLegs, commands)
	}
	for _, relation := range []string{
		"human_calling_calls",
		"human_calling_call_legs",
		"human_calling_provider_commands",
	} {
		if _, err := pool.Exec(context.Background(), "ANALYZE "+relation); err != nil {
			t.Fatalf("analyze %s: %v", relation, err)
		}
	}
	planRows, err := pool.Query(
		context.Background(),
		"EXPLAIN (COSTS OFF) "+humancalling.StaleCallLegCandidateQueryForTest,
		now,
	)
	if err != nil {
		t.Fatalf("explain stale CallLeg reconciliation: %v", err)
	}
	var plan strings.Builder
	for planRows.Next() {
		var line string
		if err := planRows.Scan(&line); err != nil {
			planRows.Close()
			t.Fatal(err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err := planRows.Err(); err != nil {
		planRows.Close()
		t.Fatal(err)
	}
	planRows.Close()
	if !strings.Contains(plan.String(), "human_calling_stale_leg_commands_idx") ||
		strings.Contains(plan.String(), "Seq Scan on human_calling_provider_commands pending") {
		t.Fatalf("stale CallLeg reconciliation lost its indexed lateral lookup:\n%s", plan.String())
	}

	config, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	queryStarted := make(chan struct{})
	config.MaxConns = 2
	config.ConnConfig.Tracer = &reconciliationQueryTracer{started: queryStarted}
	workerPool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workerPool.Close)
	provider := &recordingProvider{}
	calling := humancalling.New(
		workerPool, nil, provider, humancalling.Config{}, func() time.Time { return now },
	)

	reconcileDone := make(chan error, 1)
	go func() {
		_, err := calling.ReconcileStaleCalls(context.Background())
		reconcileDone <- err
	}()
	select {
	case <-queryStarted:
	case <-time.After(time.Second):
		t.Fatal("stale CallLeg query did not start")
	}

	progressContext, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	errors := make(chan error, 3)
	var progress sync.WaitGroup
	for range 3 {
		progress.Add(1)
		go func() {
			defer progress.Done()
			processed, err := calling.ProcessNextCommand(progressContext)
			if err == nil && !processed {
				err = fmt.Errorf("no realtime command claimed")
			}
			errors <- err
		}()
	}
	progress.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Errorf("realtime answer/bridge/hangup progress was starved: %v", err)
		}
	}
	if err := <-reconcileDone; err != nil {
		t.Fatalf("reconcile production-sized stale CallLegs: %v", err)
	}
	var sent int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM human_calling_provider_commands
		WHERE target_id IN ('realtime-answer', 'realtime-bridge', 'realtime-hangup')
			AND state = 'SENT'
	`).Scan(&sent); err != nil {
		t.Fatal(err)
	}
	if sent != 3 {
		t.Fatalf("realtime commands sent = %d, want 3", sent)
	}
	var unbound int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM human_calling_provider_commands
		WHERE target_id IN ('realtime-answer', 'realtime-bridge', 'realtime-hangup')
			AND (call_id IS NULL OR call_leg_id IS NULL)
	`).Scan(&unbound); err != nil {
		t.Fatal(err)
	}
	if unbound != 0 {
		t.Fatalf("realtime commands without Call/CallLeg ownership = %d", unbound)
	}
}

type reconciliationQueryTracer struct {
	once    sync.Once
	started chan struct{}
}

func (tracer *reconciliationQueryTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	if strings.Contains(data.SQL, "LEFT JOIN LATERAL") {
		tracer.once.Do(func() { close(tracer.started) })
	}
	return ctx
}

func (*reconciliationQueryTracer) TraceQueryEnd(
	context.Context,
	*pgx.Conn,
	pgx.TraceQueryEndData,
) {
}
