package migrations_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
)

func TestCallVersionAdvancesOncePerTransactionForClientVisibleChanges(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	var practiceID, locationID, callID, staffLegID string
	if err := db.Owner.QueryRow(ctx, `
		INSERT INTO access_practices (provisioning_key, name)
		VALUES ('call-version', 'Synthetic') RETURNING id::text
	`).Scan(&practiceID); err != nil {
		t.Fatal(err)
	}
	if err := db.Owner.QueryRow(ctx, `
		INSERT INTO access_locations (practice_id, provisioning_key, name)
		VALUES ($1, 'main', 'Main') RETURNING id::text
	`, practiceID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	if err := db.Owner.QueryRow(ctx, `
		INSERT INTO human_calling_calls (practice_id, location_id, direction, entry_point)
		VALUES ($1, $2, 'INBOUND', 'STANDALONE') RETURNING id::text
	`, practiceID, locationID).Scan(&callID); err != nil {
		t.Fatal(err)
	}
	if err := db.Owner.QueryRow(ctx, `
		INSERT INTO human_calling_call_legs (call_id, role, sequence, staff_subject, staff_session_id, state)
		VALUES ($1, 'STAFF', 1, 'synthetic-staff', 'session-1', 'PENDING') RETURNING id::text
	`, callID).Scan(&staffLegID); err != nil {
		t.Fatal(err)
	}

	listener, err := pgx.ConnectConfig(ctx, db.Owner.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close(context.Background()) })
	if _, err := listener.Exec(ctx, "LISTEN acuity_calling_hints"); err != nil {
		t.Fatal(err)
	}
	hints := func() int {
		count := 0
		for {
			waitContext, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			notification, err := listener.WaitForNotification(waitContext)
			cancel()
			if errors.Is(err, context.DeadlineExceeded) || (err != nil && waitContext.Err() != nil) {
				return count
			}
			if err != nil {
				t.Fatalf("wait for calling hint: %v", err)
			}
			if notification.Payload != `{"practiceId" : "`+practiceID+`"}` {
				t.Fatalf("calling hint payload = %q", notification.Payload)
			}
			count++
		}
	}
	version := func() int64 {
		var current int64
		if err := db.Owner.QueryRow(ctx,
			`SELECT version FROM human_calling_calls WHERE id = $1`, callID,
		).Scan(&current); err != nil {
			t.Fatal(err)
		}
		return current
	}
	hints()

	worker := db.As(testdb.Worker)
	for _, test := range []struct {
		name       string
		statements []string
		advances   bool
	}{
		{"two Call updates", []string{
			`UPDATE human_calling_calls SET provider_termination = 'FIRST' WHERE id = $1`,
			`UPDATE human_calling_calls SET provider_termination = 'SECOND' WHERE id = $1`,
		}, true},
		{"hand-written Call version", []string{
			`UPDATE human_calling_calls SET version = 99 WHERE id = $1`,
		}, true},
		{"CallLeg state", []string{
			`UPDATE human_calling_call_legs SET state = 'DIALING' WHERE call_id = $1`,
		}, true},
		{"CallLeg and Call together", []string{
			`UPDATE human_calling_call_legs SET state = 'RINGING' WHERE call_id = $1`,
			`UPDATE human_calling_calls SET provider_termination = 'THIRD' WHERE id = $1`,
			`UPDATE human_calling_call_legs SET provider_call_session_id = 'session' WHERE call_id = $1`,
		}, true},
		{"CallLeg reconciliation bookkeeping", []string{
			`UPDATE human_calling_call_legs SET reconciliation_checked_at = now(),
				reconciliation_attempts = 1, updated_at = now() WHERE call_id = $1`,
		}, false},
		{"CallLeg softphone session ownership", []string{
			`UPDATE human_calling_call_legs SET staff_session_id = 'session-2' WHERE call_id = $1`,
		}, false},
		{"command the client does not read", []string{
			`INSERT INTO human_calling_provider_commands (call_id, action, target_id)
			VALUES ($1, 'HANGUP_LEG', 'synthetic-control')`,
		}, false},
		{"ring window command", []string{
			`INSERT INTO human_calling_provider_commands (call_id, action, target_id)
			VALUES ($1, 'START_RING_WINDOW', 'synthetic-control')`,
		}, true},
		{"ring window command claimed", []string{
			`UPDATE human_calling_provider_commands SET state = 'SENDING', attempts = 1
			WHERE call_id = $1 AND action = 'START_RING_WINDOW'`,
		}, false},
		{"ring window command sent", []string{
			`UPDATE human_calling_provider_commands SET state = 'SENT', sent_at = now()
			WHERE call_id = $1 AND action = 'START_RING_WINDOW'`,
		}, true},
		{"ring window command unobserved", []string{
			`UPDATE human_calling_provider_commands SET state = 'AMBIGUOUS'
			WHERE call_id = $1 AND action = 'START_RING_WINDOW'`,
		}, true},
		{"recording reserved", []string{
			`INSERT INTO human_calling_call_recordings (
				call_id, practice_id, location_id, audio_state, retention_days, created_at, updated_at
			) SELECT id, practice_id, location_id, 'PROCESSING', 30, now(), now()
			FROM human_calling_calls WHERE id = $1`,
		}, true},
		{"recording reconciliation bookkeeping", []string{
			`UPDATE human_calling_call_recordings SET reconciliation_attempts = 1,
				reconciliation_claimed_at = now(), next_reconciliation_attempt_at = now(),
				reconciliation_error_code = 'SYNTHETIC', updated_at = now()
			WHERE call_id = $1`,
		}, false},
		{"recording ready", []string{
			`UPDATE human_calling_call_recordings SET audio_state = 'READY',
				provider_recording_id = 'synthetic-recording',
				recording_started_at = now() - interval '1 minute', recording_ended_at = now(),
				content_expires_at = now() + interval '30 days', duration_millis = 60000,
				reconciliation_claimed_at = NULL, next_reconciliation_attempt_at = NULL,
				reconciliation_error_code = NULL
			WHERE call_id = $1`,
		}, true},
		{"recording deletion bookkeeping", []string{
			`UPDATE human_calling_call_recordings SET deletion_attempts = 1,
				deletion_claimed_at = now(), next_deletion_attempt_at = now(),
				deletion_error_code = 'SYNTHETIC', updated_at = now()
			WHERE call_id = $1`,
		}, false},
		{"Hangup request", []string{
			`INSERT INTO human_calling_timeline (call_id, practice_id, kind, occurred_at)
			SELECT id, practice_id, 'call.hangup.requested', now() FROM human_calling_calls WHERE id = $1`,
		}, true},
		{"other timeline evidence", []string{
			`INSERT INTO human_calling_timeline (call_id, practice_id, kind, occurred_at)
			SELECT id, practice_id, 'provider.receipt.applied', now() FROM human_calling_calls WHERE id = $1`,
		}, false},
	} {
		before := version()
		tx, err := worker.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, statement := range test.statements {
			if _, err := tx.Exec(ctx, statement, callID); err != nil {
				_ = tx.Rollback(ctx)
				t.Fatalf("%s: %v", test.name, err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("%s: commit: %v", test.name, err)
		}
		wantVersion, wantHints := before, 0
		if test.advances {
			wantVersion, wantHints = before+1, 1
		}
		if got, sent := version(), hints(); got != wantVersion || sent != wantHints {
			t.Errorf("%s: version %d -> %d with %d calling hints, want %d with %d",
				test.name, before, got, sent, wantVersion, wantHints)
		}
	}
}
