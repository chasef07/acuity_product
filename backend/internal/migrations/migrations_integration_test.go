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
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestForwardMigrationsAreRepeatableAndIncludeReviewedRuntimeSchemas(t *testing.T) {
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
	if migrationCount != 18 {
		t.Fatalf("migration count = %d, want 18", migrationCount)
	}
	var staffTransferTableExists bool
	var taskInteractionTableExists bool
	var staffTransferColumnExists bool
	var openRecoveryIndexExists bool
	var voicemailTaskUnique bool
	var taskActivityConstraint string
	if err := pool.QueryRow(ctx, `
		SELECT
			to_regclass('public.human_calling_staff_transfers') IS NOT NULL,
			to_regclass('public.work_task_interactions') IS NOT NULL,
			EXISTS (
				SELECT 1
				FROM information_schema.columns
				WHERE table_schema = 'public'
					AND table_name = 'human_calling_connection_attempts'
					AND column_name = 'staff_transfer_id'
			),
			to_regclass('public.work_tasks_one_open_recovery_need_idx') IS NOT NULL,
			EXISTS (
				SELECT 1
				FROM pg_constraint
				WHERE conrelid = 'public.human_calling_voicemails'::regclass
					AND conname = 'human_calling_voicemails_task_id_key'
					AND contype = 'u'
			),
			(
				SELECT pg_get_constraintdef(oid)
				FROM pg_constraint
				WHERE conrelid = 'public.work_task_activities'::regclass
					AND conname = 'work_task_activities_kind_check'
			)
	`).Scan(
		&staffTransferTableExists,
		&taskInteractionTableExists,
		&staffTransferColumnExists,
		&openRecoveryIndexExists,
		&voicemailTaskUnique,
		&taskActivityConstraint,
	); err != nil {
		t.Fatalf("inspect restored phone workspace schema: %v", err)
	}
	if staffTransferTableExists || !taskInteractionTableExists ||
		staffTransferColumnExists || !openRecoveryIndexExists || voicemailTaskUnique ||
		!strings.Contains(taskActivityConstraint, "INTERACTION_ATTACHED") {
		t.Fatalf(
			"restored phone workspace schema = transfer table:%t interaction table:%t transfer column:%t recovery index:%t voicemail unique:%t",
			staffTransferTableExists,
			taskInteractionTableExists,
			staffTransferColumnExists,
			openRecoveryIndexExists,
			voicemailTaskUnique,
		)
	}
	var greetingRequired bool
	var greetingDefault string
	var rollbackColumnExists bool
	if err := pool.QueryRow(ctx, `
		SELECT
			is_nullable = 'NO',
			column_default
		FROM information_schema.columns
		WHERE table_schema = 'public'
			AND table_name = 'human_calling_location_voice_numbers'
			AND column_name = 'voicemail_greeting'
	`).Scan(&greetingRequired, &greetingDefault); err != nil {
		t.Fatalf("inspect voicemail greeting column: %v", err)
	}
	if !greetingRequired ||
		!strings.Contains(greetingDefault, "Please leave a message after the beep.") {
		t.Fatalf("voicemail greeting column = required:%t default:%q", greetingRequired, greetingDefault)
	}
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
				AND table_name = 'human_calling_location_voice_numbers'
				AND column_name = 'voicemail_greeting_url'
		)
	`).Scan(&rollbackColumnExists); err != nil {
		t.Fatalf("inspect rollback-compatible greeting column: %v", err)
	}
	if !rollbackColumnExists {
		t.Fatal("voicemail greeting migration removed the serving revision's column")
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
	var quarantineIndexValid bool
	var quarantineIndexColumn string
	var quarantineIndexPredicate string
	if err := pool.QueryRow(ctx, `
		SELECT
			index_metadata.indisvalid,
			indexed_column.attname,
			pg_get_expr(
				index_metadata.indpred,
				index_metadata.indrelid
			)
		FROM pg_index index_metadata
		JOIN pg_attribute indexed_column
			ON indexed_column.attrelid = index_metadata.indrelid
			AND indexed_column.attnum = index_metadata.indkey[0]
		WHERE index_metadata.indexrelid =
			'human_calling_quarantined_receipts_idx'::regclass
	`).Scan(
		&quarantineIndexValid,
		&quarantineIndexColumn,
		&quarantineIndexPredicate,
	); err != nil {
		t.Fatalf("inspect quarantined provider receipt index: %v", err)
	}
	if !quarantineIndexValid ||
		quarantineIndexColumn != "quarantined_at" ||
		quarantineIndexPredicate != "(state = 'QUARANTINED'::text)" {
		t.Fatalf(
			"quarantined provider receipt index = valid:%t column:%q predicate:%q",
			quarantineIndexValid,
			quarantineIndexColumn,
			quarantineIndexPredicate,
		)
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
		"human_calling_location_voice_numbers",
		"human_calling_voicemails",
		"access_abita_office_locations",
		"work_tasks",
		"work_task_activities",
		"messaging_location_configurations",
		"messaging_threads",
		"messaging_messages",
		"messaging_attachments",
		"messaging_thread_unreads",
		"messaging_provider_commands",
		"messaging_provider_receipts",
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

func TestRetiredPhoneLedWorkspaceMigrationPreservesRecoveryTasks(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		DROP INDEX work_tasks_one_open_recovery_need_idx;
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES (
			'00000000-0000-0000-0000-000000000901',
			'retired-migration-practice',
			'Retired Migration Practice'
		);
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES (
			'00000000-0000-0000-0000-000000000902',
			'00000000-0000-0000-0000-000000000901',
			'retired-migration-location',
			'Retired Migration Location'
		);
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, token_hash, phone, expires_at
		)
		VALUES
			(
				'00000000-0000-0000-0000-000000000911',
				'retired-migration-test',
				'00000000-0000-0000-0000-000000000901',
				'00000000-0000-0000-0000-000000000902',
				'retired-migration-source-one',
				'retired-migration-key-one',
				'\x01',
				'\x02',
				'+15555550101',
				now() + interval '1 hour'
			),
			(
				'00000000-0000-0000-0000-000000000912',
				'retired-migration-test',
				'00000000-0000-0000-0000-000000000901',
				'00000000-0000-0000-0000-000000000902',
				'retired-migration-source-two',
				'retired-migration-key-two',
				'\x03',
				'\x04',
				'+15555550101',
				now() + interval '1 hour'
			);
		INSERT INTO human_calling_calls (
			id, handoff_id, practice_id, location_id, state, offer_deadline,
			caller_call_control_id, caller_call_leg_id, call_session_id
		)
		VALUES
			(
				'00000000-0000-0000-0000-000000000921',
				'00000000-0000-0000-0000-000000000911',
				'00000000-0000-0000-0000-000000000901',
				'00000000-0000-0000-0000-000000000902',
				'OFFERING',
				now() + interval '1 minute',
				'retired-migration-control-one',
				'retired-migration-leg-one',
				'retired-migration-session-one'
			),
			(
				'00000000-0000-0000-0000-000000000922',
				'00000000-0000-0000-0000-000000000912',
				'00000000-0000-0000-0000-000000000901',
				'00000000-0000-0000-0000-000000000902',
				'OFFERING',
				now() + interval '1 minute',
				'retired-migration-control-two',
				'retired-migration-leg-two',
				'retired-migration-session-two'
			);
		INSERT INTO work_tasks (
			id, practice_id, location_id, call_id, phone, title, state, origin,
			urgency, created_by_kind, created_by_subject, created_at, updated_at,
			recovery_outcome
		)
		VALUES
			(
				'00000000-0000-0000-0000-000000000931',
				'00000000-0000-0000-0000-000000000901',
				'00000000-0000-0000-0000-000000000902',
				'00000000-0000-0000-0000-000000000921',
				'+15555550101',
				'Review missed call',
				'OPEN',
				'MISSED_CALL_RECOVERY',
				'normal',
				'SERVICE',
				'retired-migration-test',
				now() - interval '1 minute',
				now() - interval '1 minute',
				'MISSED_CALL'
			),
			(
				'00000000-0000-0000-0000-000000000932',
				'00000000-0000-0000-0000-000000000901',
				'00000000-0000-0000-0000-000000000902',
				'00000000-0000-0000-0000-000000000922',
				'+15555550101',
				'Review voicemail',
				'OPEN',
				'VOICEMAIL_RECOVERY',
				'normal',
				'SERVICE',
				'retired-migration-test',
				now(),
				now(),
				'VOICEMAIL'
			);
		INSERT INTO work_task_activities (
			id, task_id, task_version, kind, actor_subject, actor_email, occurred_at
		)
		VALUES
			(
				'00000000-0000-0000-0000-000000000941',
				'00000000-0000-0000-0000-000000000931',
				1,
				'TASK_CREATED',
				'retired-migration-test',
				'retired-migration@example.com',
				now() - interval '1 minute'
			),
			(
				'00000000-0000-0000-0000-000000000942',
				'00000000-0000-0000-0000-000000000932',
				1,
				'TASK_CREATED',
				'retired-migration-test',
				'retired-migration@example.com',
				now()
			);
		DELETE FROM schema_migrations
		WHERE name = '0016_phone_led_workspace.sql';
	`, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("seed migration 0015 state: %v", err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply retired phone-led workspace migration: %v", err)
	}
	var taskCount int
	var activityCount int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM work_tasks WHERE id IN (
				'00000000-0000-0000-0000-000000000931',
				'00000000-0000-0000-0000-000000000932'
			)),
			(SELECT count(*) FROM work_task_activities WHERE id IN (
				'00000000-0000-0000-0000-000000000941',
				'00000000-0000-0000-0000-000000000942'
			))
	`).Scan(&taskCount, &activityCount); err != nil {
		t.Fatalf("count preserved recovery evidence: %v", err)
	}
	if taskCount != 2 || activityCount != 2 {
		t.Fatalf(
			"preserved recovery evidence = %d Tasks and %d Activities, want 2 and 2",
			taskCount,
			activityCount,
		)
	}
}

func TestRestoredPhoneWorkspaceMigrationFailsClosedOnDuplicateRecoveryTasks(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	// Recreate the post-rollback production shape, then introduce two distinct
	// accountable recovery Tasks that a migration must not merge or delete.
	if _, err := pool.Exec(ctx, `
		DROP INDEX work_tasks_one_open_recovery_need_idx;
		DROP TABLE work_task_interactions;
		ALTER TABLE human_calling_voicemails
			ADD CONSTRAINT human_calling_voicemails_task_id_key UNIQUE (task_id);
		DELETE FROM schema_migrations
		WHERE name = '0018_restore_phone_led_workspace.sql';

		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES (
			'00000000-0000-0000-0000-000000000951',
			'restore-preflight-practice',
			'Restore Preflight Practice'
		);
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES (
			'00000000-0000-0000-0000-000000000952',
			'00000000-0000-0000-0000-000000000951',
			'restore-preflight-location',
			'Restore Preflight Location'
		);
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, token_hash, phone, expires_at
		)
		VALUES
			(
				'00000000-0000-0000-0000-000000000953',
				'restore-preflight-test',
				'00000000-0000-0000-0000-000000000951',
				'00000000-0000-0000-0000-000000000952',
				'restore-preflight-source-one',
				'restore-preflight-key-one',
				'\x01',
				'\x02',
				'+15555550102',
				now() + interval '1 hour'
			),
			(
				'00000000-0000-0000-0000-000000000954',
				'restore-preflight-test',
				'00000000-0000-0000-0000-000000000951',
				'00000000-0000-0000-0000-000000000952',
				'restore-preflight-source-two',
				'restore-preflight-key-two',
				'\x03',
				'\x04',
				'+15555550102',
				now() + interval '1 hour'
			);
		INSERT INTO human_calling_calls (
			id, handoff_id, practice_id, location_id, state, offer_deadline,
			caller_call_control_id, caller_call_leg_id, call_session_id
		)
		VALUES
			(
				'00000000-0000-0000-0000-000000000955',
				'00000000-0000-0000-0000-000000000953',
				'00000000-0000-0000-0000-000000000951',
				'00000000-0000-0000-0000-000000000952',
				'OFFERING',
				now() + interval '1 minute',
				'restore-preflight-control-one',
				'restore-preflight-leg-one',
				'restore-preflight-session-one'
			),
			(
				'00000000-0000-0000-0000-000000000956',
				'00000000-0000-0000-0000-000000000954',
				'00000000-0000-0000-0000-000000000951',
				'00000000-0000-0000-0000-000000000952',
				'OFFERING',
				now() + interval '1 minute',
				'restore-preflight-control-two',
				'restore-preflight-leg-two',
				'restore-preflight-session-two'
			);
		INSERT INTO work_tasks (
			id, practice_id, location_id, call_id, phone, title, state, origin,
			urgency, created_by_kind, created_by_subject, created_at, updated_at,
			recovery_outcome
		)
		VALUES
			(
				'00000000-0000-0000-0000-000000000961',
				'00000000-0000-0000-0000-000000000951',
				'00000000-0000-0000-0000-000000000952',
				'00000000-0000-0000-0000-000000000955',
				'+15555550102',
				'Review missed call',
				'OPEN',
				'MISSED_CALL_RECOVERY',
				'normal',
				'SERVICE',
				'restore-preflight-test',
				now() - interval '1 minute',
				now() - interval '1 minute',
				'MISSED_CALL'
			),
			(
				'00000000-0000-0000-0000-000000000962',
				'00000000-0000-0000-0000-000000000951',
				'00000000-0000-0000-0000-000000000952',
				'00000000-0000-0000-0000-000000000956',
				'+15555550102',
				'Review voicemail',
				'OPEN',
				'VOICEMAIL_RECOVERY',
				'normal',
				'SERVICE',
				'restore-preflight-test',
				now(),
				now(),
				'VOICEMAIL'
			);
	`, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("seed duplicate recovery Tasks: %v", err)
	}

	err := migrations.Apply(ctx, pool)
	if err == nil || !strings.Contains(
		err.Error(),
		"duplicate open recovery Tasks require audited reconciliation",
	) {
		t.Fatalf("restore preflight error = %v", err)
	}
	var taskCount int
	var interactionTableExists bool
	if err := pool.QueryRow(ctx, `
		SELECT
			count(*),
			to_regclass('public.work_task_interactions') IS NOT NULL
		FROM work_tasks
		WHERE id IN (
			'00000000-0000-0000-0000-000000000961',
			'00000000-0000-0000-0000-000000000962'
		)
		GROUP BY to_regclass('public.work_task_interactions') IS NOT NULL
	`).Scan(&taskCount, &interactionTableExists); err != nil {
		t.Fatalf("inspect failed restoration: %v", err)
	}
	if taskCount != 2 || interactionTableExists {
		t.Fatalf(
			"failed restoration left %d Tasks and interaction table:%t, want 2 and false",
			taskCount,
			interactionTableExists,
		)
	}
}

func TestPhoneLedWorkspaceRollbackFailsClosedBeforeDroppingEvidence(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES (
			'00000000-0000-0000-0000-000000000801',
			'rollback-practice',
			'Rollback Practice'
		);
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES (
			'00000000-0000-0000-0000-000000000802',
			'00000000-0000-0000-0000-000000000801',
			'rollback-location',
			'Rollback Location'
		);
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, token_hash, phone, expires_at
		)
		VALUES (
			'00000000-0000-0000-0000-000000000811',
			'rollback-test',
			'00000000-0000-0000-0000-000000000801',
			'00000000-0000-0000-0000-000000000802',
			'rollback-source',
			'rollback-key',
			'\x01',
			'\x02',
			'+15555550100',
			now() + interval '1 hour'
		);
		INSERT INTO human_calling_calls (
			id, handoff_id, practice_id, location_id, state, offer_deadline,
			caller_call_control_id, caller_call_leg_id, call_session_id
		)
		VALUES (
			'00000000-0000-0000-0000-000000000821',
			'00000000-0000-0000-0000-000000000811',
			'00000000-0000-0000-0000-000000000801',
			'00000000-0000-0000-0000-000000000802',
			'OFFERING',
			now() + interval '1 minute',
			'rollback-control',
			'rollback-leg',
			'rollback-session'
		);
		INSERT INTO work_tasks (
			id, practice_id, location_id, call_id, phone, title, state, origin,
			urgency, created_by_kind, created_by_subject, created_by_email,
			created_at, updated_at
		)
		VALUES (
			'00000000-0000-0000-0000-000000000831',
			'00000000-0000-0000-0000-000000000801',
			'00000000-0000-0000-0000-000000000802',
			'00000000-0000-0000-0000-000000000821',
			'+15555550100',
			'Rollback follow-up',
			'OPEN',
			'HUMAN_CALL_FOLLOW_UP',
			'normal',
			'HUMAN',
			'rollback-user',
			'rollback@example.com',
			now(),
			now()
		)
	`, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("seed rollback fixture: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		CREATE TABLE human_calling_staff_transfers (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			call_id uuid NOT NULL REFERENCES human_calling_calls(id) ON DELETE CASCADE,
			practice_id uuid NOT NULL REFERENCES access_practices(id),
			location_id uuid NOT NULL,
			requested_by_subject text NOT NULL,
			requested_by_session_id text NOT NULL,
			recipient_subject text NOT NULL,
			recipient_session_id text,
			handoff_note text NOT NULL CHECK (char_length(handoff_note) <= 500),
			state text NOT NULL CHECK (state IN (
				'REQUESTED', 'ACCEPTED', 'COMPLETED', 'DECLINED',
				'CANCELLED', 'EXPIRED', 'FAILED'
			)),
			expires_at timestamptz NOT NULL,
			completed_at timestamptz,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			FOREIGN KEY (practice_id, location_id)
				REFERENCES access_locations(practice_id, id),
			CHECK (requested_by_subject <> recipient_subject)
		);
		CREATE UNIQUE INDEX human_calling_one_active_staff_transfer_idx
			ON human_calling_staff_transfers (call_id)
			WHERE state IN ('REQUESTED', 'ACCEPTED');
		CREATE INDEX human_calling_staff_transfer_recipient_idx
			ON human_calling_staff_transfers (recipient_subject, state, expires_at);
		ALTER TABLE human_calling_connection_attempts
			ADD COLUMN staff_transfer_id uuid
				REFERENCES human_calling_staff_transfers(id);
		CREATE UNIQUE INDEX human_calling_staff_transfer_attempt_idx
			ON human_calling_connection_attempts (staff_transfer_id)
			WHERE staff_transfer_id IS NOT NULL;
		DELETE FROM schema_migrations
		WHERE name = '0017_revert_phone_led_workspace.sql';
		INSERT INTO human_calling_staff_transfers (
			id, call_id, practice_id, location_id, requested_by_subject,
			requested_by_session_id, recipient_subject, handoff_note, state,
			expires_at
		)
		VALUES (
			'00000000-0000-0000-0000-000000000841',
			'00000000-0000-0000-0000-000000000821',
			'00000000-0000-0000-0000-000000000801',
			'00000000-0000-0000-0000-000000000802',
			'rollback-requester',
			'rollback-requester-session',
			'rollback-recipient',
			'preserve this evidence',
			'CANCELLED',
			now()
		)
	`, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("seed rollback blocker: %v", err)
	}

	err := migrations.Apply(ctx, pool)
	if err == nil || !strings.Contains(err.Error(), "staff transfer evidence exists") {
		t.Fatalf("staff transfer rollback error = %v", err)
	}
	var transferCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM human_calling_staff_transfers
	`).Scan(&transferCount); err != nil {
		t.Fatalf("read preserved transfer evidence: %v", err)
	}
	if transferCount != 1 {
		t.Fatalf("preserved transfer evidence count = %d, want 1", transferCount)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM human_calling_staff_transfers`); err != nil {
		t.Fatalf("remove reviewed rollback blocker: %v", err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply reviewed phone-led workspace rollback: %v", err)
	}
	var interactionTableExists bool
	if err := pool.QueryRow(ctx, `
		SELECT to_regclass('public.work_task_interactions') IS NOT NULL
	`).Scan(&interactionTableExists); err != nil {
		t.Fatalf("inspect completed rollback: %v", err)
	}
	if interactionTableExists {
		t.Fatal("reviewed phone-led workspace rollback retained interaction schema")
	}
}

func TestTelnyxNativeVoicemailMigrationPreservesLegacyEvidence(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES (
			'00000000-0000-0000-0000-000000000701',
			'legacy-voicemail-practice',
			'Legacy Voicemail Practice'
		);
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES (
			'00000000-0000-0000-0000-000000000702',
			'00000000-0000-0000-0000-000000000701',
			'legacy-voicemail-location',
			'Legacy Voicemail Location'
		);
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, token_hash, phone,
			expires_at, consumed_at, created_at
		)
		VALUES (
			'00000000-0000-0000-0000-000000000711',
			'legacy-voicemail-test',
			'00000000-0000-0000-0000-000000000701',
			'00000000-0000-0000-0000-000000000702',
			'legacy-voicemail-source',
			'legacy-voicemail-key',
			'\x01',
			'\x02',
			'+15555550100',
			'2026-08-03T12:05:00Z',
			'2026-08-03T12:00:00Z',
			'2026-08-03T12:00:00Z'
		);
		INSERT INTO human_calling_calls (
			id, handoff_id, practice_id, location_id, state, offer_deadline,
			caller_call_control_id, caller_call_leg_id, call_session_id,
			ended_at, created_at, updated_at
		)
		VALUES (
			'00000000-0000-0000-0000-000000000721',
			'00000000-0000-0000-0000-000000000711',
			'00000000-0000-0000-0000-000000000701',
			'00000000-0000-0000-0000-000000000702',
			'VOICEMAIL',
			'2026-08-03T12:01:00Z',
			'legacy-voicemail-control',
			'legacy-voicemail-leg',
			'legacy-voicemail-session',
			'2026-08-03T12:00:12Z',
			'2026-08-03T12:00:00Z',
			'2026-08-03T12:00:12Z'
		);
		INSERT INTO work_tasks (
			id, practice_id, location_id, call_id, phone, title, state,
			created_by_subject, created_at, updated_at, origin,
			created_by_kind, recovery_outcome
		)
		VALUES (
			'00000000-0000-0000-0000-000000000731',
			'00000000-0000-0000-0000-000000000701',
			'00000000-0000-0000-0000-000000000702',
			'00000000-0000-0000-0000-000000000721',
			'+15555550100',
			'Review voicemail',
			'OPEN',
			'system:human-calling',
			'2026-08-03T12:00:12Z',
			'2026-08-03T12:00:12Z',
			'VOICEMAIL_RECOVERY',
			'SERVICE',
			'VOICEMAIL'
		);
		INSERT INTO human_calling_voicemails (
			call_id, practice_id, location_id, task_id, outcome, audio_state,
			provider_recording_id, recording_started_at, recording_ended_at,
			duration_millis, object_key, content_type, byte_size, copy_attempts,
			copied_at, created_at, updated_at
		)
		VALUES (
			'00000000-0000-0000-0000-000000000721',
			'00000000-0000-0000-0000-000000000701',
			'00000000-0000-0000-0000-000000000702',
			'00000000-0000-0000-0000-000000000731',
			'VOICEMAIL',
			'READY',
			'legacy-provider-recording',
			'2026-08-03T12:00:00Z',
			'2026-08-03T12:00:12Z',
			12000,
			'legacy/voicemail.wav',
			'audio/wav',
			17,
			1,
			'2026-08-03T12:00:13Z',
			'2026-08-03T12:00:00Z',
			'2026-08-03T12:00:13Z'
		);
		DELETE FROM schema_migrations
		WHERE name = '0015_telnyx_native_voicemail.sql';
	`, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("prepare legacy voicemail evidence: %v", err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("reapply Telnyx-native voicemail migration: %v", err)
	}
	var providerRecordingID, objectKey, contentType string
	var byteSize int64
	var copyAttempts int
	if err := pool.QueryRow(ctx, `
		SELECT provider_recording_id, object_key, content_type, byte_size,
			copy_attempts
		FROM human_calling_voicemails
		WHERE call_id = '00000000-0000-0000-0000-000000000721'
	`).Scan(
		&providerRecordingID,
		&objectKey,
		&contentType,
		&byteSize,
		&copyAttempts,
	); err != nil {
		t.Fatalf("read preserved legacy voicemail evidence: %v", err)
	}
	if providerRecordingID != "legacy-provider-recording" ||
		objectKey != "legacy/voicemail.wav" ||
		contentType != "audio/wav" ||
		byteSize != 17 ||
		copyAttempts != 1 {
		t.Fatalf(
			"legacy voicemail changed: recording=%q object=%q type=%q bytes=%d attempts=%d",
			providerRecordingID,
			objectKey,
			contentType,
			byteSize,
			copyAttempts,
		)
	}
}

func TestRejectedProviderLegMigrationBackfillsFailedHandoffs(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		DROP TABLE human_calling_rejected_provider_legs;
		DELETE FROM schema_migrations
		WHERE name = '0013_rejected_provider_legs.sql';
		INSERT INTO human_calling_provider_receipts (
			event_id,
			event_type,
			occurred_at,
			received_at,
			signature_timestamp,
			raw_body,
			state,
			projection_error_code,
			projected_at
		)
		VALUES (
			'backfilled-rejected-initiation',
			'call.initiated',
			'2026-07-31T12:00:00Z',
			'2026-07-31T12:00:01Z',
			1,
			convert_to(
				'{"data":{"payload":{"call_control_id":"backfilled-control","call_leg_id":"backfilled-leg","call_session_id":"backfilled-session"}}}',
				'UTF8'
			),
			'FAILED',
			'HANDOFF_REJECTED',
			'2026-07-31T12:00:02Z'
		)
	`); err != nil {
		t.Fatalf("prepare rejected provider leg backfill: %v", err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply rejected provider leg migration: %v", err)
	}
	var eventID string
	if err := pool.QueryRow(ctx, `
		SELECT initiated_event_id
		FROM human_calling_rejected_provider_legs
		WHERE call_control_id = 'backfilled-control'
			AND call_leg_id = 'backfilled-leg'
			AND call_session_id = 'backfilled-session'
	`).Scan(&eventID); err != nil {
		t.Fatalf("read backfilled rejected provider leg: %v", err)
	}
	if eventID != "backfilled-rejected-initiation" {
		t.Fatalf("backfilled rejected provider event = %q", eventID)
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
	prepareDuplicateActiveCommands(t, pool)

	err := migrations.Apply(ctx, pool)
	if err == nil ||
		!strings.Contains(err.Error(), "cannot enforce one active provider command per Call") {
		t.Fatalf("duplicate active command migration error = %v", err)
	}
}

func prepareDuplicateActiveCommands(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
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
}

func TestCommandLaneMigrationRepairsInvalidConcurrentIndex(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	prepareDuplicateActiveCommands(t, pool)

	if _, err := pool.Exec(ctx, `
		CREATE UNIQUE INDEX CONCURRENTLY human_calling_active_call_commands_idx
		ON human_calling_provider_commands (call_id)
		WHERE call_id IS NOT NULL
			AND state IN ('SENDING', 'AMBIGUOUS')
	`); err == nil {
		t.Fatal("duplicate active commands unexpectedly produced a unique index")
	}
	var valid bool
	if err := pool.QueryRow(ctx, `
		SELECT indisvalid
		FROM pg_index
		WHERE indexrelid = 'human_calling_active_call_commands_idx'::regclass
	`).Scan(&valid); err != nil {
		t.Fatalf("inspect failed concurrent index: %v", err)
	}
	if valid {
		t.Fatal("failed concurrent index is unexpectedly valid")
	}
	if _, err := pool.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'FAILED'
		WHERE target_id = 'duplicate-command-two'
	`); err != nil {
		t.Fatalf("reconcile duplicate active command: %v", err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("repair failed concurrent index: %v", err)
	}
	assertActiveCommandIndexValid(t, pool)
}

func TestCommandLaneMigrationKeepsValidIndexWhenRecordingIsRetried(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	var indexBefore uint32
	if err := pool.QueryRow(ctx, `
		SELECT 'human_calling_active_call_commands_idx'::regclass::oid
	`).Scan(&indexBefore); err != nil {
		t.Fatalf("read active provider-command index identity: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM schema_migrations
		WHERE name = '0009_human_calling_command_lanes.sql'
	`); err != nil {
		t.Fatalf("remove command-lane migration marker: %v", err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("record valid concurrent index migration: %v", err)
	}
	var indexAfter uint32
	if err := pool.QueryRow(ctx, `
		SELECT 'human_calling_active_call_commands_idx'::regclass::oid
	`).Scan(&indexAfter); err != nil {
		t.Fatalf("read recovered provider-command index identity: %v", err)
	}
	if indexAfter != indexBefore {
		t.Fatalf(
			"valid provider-command index was rebuilt: before=%d after=%d",
			indexBefore,
			indexAfter,
		)
	}
}

func TestCommandLaneMigrationDoesNotBlockProviderCommandWrites(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	prepareDuplicateActiveCommands(t, pool)
	if _, err := pool.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET state = 'FAILED'
		WHERE target_id = 'duplicate-command-two'
	`); err != nil {
		t.Fatalf("prepare one active provider command: %v", err)
	}

	blockingTransaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin old provider-command write: %v", err)
	}
	defer func() { _ = blockingTransaction.Rollback(ctx) }()
	if _, err := blockingTransaction.Exec(ctx, `
		UPDATE human_calling_provider_commands
		SET attempts = attempts
		WHERE target_id = 'duplicate-command-one'
	`); err != nil {
		t.Fatalf("hold old provider-command write: %v", err)
	}

	applyDone := make(chan error, 1)
	go func() {
		applyDone <- migrations.Apply(ctx, pool)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var building bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE pid <> pg_backend_pid()
					AND state = 'active'
					AND query LIKE
						'CREATE UNIQUE INDEX CONCURRENTLY human_calling_active_call_commands_idx%'
			)
		`).Scan(&building); err != nil {
			t.Fatalf("inspect concurrent index build: %v", err)
		}
		if building {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("concurrent index build did not wait for the old writer")
		}
		time.Sleep(10 * time.Millisecond)
	}

	writeContext, cancelWrite := context.WithTimeout(ctx, time.Second)
	defer cancelWrite()
	if _, err := pool.Exec(writeContext, `
		UPDATE human_calling_provider_commands
		SET attempts = attempts + 1
		WHERE target_id = 'duplicate-command-two'
	`); err != nil {
		t.Fatalf("provider-command write blocked by concurrent index: %v", err)
	}
	if err := blockingTransaction.Commit(ctx); err != nil {
		t.Fatalf("commit old provider-command write: %v", err)
	}

	select {
	case err := <-applyDone:
		if err != nil {
			t.Fatalf("finish concurrent command-lane migration: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent command-lane migration did not finish")
	}
	assertActiveCommandIndexValid(t, pool)
}

func assertActiveCommandIndexValid(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var valid, unique bool
	if err := pool.QueryRow(context.Background(), `
		SELECT indisvalid, indisunique
		FROM pg_index
		WHERE indexrelid = 'human_calling_active_call_commands_idx'::regclass
	`).Scan(&valid, &unique); err != nil {
		t.Fatalf("inspect active provider-command index: %v", err)
	}
	if !valid || !unique {
		t.Fatalf("active provider-command index valid=%t unique=%t", valid, unique)
	}
}
