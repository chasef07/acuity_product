package humancalling_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCallingStateValidatorOnlyLoadsVisibleVoicemailHistory(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	authorization, identities := provisionConcurrentStaff(
		t, access.New(pool, func() time.Time { return now }), now, "calling-state-capacity", 1,
	)
	// Production-shaped history: 4,032 Calls, 772 undisposed voicemails,
	// 15,625 CallLegs, and 32,449 commands, including one 2,836-command Call.
	if _, err := pool.Exec(ctx, `
		WITH inserted AS (
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point,
				terminal_outcome, ended_at, created_at, updated_at
			)
			SELECT $1, $2, 'INBOUND', 'STANDALONE',
				CASE WHEN n <= 772 THEN 'VOICEMAIL' ELSE 'RESOLVED' END,
				$3::timestamptz - interval '1 hour',
				$3::timestamptz - interval '2 hours' + n * interval '1 millisecond',
				$3::timestamptz - interval '2 hours' + n * interval '1 millisecond'
			FROM generate_series(1, 4032) n RETURNING id, created_at
		), calls AS (
			SELECT id, row_number() OVER (ORDER BY created_at) n FROM inserted
		)
		INSERT INTO human_calling_provider_commands (
			call_id, action, target_id, state, created_at, updated_at
		)
		SELECT id, 'HANGUP_LEG', 'synthetic-' || ordinal, 'SENT', $3, $3
		FROM calls
		CROSS JOIN LATERAL generate_series(
			1, CASE WHEN n = 1 THEN 2836 WHEN n <= 1397 THEN 8 ELSE 7 END
		) ordinal
	`, authorization.Practice.ID, authorization.Locations[0].ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		WITH calls AS (
			SELECT id, row_number() OVER (ORDER BY created_at) n
			FROM human_calling_calls
		)
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, staff_subject, state,
			ended_at, created_at, updated_at
		)
		SELECT id, CASE WHEN ordinal = 1 THEN 'CALLER' ELSE 'STAFF' END,
			ordinal, CASE WHEN ordinal > 1 THEN 'synthetic-staff-' || ordinal END,
			'ENDED', $1, $1, $1
		FROM calls
		CROSS JOIN LATERAL generate_series(1, CASE WHEN n <= 3529 THEN 4 ELSE 3 END) ordinal
	`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_softphone_leases (
			user_subject, session_id, lease_expires_at, readiness_updated_at
		) VALUES ($1, 'synthetic-session', $2, $3)
	`, identities[0].Subject, now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"human_calling_calls", "human_calling_call_legs",
		"human_calling_provider_commands", "access_memberships",
	} {
		if _, err := pool.Exec(ctx, "ANALYZE "+table); err != nil {
			t.Fatal(err)
		}
	}
	trace := &callingStateQueryTrace{}
	config := pool.Config().Copy()
	config.MaxConns = 1
	config.ConnConfig.Tracer = trace
	runtimePool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtimePool.Close)
	database, err := productpostgres.NewExecutor(runtimePool, productpostgres.ExecutorConfig{
		AcquireTimeout: 1500 * time.Millisecond, OperationTimeout: 10 * time.Second,
		StatementTimeout: 5 * time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(database,
		access.New(database, func() time.Time { return now }), nil,
		humancalling.Config{}, func() time.Time { return now },
	)
	started := time.Now()
	state, err := calling.ReadCallingState(ctx, identities[0])
	if err != nil || state.Voicemail == nil {
		t.Fatalf("initial Calling state: voicemail=%v err=%v", state.Voicemail, err)
	}
	t.Logf("initial Calling state: %s", time.Since(started))
	if trace.sql == "" {
		t.Fatal("Calling validator query not captured")
	}
	var snapshot string
	if err := pool.QueryRow(ctx, trace.sql, trace.args...).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	var rawPlan []byte
	if err := pool.QueryRow(ctx,
		"EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+trace.sql, trace.args...,
	).Scan(&rawPlan); err != nil {
		t.Fatal(err)
	}
	var plans []struct {
		ExecutionTime float64 `json:"Execution Time"`
	}
	if err := json.Unmarshal(rawPlan, &plans); err != nil || len(plans) != 1 {
		t.Fatalf("decode Calling validator plan: %v", err)
	}
	t.Logf("Calling validator: %d snapshot bytes, %.2f ms execution", len(snapshot), plans[0].ExecutionTime)
	if len(snapshot) >= 64*1024 {
		t.Fatalf("Calling validator loaded %d bytes for one visible voicemail, want less than 64 KiB", len(snapshot))
	}

	readConditionally := func(previous humancalling.CallingState, unchanged bool) humancalling.CallingState {
		t.Helper()
		started := time.Now()
		next, notModified, err := calling.ReadCallingStateConditionally(ctx, identities[0], previous.ETag)
		if err != nil || notModified != unchanged {
			t.Fatalf("conditional Calling state: notModified=%t want=%t err=%v", notModified, unchanged, err)
		}
		t.Logf("conditional Calling state: %s, notModified=%t", time.Since(started), notModified)
		return next
	}
	readConditionally(state, true)
	var hiddenCallID string
	if err := pool.QueryRow(ctx, `
		SELECT id::text FROM human_calling_calls
		WHERE terminal_outcome = 'VOICEMAIL' AND id <> $1
		ORDER BY updated_at DESC, id LIMIT 1
	`, state.Voicemail.CallID).Scan(&hiddenCallID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1 WHERE id = $1
	`, hiddenCallID); err != nil {
		t.Fatal(err)
	}
	// An older voicemail's metadata cannot change the one visible voicemail.
	readConditionally(state, true)

	if _, err := pool.Exec(ctx, `
		UPDATE human_calling_provider_commands SET state = 'RECONCILED'
		WHERE id = (
			SELECT id FROM human_calling_provider_commands WHERE call_id = $1 LIMIT 1
		)
	`, state.Voicemail.CallID); err != nil {
		t.Fatal(err)
	}
	state = readConditionally(state, false)
	if _, err := pool.Exec(ctx, `
		UPDATE human_calling_calls SET updated_at = $2, version = version + 1 WHERE id = $1
	`, hiddenCallID, now); err != nil {
		t.Fatal(err)
	}
	state = readConditionally(state, false)
	if state.Voicemail == nil || state.Voicemail.CallID != hiddenCallID {
		t.Fatalf("newest voicemail not selected: %#v", state.Voicemail)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE human_calling_calls SET disposition_at = $2, version = version + 1 WHERE id = $1
	`, hiddenCallID, now); err != nil {
		t.Fatal(err)
	}
	state = readConditionally(state, false)
	if state.Voicemail == nil || state.Voicemail.CallID == hiddenCallID {
		t.Fatalf("next voicemail not exposed after disposition: %#v", state.Voicemail)
	}

	var activeCallID string
	if err := pool.QueryRow(ctx, `
		UPDATE human_calling_calls SET terminal_outcome = NULL, ended_at = NULL,
			updated_at = $1, version = version + 1
		WHERE id = (SELECT id FROM human_calling_calls WHERE terminal_outcome = 'RESOLVED' LIMIT 1)
		RETURNING id::text
	`, now.Add(time.Minute)).Scan(&activeCallID); err != nil {
		t.Fatal(err)
	}
	readConditionally(state, true)
	var commandID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO human_calling_provider_commands (call_id, action, target_id, state)
		VALUES ($1, 'SPEAK_VOICEMAIL', 'synthetic-voicemail', 'PENDING') RETURNING id::text
	`, activeCallID).Scan(&commandID); err != nil {
		t.Fatal(err)
	}
	state = readConditionally(state, false)
	if state.Voicemail == nil || state.Voicemail.CallID != activeCallID ||
		state.Voicemail.State != "VOICEMAIL_GREETING" {
		t.Fatalf("active voicemail command not projected: %#v", state.Voicemail)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE human_calling_provider_commands SET state = 'FAILED' WHERE id = $1
	`, commandID); err != nil {
		t.Fatal(err)
	}
	state = readConditionally(state, false)
	if state.Voicemail == nil || state.Voicemail.CallID == activeCallID {
		t.Fatalf("next voicemail not exposed after command failure: %#v", state.Voicemail)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE human_calling_calls SET terminal_outcome = 'VOICEMAIL', ended_at = $2 WHERE id = $1;
	`, activeCallID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM human_calling_call_legs WHERE call_id = $1 AND role = 'CALLER'
	`, activeCallID); err != nil {
		t.Fatal(err)
	}
	// A Call without a Caller CallLeg cannot appear in the voicemail projection.
	readConditionally(state, true)

	if _, err := pool.Exec(ctx, `
		UPDATE access_memberships SET location_scope = 'SELECTED' WHERE id = $1
	`, authorization.Membership.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_membership_locations (membership_id, practice_id, location_id)
		VALUES ($1, $2, $3)
	`, authorization.Membership.ID, authorization.Practice.ID, authorization.Locations[0].ID); err != nil {
		t.Fatal(err)
	}
	state = readConditionally(state, false)
	var excludedLocationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_locations (practice_id, provisioning_key, name)
		VALUES ($1, 'excluded', 'Excluded Location') RETURNING id::text
	`, authorization.Practice.ID).Scan(&excludedLocationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		WITH inserted AS (
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point,
				terminal_outcome, ended_at, created_at, updated_at
			)
			VALUES ($1, $2, 'INBOUND', 'STANDALONE', 'VOICEMAIL', $3, $3, $3)
			RETURNING id
		)
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, state, ended_at, created_at, updated_at
		)
		SELECT id, 'CALLER', 1, 'ENDED', $3, $3, $3 FROM inserted
	`, authorization.Practice.ID, excludedLocationID, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// A newer voicemail outside the selected Location cannot invalidate state.
	readConditionally(state, true)
	if _, err := pool.Exec(ctx, `
		UPDATE access_memberships SET role = 'ADMIN', location_scope = 'ALL' WHERE id = $1
	`, authorization.Membership.ID); err != nil {
		t.Fatal(err)
	}
	state = readConditionally(state, false)
	if state.Voicemail != nil {
		t.Fatalf("Admin cannot receive scoped voicemail state: %#v", state.Voicemail)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE human_calling_calls SET version = version + 1 WHERE terminal_outcome = 'VOICEMAIL'
	`); err != nil {
		t.Fatal(err)
	}
	readConditionally(state, true)
}

type callingStateQueryTrace struct {
	sql  string
	args []any
}

func (trace *callingStateQueryTrace) TraceQueryStart(
	ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData,
) context.Context {
	if strings.Contains(data.SQL, "WITH relevant_call_ids AS MATERIALIZED") {
		trace.sql = data.SQL
		trace.args = append([]any(nil), data.Args...)
	}
	return ctx
}

func (*callingStateQueryTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
