package humancalling_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOutgoingCallLegMaintenanceDoesNotStarveRealtimeCommandsAtProductionCardinality(
	t *testing.T,
) {
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(
		t, db.Owner, accessModule, now, "reconciliation-capacity", 1,
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
	config := db.As(testdb.Worker).Config()
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
		_, err := calling.MaintainOutgoingCallLegs(context.Background())
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

func TestStaleCallLegCandidateQueryDoesNotScaleWithEndedCallHistory(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Date(2026, time.October, 25, 3, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(
		t, db.Owner, accessModule, now, "stale-leg-history", 1,
	)
	if _, err := pool.Exec(ctx, `
		WITH inserted_calls AS (
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point,
				terminal_outcome, ended_at, created_at, updated_at
			)
			SELECT $1, $2, 'INBOUND', 'STANDALONE',
				CASE WHEN n % 5 = 0 THEN 'VOICEMAIL' ELSE 'RESOLVED' END,
				$3::timestamptz - interval '1 day',
				$3::timestamptz - interval '2 days', $3::timestamptz - interval '1 day'
			FROM generate_series(1, 4000) n
			RETURNING id
		), inserted_legs AS (
			INSERT INTO human_calling_call_legs (
				call_id, role, sequence, staff_subject, state,
				provider_call_control_id, provider_call_leg_id,
				ended_at, created_at, updated_at
			)
			SELECT id, CASE WHEN ordinal = 1 THEN 'CALLER' ELSE 'STAFF' END,
				ordinal, CASE WHEN ordinal > 1 THEN 'history-staff' END, 'ENDED',
				'history-control-' || id || '-' || ordinal,
				'history-leg-' || id || '-' || ordinal,
				$3::timestamptz - interval '1 day',
				$3::timestamptz - interval '2 days', $3::timestamptz - interval '1 day'
			FROM inserted_calls
			CROSS JOIN generate_series(1, 2) ordinal
			RETURNING id, call_id, role
		)
		INSERT INTO human_calling_provider_commands (
			call_id, call_leg_id, action, target_id, payload, state,
			created_at, updated_at
		)
		SELECT call_id, id, action, 'history-target', '{}'::jsonb, state,
			$3::timestamptz - interval '2 days', $3::timestamptz - interval '1 day'
		FROM inserted_legs
		CROSS JOIN LATERAL (VALUES
			(CASE WHEN role = 'CALLER' THEN 'ANSWER_CALLER' ELSE 'DIAL_STAFF' END, 'RECONCILED'),
			('START_RING_WINDOW', 'SENT'),
			('STOP_RING_WINDOW', 'RECONCILED'),
			('HANGUP_LEG', CASE WHEN role = 'CALLER' THEN 'FAILED' ELSE 'RECONCILED' END)
		) command(action, state)
	`, authorization.Practice.ID, authorization.Locations[0].ID, now); err != nil {
		t.Fatalf("seed ended call history: %v", err)
	}
	for _, relation := range []string{
		"human_calling_calls",
		"human_calling_call_legs",
		"human_calling_provider_commands",
		"human_calling_timeline",
	} {
		if _, err := pool.Exec(ctx, "ANALYZE "+relation); err != nil {
			t.Fatalf("analyze %s: %v", relation, err)
		}
	}

	var raw []byte
	if err := pool.QueryRow(ctx,
		"EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+humancalling.StaleCallLegCandidateQuery, now,
	).Scan(&raw); err != nil {
		t.Fatalf("explain stale CallLeg candidate query: %v", err)
	}
	var plans []struct {
		ExecutionTime float64 `json:"Execution Time"`
		Plan          struct {
			ActualRows float64 `json:"Actual Rows"`
			HitBlocks  float64 `json:"Shared Hit Blocks"`
			ReadBlocks float64 `json:"Shared Read Blocks"`
		}
	}
	if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
		t.Fatalf("decode stale CallLeg candidate plan: %v", err)
	}
	plan := plans[0]
	t.Logf("8,000 ended CallLegs: %.3fms, %.0f buffers",
		plan.ExecutionTime, plan.Plan.HitBlocks+plan.Plan.ReadBlocks)
	if plan.Plan.ActualRows != 0 {
		t.Fatalf("ended call history produced %.0f stale CallLeg candidates", plan.Plan.ActualRows)
	}
	if blocks := plan.Plan.HitBlocks + plan.Plan.ReadBlocks; blocks > 20 {
		t.Fatalf("stale CallLeg candidate query read %.0f buffers over ended history, want at most 20: %s",
			blocks, raw)
	}
}

func TestStaleCallLegCandidatesOnTerminalCalls(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Date(2026, time.October, 25, 3, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(
		t, db.Owner, accessModule, now, "terminal-stale-leg", 1,
	)
	seed := func(name, legState, commandAction, commandState string) string {
		t.Helper()
		var legID string
		if err := pool.QueryRow(ctx, `
			WITH inserted_call AS (
				INSERT INTO human_calling_calls (
					practice_id, location_id, direction, entry_point,
					terminal_outcome, ended_at, created_at, updated_at
				) VALUES ($1, $2, 'INBOUND', 'STANDALONE', 'RESOLVED',
					$3::timestamptz - interval '5 minutes',
					$3::timestamptz - interval '10 minutes',
					$3::timestamptz - interval '5 minutes')
				RETURNING id
			), inserted_leg AS (
				INSERT INTO human_calling_call_legs (
					call_id, role, sequence, state, provider_connection_id,
					provider_call_control_id, provider_call_leg_id,
					provider_call_session_id, ended_at, created_at, updated_at
				)
				SELECT id, 'CALLER', 1, $4, 'terminal-connection',
					$5 || '-control', $5 || '-leg', $5 || '-session',
					CASE WHEN $4 IN ('ENDED', 'FAILED')
						THEN $3::timestamptz - interval '5 minutes' END,
					$3::timestamptz - interval '10 minutes',
					$3::timestamptz - interval '5 minutes'
				FROM inserted_call
				RETURNING id, call_id
			), inserted_command AS (
				INSERT INTO human_calling_provider_commands (
					call_id, call_leg_id, action, target_id, payload, state,
					created_at, updated_at
				)
				SELECT call_id, id, $6, $5 || '-control',
					jsonb_build_object('connection_id', 'terminal-connection'), $7,
					$3::timestamptz - interval '5 minutes',
					$3::timestamptz - interval '5 minutes'
				FROM inserted_leg
				WHERE $6 <> ''
			)
			SELECT id::text FROM inserted_leg
		`, authorization.Practice.ID, authorization.Locations[0].ID, now,
			legState, name, commandAction, commandState,
		).Scan(&legID); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		return legID
	}
	ending := seed("ending", "ENDING", "", "")
	hangup := seed("hangup", "ENDED", "HANGUP_LEG", "SENT")
	seed("bridged", "BRIDGED", "", "")
	seed("ring-window", "ENDED", "START_RING_WINDOW", "SENT")
	seed("reconciled-hangup", "ENDED", "HANGUP_LEG", "RECONCILED")

	var candidates []string
	for range 5 {
		rows, err := pool.Query(ctx, humancalling.StaleCallLegCandidateQuery, now)
		if err != nil {
			t.Fatal(err)
		}
		var legID string
		for rows.Next() {
			values, err := rows.Values()
			if err != nil {
				t.Fatal(err)
			}
			legID = values[2].(string)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if legID == "" {
			break
		}
		candidates = append(candidates, legID)
		if _, err := pool.Exec(ctx, `
			UPDATE human_calling_call_legs
			SET reconciliation_checked_at = $2,
				reconciliation_next_attempt_at = $2::timestamptz + interval '60 seconds'
			WHERE id = $1
		`, legID, now); err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(candidates)
	want := []string{ending, hangup}
	slices.Sort(want)
	if !slices.Equal(candidates, want) {
		t.Fatalf("terminal-call stale CallLeg candidates = %v, want ending %s and hangup %s",
			candidates, ending, hangup)
	}
}

func TestOutgoingCallLegMaintenanceRecoversOneInterruptedCommandPerTick(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(
		t, db.Owner, accessModule, now, "bounded-interrupted-recovery", 1,
	)
	if _, err := pool.Exec(context.Background(), `
		WITH inserted_calls AS (
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point,
				created_at, updated_at
			)
			SELECT $1, $2, 'INBOUND', 'STANDALONE', $3, $3
			FROM generate_series(1, 2)
			RETURNING id
		), inserted_legs AS (
			INSERT INTO human_calling_call_legs (
				call_id, role, sequence, state, provider_connection_id,
				provider_call_control_id, provider_call_leg_id,
				provider_call_session_id, created_at, updated_at
			)
			SELECT id, 'CALLER', 1, 'RINGING', 'bounded-connection',
				'bounded-control-' || row_number() OVER (),
				'bounded-leg-' || row_number() OVER (),
				'bounded-session-' || row_number() OVER (), $3, $3
			FROM inserted_calls
			RETURNING id, call_id
		)
		INSERT INTO human_calling_provider_commands (
			call_id, call_leg_id, action, target_id, payload, state,
			created_at, updated_at
		)
		SELECT call_id, id, 'HANGUP_LEG',
			'bounded-target-' || row_number() OVER (),
			jsonb_build_object('connection_id', 'bounded-connection'),
			'SENDING', $3, $3::timestamptz - interval '31 seconds'
		FROM inserted_legs
	`, authorization.Practice.ID, authorization.Locations[0].ID, now); err != nil {
		t.Fatalf("seed interrupted provider commands: %v", err)
	}
	calling := humancalling.New(
		db.As(testdb.Worker), accessModule, &recordingProvider{}, humancalling.Config{},
		func() time.Time { return now },
	)

	for tick, wantPending := range []int{1, 2} {
		maintained, err := calling.MaintainOutgoingCallLegs(context.Background())
		if err != nil || !maintained {
			t.Fatalf("maintenance tick %d = maintained:%t err:%v", tick+1, maintained, err)
		}
		var pending, sending int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FILTER (WHERE state = 'PENDING'),
				count(*) FILTER (WHERE state = 'SENDING')
			FROM human_calling_provider_commands
			WHERE target_id LIKE 'bounded-target-%'
		`).Scan(&pending, &sending); err != nil {
			t.Fatal(err)
		}
		if pending != wantPending || sending != 2-wantPending {
			t.Fatalf("maintenance tick %d recovered pending:%d sending:%d, want %d/%d",
				tick+1, pending, sending, wantPending, 2-wantPending)
		}
	}
	if maintained, err := calling.MaintainOutgoingCallLegs(context.Background()); err != nil || maintained {
		t.Fatalf("maintenance after bounded recovery = maintained:%t err:%v", maintained, err)
	}
}

func TestClaimNextCommandLocksCallLegBeforeProviderCommand(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	now := time.Date(2026, time.September, 1, 12, 30, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(
		t, db.Owner, accessModule, now, "claim-lock-order", 1,
	)
	var callLegID, commandID string
	if err := pool.QueryRow(context.Background(), `
		WITH inserted_call AS (
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point,
				created_at, updated_at
			) VALUES ($1, $2, 'INBOUND', 'STANDALONE', $3, $3)
			RETURNING id
		), inserted_leg AS (
			INSERT INTO human_calling_call_legs (
				call_id, role, sequence, state, provider_connection_id,
				provider_call_control_id, provider_call_leg_id,
				provider_call_session_id, created_at, updated_at
			)
			SELECT id, 'CALLER', 1, 'RINGING', 'claim-connection',
				'claim-control', 'claim-leg', 'claim-session', $3, $3
			FROM inserted_call
			RETURNING id, call_id
		), inserted_command AS (
			INSERT INTO human_calling_provider_commands (
				call_id, call_leg_id, action, target_id, payload,
				created_at, updated_at, next_attempt_at
			)
			SELECT call_id, id, 'HANGUP_LEG', 'claim-target',
				jsonb_build_object('connection_id', 'claim-connection'), $3, $3, $3
			FROM inserted_leg
			RETURNING id, call_leg_id
		)
		SELECT call_leg_id::text, id::text FROM inserted_command
	`, authorization.Practice.ID, authorization.Locations[0].ID, now).Scan(
		&callLegID, &commandID,
	); err != nil {
		t.Fatalf("seed provider command claim: %v", err)
	}

	const barrierKey int64 = 901120001
	barrier := holdPostgresAdvisoryLock(t, pool, barrierKey)
	defer barrier.close()
	installPostgresTestTrigger(t, pool, fmt.Sprintf(`
		CREATE FUNCTION block_provider_command_claim() RETURNS trigger
		LANGUAGE plpgsql AS $function$
		BEGIN
			PERFORM pg_advisory_xact_lock(TG_ARGV[0]::bigint);
			RETURN NEW;
		END
		$function$;
		CREATE TRIGGER block_provider_command_claim
		BEFORE UPDATE ON human_calling_provider_commands
		FOR EACH ROW WHEN (NEW.id = '%s'::uuid AND NEW.state = 'SENDING')
		EXECUTE FUNCTION block_provider_command_claim('%d')
	`, commandID, barrierKey), `
		DROP TRIGGER IF EXISTS block_provider_command_claim
			ON human_calling_provider_commands;
		DROP FUNCTION IF EXISTS block_provider_command_claim()
	`)
	calling := humancalling.New(
		db.As(testdb.Worker), accessModule, &recordingProvider{}, humancalling.Config{},
		func() time.Time { return now },
	)
	type claimResult struct {
		claimed bool
		err     error
	}
	result := make(chan claimResult, 1)
	go func() {
		_, claimed, err := calling.ClaimNextCommand(context.Background())
		result <- claimResult{claimed: claimed, err: err}
	}()
	waitForPostgresLockWaiter(t, barrier.connection, "advisory", barrier.pid)

	lockTx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var lockedCallLegID string
	lockErr := lockTx.QueryRow(context.Background(), `
		SELECT id::text FROM human_calling_call_legs
		WHERE id = $1 FOR UPDATE NOWAIT
	`, callLegID).Scan(&lockedCallLegID)
	_ = lockTx.Rollback(context.Background())
	barrier.release()
	claim := <-result
	if claim.err != nil || !claim.claimed {
		t.Fatalf("claim provider command = claimed:%t err:%v", claim.claimed, claim.err)
	}
	var postgresError *pgconn.PgError
	if !errors.As(lockErr, &postgresError) || postgresError.Code != "55P03" {
		t.Fatalf("CallLeg was not locked before provider command update: %v", lockErr)
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
