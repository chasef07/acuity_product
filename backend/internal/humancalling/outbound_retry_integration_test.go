package humancalling_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestRetryOutboundCallCopiesTheEarlierEntryPoint(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)
	fixture := newOutboundEndFixture(t, "outbound-retry", now, &recordingProvider{})
	practiceID := fixture.authorization.Practice.ID
	locationID := fixture.authorization.Locations[0].ID
	retry := func(callID, idempotencyKey string) (humancalling.Call, error) {
		return fixture.calling.RetryOutboundCall(ctx, humancalling.RetryOutboundCallCommand{
			Identity: fixture.identity, SessionID: fixture.sessionID,
			IdempotencyKey: idempotencyKey, CallID: callID,
		})
	}
	endLegs := func(callID string) {
		t.Helper()
		if _, err := fixture.pool.Exec(ctx, `
			UPDATE human_calling_call_legs SET state = 'ENDED', ended_at = $2, updated_at = $2
			WHERE call_id = $1
		`, callID, now); err != nil {
			t.Fatalf("end synthetic CallLegs: %v", err)
		}
	}
	endCall := func(callID string) {
		t.Helper()
		endLegs(callID)
		if _, err := fixture.pool.Exec(ctx, `
			UPDATE human_calling_calls
			SET terminal_outcome = 'UNANSWERED', ended_at = $2, updated_at = $2
			WHERE id = $1
		`, callID, now); err != nil {
			t.Fatalf("end synthetic Call: %v", err)
		}
	}

	t.Run("standalone retries the same Location and destination", func(t *testing.T) {
		previous := fixture.startCall(t, "outbound-retry-standalone")
		endLegs(previous.ID)
		if _, err := retry(previous.ID, "outbound-retry-live"); !errors.Is(err, humancalling.ErrConflict) {
			t.Fatalf("retry of a non-terminal Call error = %v, want conflict", err)
		}
		outsider := access.Identity{
			Subject: "outbound-retry-outsider", Email: "outsider@synthetic.test", EmailVerified: true,
		}
		if _, err := fixture.calling.RetryOutboundCall(ctx, humancalling.RetryOutboundCallCommand{
			Identity: outsider, SessionID: fixture.sessionID,
			IdempotencyKey: "outbound-retry-outsider", CallID: previous.ID,
		}); !errors.Is(err, humancalling.ErrDenied) {
			t.Fatalf("unauthorized retry error = %v, want denied", err)
		}
		endCall(previous.ID)
		call, err := retry(previous.ID, "outbound-retry-standalone-again")
		if err != nil {
			t.Fatalf("retry standalone Call: %v", err)
		}
		if call.ID == previous.ID || call.RetryOfCallID != previous.ID ||
			call.EntryPoint != humancalling.CallEntryStandalone || call.TaskID != "" ||
			call.PracticeID != practiceID || call.LocationID != locationID ||
			call.Phone != previous.Phone {
			t.Fatalf("standalone retry = %#v, previous %#v", call, previous)
		}
		replayed, err := retry(previous.ID, "outbound-retry-standalone-again")
		if err != nil || replayed.ID != call.ID {
			t.Fatalf("replayed retry = %q, %v; want %q", replayed.ID, err, call.ID)
		}
		endCall(call.ID)
	})

	t.Run("Task Call retries through its Task", func(t *testing.T) {
		workModule := work.New(fixture.pool,
			access.New(fixture.pool, func() time.Time { return now }),
			func() time.Time { return now })
		var sourceID string
		if err := fixture.pool.QueryRow(ctx, `
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point, terminal_outcome,
				caller_phone, ended_at, created_at, updated_at
			) VALUES ($1, $2, 'INBOUND', 'STANDALONE', 'VOICEMAIL', '+15555550134', $3, $3, $3)
			RETURNING id::text
		`, practiceID, locationID, now.Add(-time.Minute)).Scan(&sourceID); err != nil {
			t.Fatalf("insert synthetic source Call: %v", err)
		}
		tx, err := fixture.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		task, err := workModule.EnsureRecoveryTask(ctx, tx, work.EnsureRecoveryTaskCommand{
			CallID: sourceID, PracticeID: practiceID, LocationID: locationID,
			Phone: "+15555550134", Outcome: work.RecoveryOutcomeVoicemail,
			OccurredAt: now.Add(-time.Minute),
		})
		if err != nil {
			t.Fatalf("create synthetic Task: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		previous, err := fixture.calling.StartOutboundCall(ctx, humancalling.StartOutboundCallCommand{
			Identity: fixture.identity, SessionID: fixture.sessionID,
			IdempotencyKey: "outbound-retry-task", TaskID: task.ID,
		})
		if err != nil {
			t.Fatalf("start Task Call: %v", err)
		}
		endCall(previous.ID)
		call, err := retry(previous.ID, "outbound-retry-task-again")
		if err != nil {
			t.Fatalf("retry Task Call: %v", err)
		}
		if call.RetryOfCallID != previous.ID || call.EntryPoint != humancalling.CallEntryTask ||
			call.TaskID != task.ID || call.LocationID != locationID || call.Phone != task.Phone {
			t.Fatalf("Task retry = %#v, previous %#v", call, previous)
		}
		endCall(call.ID)
	})

	t.Run("AI handoff Call is not retried", func(t *testing.T) {
		var handoffCallID string
		if err := fixture.pool.QueryRow(ctx, `
			INSERT INTO human_calling_calls (
				practice_id, location_id, direction, entry_point, terminal_outcome,
				caller_phone, ended_at, created_at, updated_at
			) VALUES ($1, $2, 'INBOUND', 'AI_HANDOFF', 'MISSED', '+15555550145', $3, $3, $3)
			RETURNING id::text
		`, practiceID, locationID, now).Scan(&handoffCallID); err != nil {
			t.Fatalf("insert synthetic AI handoff Call: %v", err)
		}
		if _, err := retry(handoffCallID, "outbound-retry-handoff"); !errors.Is(err, humancalling.ErrConflict) {
			t.Fatalf("AI handoff retry error = %v, want conflict", err)
		}
		var retries int
		if err := fixture.pool.QueryRow(ctx, `
			SELECT count(*) FROM human_calling_calls WHERE retry_of_call_id = $1
		`, handoffCallID).Scan(&retries); err != nil || retries != 0 {
			t.Fatalf("AI handoff retries = %d, %v", retries, err)
		}
	})
}
