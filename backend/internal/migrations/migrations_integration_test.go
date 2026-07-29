package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestForwardMigrationsAreRepeatableAndIncludeReviewedAuthAndCallingSchemas(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("repeat migrations: %v", err)
	}
	var migrationCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.schema_migrations`,
	).Scan(&migrationCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrationCount != 9 {
		t.Fatalf("migration count = %d, want 9", migrationCount)
	}
	var activeCommandIndexIsUnique bool
	if err := pool.QueryRow(ctx, `
		SELECT indisunique
		FROM pg_index
		WHERE indexrelid = 'human_calling_active_call_commands_idx'::regclass
	`).Scan(&activeCommandIndexIsUnique); err != nil {
		t.Fatalf("inspect active Call command index: %v", err)
	}
	if !activeCommandIndexIsUnique {
		t.Fatal("active Call command index is not unique")
	}

	for _, table := range []string{
		"user",
		"session",
		"account",
		"verification",
		"jwks",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM information_schema.tables
				WHERE table_schema = 'auth' AND table_name = $1
			)
		`, table).Scan(&exists); err != nil {
			t.Fatalf("inspect auth table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("auth table %s is missing", table)
		}
	}
	for _, table := range []string{
		"human_calling_handoffs",
		"human_calling_calls",
		"human_calling_provider_commands",
		"human_calling_provider_receipts",
		"human_calling_recordings",
		"human_calling_timeline",
		"access_abita_office_locations",
		"work_tasks",
		"work_task_activities",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)
		`, table).Scan(&exists); err != nil {
			t.Fatalf("inspect calling table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("calling table %s is missing", table)
		}
	}
	for _, view := range []string{
		"access_operational_users",
		"human_calling_operational_users",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM information_schema.views
				WHERE table_schema = 'public'
					AND table_name = $1
			)
		`, view).Scan(&exists); err != nil {
			t.Fatalf("inspect operational Users view %s: %v", view, err)
		}
		if !exists {
			t.Fatalf("operational Users view %s is missing", view)
		}
	}
}

func TestProviderReceiptRetryMigrationEnforcesAttemptAndQuarantineState(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)

	var retryColumns int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
			AND table_name = 'human_calling_provider_receipts'
			AND column_name IN (
				'projection_attempts',
				'last_attempt_at',
				'quarantined_at'
			)
	`).Scan(&retryColumns); err != nil {
		t.Fatalf("inspect provider receipt retry columns: %v", err)
	}
	if retryColumns != 3 {
		t.Fatalf("provider receipt retry column count = %d, want 3", retryColumns)
	}

	assertCheckViolation := func(
		eventID string,
		state string,
		attempts int,
		lastAttemptAt *time.Time,
		quarantinedAt *time.Time,
		wantConstraint string,
	) {
		t.Helper()
		_, err := pool.Exec(ctx, `
			INSERT INTO human_calling_provider_receipts (
				event_id,
				event_type,
				occurred_at,
				received_at,
				signature_timestamp,
				raw_body,
				state,
				projection_attempts,
				last_attempt_at,
				quarantined_at
			)
			VALUES ($1, 'call.answered', $2, $2, $3, $4, $5, $6, $7, $8)
		`,
			eventID,
			now,
			now.Unix(),
			[]byte("{}"),
			state,
			attempts,
			lastAttemptAt,
			quarantinedAt,
		)
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) ||
			databaseError.ConstraintName != wantConstraint {
			t.Fatalf(
				"insert %s error = %v, want constraint %s",
				eventID,
				err,
				wantConstraint,
			)
		}
	}
	assertCheckViolation(
		"invalid-attempt-visibility",
		"PENDING",
		1,
		nil,
		nil,
		"human_calling_provider_receipts_attempt_visibility_check",
	)
	assertCheckViolation(
		"invalid-quarantine-state",
		"QUARANTINED",
		0,
		nil,
		nil,
		"human_calling_provider_receipts_quarantine_check",
	)
}

func TestCommandLaneMigrationRejectsDuplicateActiveCommands(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		DROP INDEX human_calling_active_call_commands_idx;
		DELETE FROM schema_migrations
		WHERE name = '0009_human_calling_command_lanes.sql';

		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES (
			'00000000-0000-0000-0000-000000000501',
			'duplicate-command-practice',
			'Duplicate Command Practice'
		);
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES (
			'00000000-0000-0000-0000-000000000502',
			'00000000-0000-0000-0000-000000000501',
			'duplicate-command-location',
			'Duplicate Command Location'
		);
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, token_hash, expires_at
		)
		VALUES (
			'00000000-0000-0000-0000-000000000511',
			'duplicate-command-test',
			'00000000-0000-0000-0000-000000000501',
			'00000000-0000-0000-0000-000000000502',
			'duplicate-command-source',
			'duplicate-command-key',
			'\x01',
			'\x02',
			now() + interval '1 hour'
		);
		INSERT INTO human_calling_calls (
			id, handoff_id, practice_id, location_id, state, offer_deadline,
			caller_call_control_id, caller_call_leg_id, call_session_id
		)
		VALUES (
			'00000000-0000-0000-0000-000000000521',
			'00000000-0000-0000-0000-000000000511',
			'00000000-0000-0000-0000-000000000501',
			'00000000-0000-0000-0000-000000000502',
			'OFFERING',
			now() + interval '1 minute',
			'duplicate-command-control',
			'duplicate-command-leg',
			'duplicate-command-session'
		);
		INSERT INTO human_calling_provider_commands (
			call_id, action, target_id, state
		)
		VALUES
			(
				'00000000-0000-0000-0000-000000000521',
				'HANGUP',
				'duplicate-command-one',
				'SENDING'
			),
			(
				'00000000-0000-0000-0000-000000000521',
				'HANGUP',
				'duplicate-command-two',
				'AMBIGUOUS'
			)
	`, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("prepare duplicate active commands: %v", err)
	}

	err := migrations.Apply(ctx, pool)
	if err == nil ||
		!strings.Contains(err.Error(), "cannot enforce one active provider command per Call") {
		t.Fatalf("duplicate active command migration error = %v", err)
	}
}
