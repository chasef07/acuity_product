package humancalling_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This provider honors the same lower-bound event filter as ObserveCall's
// Telnyx adapter. A failed read must not move that bound beyond unseen evidence.
type scheduledObservationProvider struct {
	recordingProvider
	err              error
	since            []time.Time
	event            *humancalling.ProviderFact
	healthyControlID string
}

func (p *scheduledObservationProvider) ObserveCall(
	_ context.Context, _ string, controlID, legID, _ string, since time.Time,
) (humancalling.ProviderCallObservation, error) {
	p.since = append(p.since, since)
	observation := humancalling.ProviderCallObservation{
		Active: true, CallControlID: controlID, CallLegID: legID,
	}
	if p.event != nil && !p.event.OccurredAt.Before(since) {
		observation.Events = append(observation.Events, *p.event)
		if p.event.Type == humancalling.FactCallHangup {
			observation.Active = false
		}
	}
	if controlID == p.healthyControlID {
		return observation, nil
	}
	return observation, p.err
}

func TestOutgoingReconciliationBackoffIsDurableAndDoesNotStarveOtherCalls(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.September, 4, 13, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(t, accessModule, now, "observation-schedule", 1)
	legID := seedScheduledObservationLeg(t, pool, authorization, now.Add(-2*time.Minute), "failing")
	provider := &scheduledObservationProvider{err: humancalling.ErrInvalidInput, healthyControlID: "healthy-control"}
	for index, delay := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute} {
		calling := humancalling.New(pool, accessModule, provider, humancalling.Config{}, func() time.Time { return now })
		if maintained, err := calling.MaintainOutgoingCallLegs(context.Background()); !maintained || !errors.Is(err, humancalling.ErrInvalidInput) {
			t.Fatalf("observation %d = maintained:%t err:%v", index+1, maintained, err)
		}
		var next time.Time
		var attempts int
		var code, state string
		if err := pool.QueryRow(context.Background(), `
			SELECT reconciliation_next_attempt_at, reconciliation_attempts,
				reconciliation_error_code, state
			FROM human_calling_call_legs WHERE id = $1
		`, legID).Scan(&next, &attempts, &code, &state); err != nil {
			t.Fatal(err)
		}
		if !next.Equal(now.Add(delay)) || attempts != min(index+1, 5) || code != "INVALID_INPUT" || state != "RINGING" {
			t.Fatalf("observation %d schedule = next:%s attempts:%d error:%s state:%s", index+1, next, attempts, code, state)
		}
		if index == 1 {
			healthyLegID := seedScheduledObservationLeg(t, pool, authorization, now.Add(-90*time.Second), "healthy")
			if maintained, err := calling.MaintainOutgoingCallLegs(context.Background()); !maintained || err != nil {
				t.Fatalf("failing Call starved another Call: maintained:%t err:%v", maintained, err)
			}
			// Retire only the synthetic fixture after proving its progress.
			if _, err := pool.Exec(context.Background(), `UPDATE human_calling_call_legs SET state = 'ENDED', ended_at = $2 WHERE id = $1`, healthyLegID, now); err != nil {
				t.Fatal(err)
			}
		}
		now = next.Add(-time.Second)
		if maintained, err := calling.MaintainOutgoingCallLegs(context.Background()); maintained || err != nil {
			t.Fatalf("observation %d ignored durable backoff: maintained:%t err:%v", index+1, maintained, err)
		}
		now = next
	}
	// New durable provider state makes the item eligible after normal stale age,
	// even when the previous failure was backed off for fifteen minutes.
	now = now.Add(-14 * time.Minute)
	if _, err := pool.Exec(context.Background(), `UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1`, legID, now); err != nil {
		t.Fatal(err)
	}
	now = now.Add(61 * time.Second)
	provider.err = nil
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{}, func() time.Time { return now })
	if maintained, err := calling.MaintainOutgoingCallLegs(context.Background()); !maintained || err != nil {
		t.Fatalf("fresh durable state stayed behind old backoff: maintained:%t err:%v", maintained, err)
	}
	var attempts, commands int
	var errorCode *string
	if err := pool.QueryRow(context.Background(), `
		SELECT reconciliation_attempts, reconciliation_error_code,
			(SELECT count(*) FROM human_calling_provider_commands WHERE call_leg_id = $1)
		FROM human_calling_call_legs WHERE id = $1
	`, legID).Scan(&attempts, &errorCode, &commands); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || errorCode != nil || commands != 0 {
		t.Fatalf("successful read retry state = attempts:%d error:%v provider commands:%d", attempts, errorCode, commands)
	}
}

func seedScheduledObservationLeg(t *testing.T, pool *pgxpool.Pool, authorization access.Authorization, updatedAt time.Time, prefix string) string {
	t.Helper()
	var legID string
	if err := pool.QueryRow(context.Background(), `
		WITH inserted_call AS (
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point, created_at, updated_at
			) VALUES ($1, $2, 'INBOUND', 'STANDALONE', $3, $3) RETURNING id
		)
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, state, provider_connection_id,
			provider_call_control_id, provider_call_leg_id, provider_call_session_id,
			created_at, updated_at
		)
		SELECT id, 'CALLER', 1, 'RINGING', 'synthetic-connection',
			$4 || '-control', $4 || '-leg', $4 || '-session', $3, $3
		FROM inserted_call RETURNING id::text
	`, authorization.Practice.ID, authorization.Locations[0].ID, updatedAt, prefix).Scan(&legID); err != nil {
		t.Fatal(err)
	}
	return legID
}

type blockedObservationProvider struct {
	recordingProvider
	started chan struct{}
	release chan struct{}
}

func (p *blockedObservationProvider) ObserveCall(
	ctx context.Context, _ string, controlID, legID, _ string, _ time.Time,
) (humancalling.ProviderCallObservation, error) {
	close(p.started)
	select {
	case <-p.release:
	case <-ctx.Done():
		return humancalling.ProviderCallObservation{}, ctx.Err()
	}
	return humancalling.ProviderCallObservation{
		Active: false, CallControlID: controlID, CallLegID: legID,
	}, nil
}

func TestOutgoingReconciliationLateObserverCannotOverwriteNewerFailure(t *testing.T) {
	t.Run("inferred hangup", func(t *testing.T) { testLateObservation(t, false) })
	t.Run("unobserved command rejection", func(t *testing.T) { testLateObservation(t, true) })
}

func testLateObservation(t *testing.T, withCommand bool) {
	t.Helper()
	pool := testdb.Open(t)
	initialTime := time.Date(2026, time.September, 4, 14, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return initialTime })
	authorization, _ := provisionConcurrentStaff(t, accessModule, initialTime, "observation-fence", 1)
	legID := seedScheduledObservationLeg(t, pool, authorization, initialTime.Add(-2*time.Minute), "fenced")
	if withCommand {
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO human_calling_provider_commands (
				call_id, call_leg_id, action, target_id, payload, state, created_at, updated_at
			)
			SELECT call_id, id, 'ANSWER_CALLER', provider_call_control_id, '{}', 'SENT', $2, $2
			FROM human_calling_call_legs WHERE id = $1
		`, legID, initialTime.Add(-2*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	provider := &blockedObservationProvider{started: make(chan struct{}), release: make(chan struct{})}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{}, func() time.Time { return initialTime })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := calling.MaintainOutgoingCallLegs(ctx)
		done <- err
	}()
	select {
	case <-provider.started:
	case <-ctx.Done():
		t.Fatal("observation did not start")
	}
	// Simulate a later committed receipt, then a second worker after the first
	// observation lease expires. Neither observer owns domain-state timestamps.
	updatedAt := initialTime.Add(time.Second)
	if _, err := pool.Exec(ctx, `UPDATE human_calling_call_legs SET updated_at = $2 WHERE id = $1`, legID, updatedAt); err != nil {
		t.Fatal(err)
	}
	newTime := initialTime.Add(62 * time.Second)
	newProvider := &scheduledObservationProvider{err: humancalling.ErrDefinitiveProviderFailure}
	newCalling := humancalling.New(pool, accessModule, newProvider, humancalling.Config{}, func() time.Time { return newTime })
	if maintained, err := newCalling.MaintainOutgoingCallLegs(ctx); !maintained || !errors.Is(err, humancalling.ErrDefinitiveProviderFailure) {
		t.Fatalf("new observation = maintained:%t err:%v", maintained, err)
	}
	close(provider.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var checkedAt, next, domainUpdatedAt time.Time
	var attempts int
	var code string
	if err := pool.QueryRow(ctx, `
		SELECT reconciliation_checked_at, reconciliation_next_attempt_at,
			reconciliation_attempts, reconciliation_error_code, updated_at
		FROM human_calling_call_legs WHERE id = $1
	`, legID).Scan(&checkedAt, &next, &attempts, &code, &domainUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if !checkedAt.Equal(newTime) || !next.Equal(newTime.Add(time.Minute)) || attempts != 1 || code != "PROVIDER_REJECTED" || !domainUpdatedAt.Equal(updatedAt) {
		t.Fatalf("late observer overwrote newer evidence: checked:%s next:%s attempts:%d error:%s domain:%s", checkedAt, next, attempts, code, domainUpdatedAt)
	}
	var inferredFacts, changedCommands int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM human_calling_projected_facts WHERE event_id LIKE 'reconcile-absent-%'),
			(SELECT count(*) FROM human_calling_provider_commands WHERE call_leg_id = $1 AND state <> 'SENT')
	`, legID).Scan(&inferredFacts, &changedCommands); err != nil {
		t.Fatal(err)
	}
	if inferredFacts != 0 || changedCommands != 0 {
		t.Fatalf("stale observation committed inferred facts:%d or changed commands:%d", inferredFacts, changedCommands)
	}
}

func TestOutgoingReconciliationPreservesEvidenceWindowAcrossFailure(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	authorization, _ := provisionConcurrentStaff(t, accessModule, now, "observation-window", 1)
	updatedAt := now.Add(-2 * time.Minute)
	var legID string
	if err := pool.QueryRow(context.Background(), `
		WITH inserted_call AS (
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point, created_at, updated_at
			) VALUES ($1, $2, 'INBOUND', 'STANDALONE', $3, $3) RETURNING id
		)
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, state, provider_connection_id,
			provider_call_control_id, provider_call_leg_id, provider_call_session_id,
			created_at, updated_at
		)
		SELECT id, 'CALLER', 1, 'RINGING', 'synthetic-connection',
			'synthetic-control', 'synthetic-leg', 'synthetic-session', $3, $3
		FROM inserted_call RETURNING id::text
	`, authorization.Practice.ID, authorization.Locations[0].ID, updatedAt).Scan(&legID); err != nil {
		t.Fatal(err)
	}
	provider := &scheduledObservationProvider{err: humancalling.ErrAmbiguousEffect}
	calling := humancalling.New(pool, accessModule, provider, humancalling.Config{}, func() time.Time { return now })
	if maintained, err := calling.MaintainOutgoingCallLegs(context.Background()); !maintained || !errors.Is(err, humancalling.ErrAmbiguousEffect) {
		t.Fatalf("first observation = maintained:%t err:%v", maintained, err)
	}
	var afterFailure time.Time
	if err := pool.QueryRow(context.Background(), `SELECT updated_at FROM human_calling_call_legs WHERE id = $1`, legID).Scan(&afterFailure); err != nil {
		t.Fatal(err)
	}
	if !afterFailure.Equal(updatedAt) {
		t.Errorf("failed provider observation changed domain timestamp: got %s, want %s", afterFailure, updatedAt)
	}
	now = now.Add(61 * time.Second)
	provider.err = nil
	// Recreate the module to prove recovery does not depend on process memory.
	calling = humancalling.New(pool, accessModule, provider, humancalling.Config{}, func() time.Time { return now })
	if maintained, err := calling.MaintainOutgoingCallLegs(context.Background()); !maintained || err != nil {
		t.Fatalf("retry observation = maintained:%t err:%v", maintained, err)
	}
	if len(provider.since) != 2 || !provider.since[1].Equal(provider.since[0]) {
		t.Fatalf("failed read skipped provider evidence: lower bounds %v", provider.since)
	}
	now = now.Add(61 * time.Second)
	if maintained, err := calling.MaintainOutgoingCallLegs(context.Background()); !maintained || err != nil {
		t.Fatalf("successful checkpoint observation = maintained:%t err:%v", maintained, err)
	}
	if len(provider.since) != 3 || !provider.since[2].Equal(provider.since[1]) {
		t.Fatalf("empty successful read skipped delayed provider evidence: %v", provider.since)
	}
	provider.event = &humancalling.ProviderFact{
		EventID: "synthetic-delayed-hangup", Type: humancalling.FactCallHangup,
		OccurredAt: updatedAt.Add(30 * time.Second), ConnectionID: "synthetic-connection",
		CallControlID: "synthetic-control", CallLegID: "synthetic-leg", CallSessionID: "synthetic-session",
		HangupCause: "NORMAL_CLEARING", TerminationSource: "CALLER",
	}
	now = now.Add(61 * time.Second)
	if maintained, err := calling.MaintainOutgoingCallLegs(context.Background()); !maintained || err != nil {
		t.Fatalf("delayed evidence observation = maintained:%t err:%v", maintained, err)
	}
	var state string
	if err := pool.QueryRow(context.Background(), `SELECT state FROM human_calling_call_legs WHERE id = $1`, legID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "ENDED" {
		t.Fatalf("delayed provider hangup did not converge durable CallLeg: %s", state)
	}
}
