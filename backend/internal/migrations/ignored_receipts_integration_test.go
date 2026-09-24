package migrations_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestIgnoredReceiptMigrationAllowsWritesAndResumesIndexBuild(t *testing.T) {
	pool := testdb.OpenThrough(t, "0071_callback_completion.sql")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	// An existing writer makes a concurrent index build wait before scanning.
	if _, err := blocker.Exec(ctx, `LOCK TABLE human_calling_call_legs IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	migrationCtx, stopMigration := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- migrations.ApplyThrough(migrationCtx, pool, "0072_ignored_credential_receipts.sql") }()
	finished := false
	defer func() {
		stopMigration()
		_ = blocker.Rollback(context.Background())
		if !finished {
			<-done
		}
	}()
	var migrationPID int
	for {
		if err := pool.QueryRow(ctx, `SELECT COALESCE((
			SELECT pid FROM pg_stat_activity
			WHERE datname = current_database()
				AND pid <> pg_backend_pid() AND state = 'active'
				AND wait_event_type = 'Lock'
				AND query LIKE '%CREATE INDEX%human_calling_staff_provider_session_idx%'
		), 0)`).Scan(&migrationPID); err != nil {
			t.Fatal(err)
		}
		if migrationPID != 0 {
			break
		}
		select {
		case err := <-done:
			finished = true
			t.Fatalf("migration finished before waiting for writer: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Neither the receipt validation nor the index build may retain a lock
	// that blocks ingress inserts or live CallLeg updates.
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := writer.Exec(ctx, `SET LOCAL lock_timeout = '250ms';
		LOCK TABLE human_calling_provider_receipts, human_calling_call_legs IN ROW EXCLUSIVE MODE`)
	_ = writer.Rollback(ctx)
	if writeErr != nil {
		t.Fatalf("migration blocks live writes: %v", writeErr)
	}
	var validated bool
	if err := pool.QueryRow(ctx, `SELECT convalidated FROM pg_constraint
		WHERE conrelid = 'human_calling_provider_receipts'::regclass
			AND conname = 'human_calling_provider_receipts_state_check'`).Scan(&validated); err != nil {
		t.Fatal(err)
	}
	if !validated {
		t.Fatal("receipt constraint was not validated before index build")
	}
	// Context cancellation can return before PostgreSQL stops the build. Keep
	// the writer lock until the server acknowledges cancellation, otherwise
	// releasing it can let the index finish before the cancel request arrives.
	var canceled bool
	if err := pool.QueryRow(ctx, `SELECT pg_cancel_backend($1)`, migrationPID).Scan(&canceled); err != nil {
		t.Fatal(err)
	}
	if !canceled {
		t.Fatal("index build cancellation was not sent")
	}
	err = <-done
	finished = true
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "57014" {
		t.Fatalf("expected server to cancel index build: %v", err)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var valid bool
	if err := pool.QueryRow(ctx, `SELECT indisvalid FROM pg_index
		WHERE indexrelid = 'human_calling_staff_provider_session_idx'::regclass`).Scan(&valid); err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("expected interrupted build to leave an invalid index")
	}
	if err := migrations.ApplyThrough(ctx, pool, "0072_ignored_credential_receipts.sql"); err != nil {
		t.Fatalf("resume migration: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT indisvalid AND indisready FROM pg_index
		WHERE indexrelid = 'human_calling_staff_provider_session_idx'::regclass`).Scan(&valid); err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("resumed migration did not repair the index")
	}
	if err := migrations.ApplyThrough(ctx, pool, "0072_ignored_credential_receipts.sql"); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}
