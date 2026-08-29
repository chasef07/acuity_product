package migrations_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	practiceID = "00000000-0000-0000-0000-000000000101"
	locationID = "00000000-0000-0000-0000-000000000102"
	handoffID  = "00000000-0000-0000-0000-000000000103"
	callID     = "00000000-0000-0000-0000-000000000104"
	attemptID  = "00000000-0000-0000-0000-000000000105"
)

func TestForwardMigrationsAreRepeatableAndExposeCurrentSchema(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("repeat migrations: %v", err)
	}

	var migrationCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 40 {
		t.Fatalf("migration count = %d, want 40", migrationCount)
	}
	var pendingInteractionReceiptIndex string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public'
			AND indexname = 'ai_interaction_pending_receipts_idx'
	`).Scan(&pendingInteractionReceiptIndex); err != nil {
		t.Fatalf("read pending AI Interaction receipt index: %v", err)
	}
	for _, fragment := range []string{"kind", "START", "OUTCOME_CHECKPOINT", "CLOSEOUT"} {
		if !strings.Contains(pendingInteractionReceiptIndex, fragment) {
			t.Errorf("pending AI Interaction receipt index omits %q: %s",
				fragment, pendingInteractionReceiptIndex)
		}
	}
	var recordingRetentionIndex string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public'
			AND indexname = 'human_calling_call_recordings_retention_idx'
	`).Scan(&recordingRetentionIndex); err != nil {
		t.Fatalf("read connected recording retention index: %v", err)
	}
	for _, fragment := range []string{
		"(content_expires_at, next_deletion_attempt_at, updated_at, call_id)",
		"audio_state = 'READY'::text",
	} {
		if !strings.Contains(recordingRetentionIndex, fragment) {
			t.Errorf("connected recording retention index omits %q: %s",
				fragment, recordingRetentionIndex)
		}
	}
	var recordingReconciliationIndex string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public'
			AND indexname = 'human_calling_call_recordings_reconciliation_idx'
	`).Scan(&recordingReconciliationIndex); err != nil {
		t.Fatalf("read connected recording reconciliation index: %v", err)
	}
	for _, fragment := range []string{
		"(next_reconciliation_attempt_at, updated_at, call_id)",
		"audio_state = 'PROCESSING'::text",
	} {
		if !strings.Contains(recordingReconciliationIndex, fragment) {
			t.Errorf("connected recording reconciliation index omits %q: %s",
				fragment, recordingReconciliationIndex)
		}
	}
	var staleCommandIndex string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public'
			AND indexname = 'human_calling_stale_leg_commands_idx'
	`).Scan(&staleCommandIndex); err != nil {
		t.Fatalf("read stale CallLeg command index: %v", err)
	}
	for _, fragment := range []string{
		"(call_leg_id, created_at, id)",
		"INCLUDE (action, payload)",
		"call_leg_id IS NOT NULL",
		"state = ANY (ARRAY['SENDING'::text, 'SENT'::text, 'AMBIGUOUS'::text])",
	} {
		if !strings.Contains(staleCommandIndex, fragment) {
			t.Errorf("stale CallLeg command index omits %q: %s", fragment, staleCommandIndex)
		}
	}
	var messageThreadActivityIndex string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public'
			AND indexname = 'messaging_threads_phone_activity_idx'
	`).Scan(&messageThreadActivityIndex); err != nil {
		t.Fatalf("read Message Thread activity index: %v", err)
	}
	if !strings.Contains(
		messageThreadActivityIndex,
		"(practice_id, location_id, external_phone, id)",
	) {
		t.Fatalf("Message Thread activity index = %s", messageThreadActivityIndex)
	}

	for _, relation := range []string{
		"ai_interaction_attention",
		"ai_interaction_receipts",
		"ai_interactions",
		"access_calling_scopes",
		"access_operational_scopes",
		"access_grants",
		"access_grant_locations",
		"human_calling_handoffs",
		"human_calling_calls",
		"human_calling_call_legs",
		"human_calling_call_recordings",
		"human_calling_provider_commands",
		"human_calling_provider_receipts",
		"human_calling_projected_facts",
		"human_calling_rejected_provider_legs",
		"human_calling_timeline",
		"human_calling_location_voice_numbers",
		"human_calling_outbound_voice_fallbacks",
		"human_calling_voicemails",
		"work_task_interactions",
		"work_recovery_reconciliation_queue",
		"work_recovery_resolution_checkpoints",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, relation).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("current relation %s is missing", relation)
		}
	}
	for _, relation := range []string{
		"access_invitation_locations",
		"access_invitations",
		"access_support_sessions",
		"human_calling_connection_attempts",
		"human_calling_recordings",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, relation).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("legacy relation %s still exists", relation)
		}
	}

	var legacyColumns, commandLegColumns int
	if err := pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE
				(table_name = 'human_calling_calls' AND column_name IN (
					'state', 'offer_deadline', 'connection_deadline', 'claimant_subject',
					'winner_subject', 'claimant_session_id', 'current_attempt_id',
					'caller_call_control_id', 'caller_call_leg_id', 'call_session_id',
					'destination_call_control_id', 'destination_call_leg_id', 'connected_at'
			)) OR (table_name = 'human_calling_handoffs' AND column_name = 'token_hash')
			OR (table_name = 'access_audit_events' AND column_name = 'support_session_id')
			),
			count(*) FILTER (WHERE table_name = 'human_calling_provider_commands'
				AND column_name IN ('call_leg_id', 'peer_call_leg_id'))
		FROM information_schema.columns
		WHERE table_schema = 'public'
	`).Scan(&legacyColumns, &commandLegColumns); err != nil {
		t.Fatal(err)
	}
	if legacyColumns != 0 || commandLegColumns != 2 {
		t.Fatalf("legacy calling columns = %d, command CallLeg columns = %d", legacyColumns, commandLegColumns)
	}
	var legacyVoicemailColumns int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'human_calling_voicemails'
			AND column_name IN (
				'provider_recording_url', 'object_key', 'content_type', 'byte_size',
				'copy_attempts', 'next_copy_at', 'copied_at'
			)
	`).Scan(&legacyVoicemailColumns); err != nil {
		t.Fatal(err)
	}
	if legacyVoicemailColumns != 0 {
		t.Fatalf("legacy voicemail copy columns = %d, want 0", legacyVoicemailColumns)
	}
}

func TestRetiredSummaryReceiptMigrationPreservesAuditRowsOutsideBacklog(t *testing.T) {
	pool := testdb.OpenThrough(t, "0039_automatic_task_acknowledgements.sql")
	ctx := context.Background()
	const (
		historyPractice = "00000000-0000-0000-0000-000000004001"
		historyLocation = "00000000-0000-0000-0000-000000004002"
		historyReceipt  = "00000000-0000-0000-0000-000000004003"
	)
	if _, err := pool.Exec(ctx, `
		DROP INDEX ai_interaction_pending_receipts_idx;
		CREATE INDEX ai_interaction_pending_receipts_idx
			ON ai_interaction_receipts (received_at, id)
			WHERE state = 'PENDING'
				AND kind IN ('START', 'OUTCOME_CHECKPOINT', 'CLOSEOUT', 'SUMMARY');
	`); err != nil {
		t.Fatalf("restore unsafe pending receipt index: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES ($1, 'summary-history', 'Summary History')
	`, historyPractice); err != nil {
		t.Fatalf("seed historical SUMMARY Practice: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES ($2, $1, 'main', 'Main')
	`, historyPractice, historyLocation); err != nil {
		t.Fatalf("seed historical SUMMARY Location: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO ai_interaction_receipts (
			id, service_subject, practice_id, location_id, source_call_id,
			kind, payload_fingerprint, payload
		) VALUES (
			$3, 'abita-agent', $1, $2, 'historical-summary', 'SUMMARY',
			decode(repeat('01', 32), 'hex'), '{"historical":true}'::jsonb
		)
	`, historyPractice, historyLocation, historyReceipt); err != nil {
		t.Fatalf("seed historical SUMMARY receipt: %v", err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply retired SUMMARY backlog migration: %v", err)
	}

	var receiptState, indexDefinition string
	if err := pool.QueryRow(ctx, `
		SELECT state FROM ai_interaction_receipts WHERE id = $1
	`, historyReceipt).Scan(&receiptState); err != nil {
		t.Fatalf("read historical SUMMARY receipt: %v", err)
	}
	if receiptState != "PENDING" {
		t.Fatalf("historical SUMMARY receipt state = %q, want PENDING", receiptState)
	}
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public'
			AND indexname = 'ai_interaction_pending_receipts_idx'
	`).Scan(&indexDefinition); err != nil {
		t.Fatalf("read upgraded AI Interaction receipt index: %v", err)
	}
	for _, fragment := range []string{"START", "OUTCOME_CHECKPOINT", "CLOSEOUT"} {
		if !strings.Contains(indexDefinition, fragment) {
			t.Errorf("upgraded pending receipt index omits %q: %s", fragment, indexDefinition)
		}
	}
	if strings.Contains(indexDefinition, "SUMMARY") {
		t.Fatalf("upgraded pending receipt index includes retired SUMMARY: %s", indexDefinition)
	}
}

func TestMessageCreatorKindSupportsOverlappingOldRevision(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 20, 15, 0, 0, 0, time.UTC)
	const (
		creatorPractice = "00000000-0000-0000-0000-000000003901"
		creatorLocation = "00000000-0000-0000-0000-000000003902"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES ($1, 'message-creator-constraint', 'Message Creator Constraint')
	`, creatorPractice); err != nil {
		t.Fatalf("seed Message creator Practice: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES ($2, $1, 'main', 'Main')
	`, creatorPractice, creatorLocation); err != nil {
		t.Fatalf("seed Message creator Location: %v", err)
	}
	var threadID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO messaging_threads (
			practice_id, location_id, office_phone, external_phone,
			created_at, updated_at
		) VALUES ($1, $2, '+17275550100', '+17275550199', $3, $3)
		RETURNING id::text
	`, creatorPractice, creatorLocation, now).Scan(&threadID); err != nil {
		t.Fatalf("seed Message creator Thread: %v", err)
	}
	var outboundCreatorKind string
	if err := pool.QueryRow(ctx, `
		INSERT INTO messaging_messages (
			thread_id, practice_id, location_id, direction, body,
			sender, destination, delivery_state, created_by_subject,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, 'OUTBOUND', 'Creator is required',
			'+17275550100', '+17275550199', 'SENDING', 'staff-subject',
			$4, $4
		)
		RETURNING created_by_kind
	`, threadID, creatorPractice, creatorLocation, now).Scan(
		&outboundCreatorKind,
	); err != nil {
		t.Fatalf("insert old-revision outbound Message: %v", err)
	}
	if outboundCreatorKind != "HUMAN" {
		t.Fatalf("old-revision outbound creator kind = %q, want HUMAN", outboundCreatorKind)
	}
	var serviceCreatorKind string
	if err := pool.QueryRow(ctx, `
		INSERT INTO messaging_messages (
			thread_id, practice_id, location_id, direction, body,
			sender, destination, delivery_state, created_by_kind,
			created_by_subject, created_at, updated_at
		) VALUES (
			$1, $2, $3, 'OUTBOUND', 'Automatic acknowledgement',
			'+17275550100', '+17275550199', 'SENDING', 'SERVICE',
			'service:task-acknowledgement', $4, $4
		)
		RETURNING created_by_kind
	`, threadID, creatorPractice, creatorLocation, now).Scan(
		&serviceCreatorKind,
	); err != nil {
		t.Fatalf("insert service-authored outbound Message: %v", err)
	}
	if serviceCreatorKind != "SERVICE" {
		t.Fatalf("service-authored outbound creator kind = %q, want SERVICE", serviceCreatorKind)
	}

	var inboundCreatorKind *string
	if err := pool.QueryRow(ctx, `
		INSERT INTO messaging_messages (
			thread_id, practice_id, location_id, direction, body,
			sender, destination, delivery_state, provider_message_id,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, 'INBOUND', 'Legacy inbound Message',
			'+17275550199', '+17275550100', 'DELIVERED',
			'legacy-inbound-provider-message', $4, $4
		)
		RETURNING created_by_kind
	`, threadID, creatorPractice, creatorLocation, now).Scan(
		&inboundCreatorKind,
	); err != nil {
		t.Fatalf("insert old-revision inbound Message: %v", err)
	}
	if inboundCreatorKind != nil {
		t.Fatalf("old-revision inbound creator kind = %q, want NULL", *inboundCreatorKind)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO messaging_messages (
			thread_id, practice_id, location_id, direction, body,
			sender, destination, delivery_state, created_at, updated_at
		) VALUES (
			$1, $2, $3, 'OUTBOUND', 'Creator is required',
			'+17275550100', '+17275550199', 'SENDING', $4, $4
		)
	`, threadID, creatorPractice, creatorLocation, now)
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "23514" {
		t.Fatalf("outbound Message without creator evidence error = %v, want check violation", err)
	}
}

func TestConcurrentStaffDialMigrationEnforcesActiveCommandLanes(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 19, 19, 0, 0, 0, time.UTC)
	const (
		practice  = "00000000-0000-0000-0000-000000003801"
		location  = "00000000-0000-0000-0000-000000003802"
		call      = "00000000-0000-0000-0000-000000003803"
		firstLeg  = "00000000-0000-0000-0000-000000003804"
		secondLeg = "00000000-0000-0000-0000-000000003805"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES ($1, 'concurrent-dial-migration', 'Concurrent Dial Migration')
	`, practice); err != nil {
		t.Fatalf("seed concurrent Staff Dial Practice: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES ($2, $1, 'office', 'Office')
	`, practice, location); err != nil {
		t.Fatalf("seed concurrent Staff Dial Location: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_calls (
			id, practice_id, location_id, direction, entry_point,
			created_at, updated_at
		) VALUES ($3, $1, $2, 'INBOUND', 'STANDALONE', $4, $4)
	`, practice, location, call, now); err != nil {
		t.Fatalf("seed concurrent Staff Dial Call: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_call_legs (
			id, call_id, role, sequence, staff_subject, state,
			created_at, updated_at
		) VALUES
			($2, $1, 'STAFF', 1, 'staff-one', 'PENDING', $4, $4),
			($3, $1, 'STAFF', 1, 'staff-two', 'PENDING', $4, $4)
	`, call, firstLeg, secondLeg, now); err != nil {
		t.Fatalf("seed concurrent Staff Dial CallLegs: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			call_id, call_leg_id, action, target_id, state,
			created_at, next_attempt_at, updated_at
		) VALUES
			($1, $2, 'DIAL_STAFF', 'first', 'SENDING', $4, $4, $4),
			($1, $3, 'DIAL_STAFF', 'second', 'SENDING', $4, $4, $4)
	`, call, firstLeg, secondLeg, now); err != nil {
		t.Fatalf("allow independent active Staff Dials: %v", err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			call_id, call_leg_id, action, target_id, state,
			created_at, next_attempt_at, updated_at
		) VALUES ($1, $2, 'DIAL_STAFF', 'duplicate', 'AMBIGUOUS', $3, $3, $3)
	`, call, firstLeg, now)
	assertUniqueViolation(t, err, "duplicate active Staff Dial for one CallLeg")

	if _, err := pool.Exec(ctx, `
		UPDATE human_calling_provider_commands SET state = 'SENT'
		WHERE call_id = $1
	`, call); err != nil {
		t.Fatalf("finish Staff Dials: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			call_id, call_leg_id, action, target_id, state,
			created_at, next_attempt_at, updated_at
		) VALUES ($1, $2, 'STOP_RING_WINDOW', 'stop', 'SENDING', $3, $3, $3)
	`, call, firstLeg, now); err != nil {
		t.Fatalf("seed active non-Dial command: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			call_id, call_leg_id, action, target_id, state,
			created_at, next_attempt_at, updated_at
		) VALUES ($1, $2, 'BRIDGE', 'bridge', 'SENDING', $3, $3, $3)
	`, call, secondLeg, now)
	assertUniqueViolation(t, err, "second active non-Dial command for one Call")
}

func assertUniqueViolation(t *testing.T, err error, operation string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23505" {
		t.Fatalf("%s error = %v, want PostgreSQL unique violation", operation, err)
	}
}

func TestQuietQueueMigrationQueuesOnlyExistingOpenRecoveryKeys(t *testing.T) {
	pool := testdb.OpenThrough(t, "0034_ai_interaction_attention.sql")
	ctx := context.Background()
	now := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	const (
		queuePracticeID = "00000000-0000-0000-0000-000000000401"
		queueLocationID = "00000000-0000-0000-0000-000000000402"
		queueHandoffID  = "00000000-0000-0000-0000-000000000403"
		queueCallID     = "00000000-0000-0000-0000-000000000404"
		queuePhone      = "+15555550404"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES ($1, 'quiet-queue-migration', 'Quiet Queue Migration')
	`, queuePracticeID); err != nil {
		t.Fatalf("seed pre-migration Practice: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES ($2, $1, 'office', 'Office')
	`, queuePracticeID, queueLocationID); err != nil {
		t.Fatalf("seed pre-migration Location: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, phone, phone_source,
			display_name, name_source, transfer_reason, reason_source,
			expires_at, consumed_at, created_at
		) VALUES (
			$3, 'migration-service', $1, $2, 'source-call', 'source-attempt',
			'fingerprint'::bytea, $4, 'Abita', 'Migration caller', 'Abita',
			'Missed call', 'Abita AI', $5, $5, $5
		)
	`, queuePracticeID, queueLocationID, queueHandoffID,
		queuePhone, now); err != nil {
		t.Fatalf("seed pre-migration Handoff: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_calls (
			id, source_handoff_id, practice_id, location_id, disposition_at,
			disposition_actor_subject, disposition_outcome, terminal_outcome,
			caller_phone, ended_at, created_at, updated_at
		) VALUES (
			$4, $3, $1, $2, $6, 'migration-service', 'FOLLOW_UP_REQUIRED',
			'FOLLOW_UP_REQUIRED', $5, $6, $6, $6
		)
	`, queuePracticeID, queueLocationID, queueHandoffID, queueCallID,
		queuePhone, now); err != nil {
		t.Fatalf("seed pre-migration Call: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO work_tasks (
			practice_id, location_id, call_id, phone, title, state, origin,
			urgency, created_by_kind, created_by_subject, created_at,
			recovery_outcome, updated_at
		) VALUES (
			$1, $2, $3, $4, 'Return missed call', 'OPEN',
			'MISSED_CALL_RECOVERY', 'normal', 'SERVICE', 'human-calling',
			$5, 'MISSED_CALL', $5
		)
	`, queuePracticeID, queueLocationID, queueCallID, queuePhone, now); err != nil {
		t.Fatalf("seed pre-migration recovery Task: %v", err)
	}

	if err := migrations.ApplyThrough(ctx, pool, "0036_quiet_staff_queue.sql"); err != nil {
		t.Fatalf("apply quiet Queue migration: %v", err)
	}
	var queued int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM work_recovery_reconciliation_queue
		WHERE practice_id = $1 AND phone = $2
	`, queuePracticeID, queuePhone).Scan(&queued); err != nil {
		t.Fatalf("read queued recovery key: %v", err)
	}
	if queued != 1 {
		t.Fatalf("queued recovery keys = %d, want 1", queued)
	}
}

func TestGoogleOnlyAccessMigrationConvertsCompatibleLegacyMembership(t *testing.T) {
	pool := testdb.OpenThrough(t, "0026_ai_interactions.sql")
	ctx := context.Background()
	const (
		legacyPracticeID   = "00000000-0000-0000-0000-000000000301"
		legacyInvitationID = "00000000-0000-0000-0000-000000000302"
		legacyGrantID      = "00000000-0000-0000-0000-000000000303"
		legacyMembershipID = "00000000-0000-0000-0000-000000000304"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES ($1, 'legacy-practice', 'Legacy Practice')
	`, legacyPracticeID); err != nil {
		t.Fatalf("seed legacy Practice: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_invitations (
			id, provisioning_key, practice_id, token_hash, email, role,
			location_scope, expires_at, accepted_at, accepted_by_subject
		) VALUES (
			$2, 'legacy-user', $1, decode(repeat('01', 32), 'hex'),
			'legacy@example.com', 'STAFF', 'ALL', now() + interval '1 day',
			now(), 'google-subject'
		)
	`, legacyPracticeID, legacyInvitationID); err != nil {
		t.Fatalf("seed legacy invitation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_grants (
			id, provisioning_key, practice_id, email, role, location_scope
		) VALUES (
			$2, 'google-user', $1, 'legacy@example.com', 'STAFF', 'ALL'
		)
	`, legacyPracticeID, legacyGrantID); err != nil {
		t.Fatalf("seed matching Access Grant: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_memberships (
			id, user_subject, email, practice_id, role, location_scope, invitation_id
		) VALUES (
			$3, 'google-subject', 'legacy@example.com', $1, 'STAFF', 'ALL', $2
		)
	`, legacyPracticeID, legacyInvitationID, legacyMembershipID); err != nil {
		t.Fatalf("seed compatible legacy Membership: %v", err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply Google-only Access migration: %v", err)
	}

	var accessGrantID, claimedSubject string
	if err := pool.QueryRow(ctx, `
		SELECT membership.access_grant_id::text, access_grant.claimed_by_subject
		FROM access_memberships membership
		JOIN access_grants access_grant ON access_grant.id = membership.access_grant_id
		WHERE membership.id = $1
	`, legacyMembershipID).Scan(&accessGrantID, &claimedSubject); err != nil {
		t.Fatalf("read converted Membership: %v", err)
	}
	if accessGrantID != legacyGrantID || claimedSubject != "google-subject" {
		t.Fatalf("converted Membership = grant:%q subject:%q", accessGrantID, claimedSubject)
	}
	for _, relation := range []string{"access_invitations", "access_invitation_locations"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, relation).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("legacy relation %s still exists", relation)
		}
	}
	var invitationColumnExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public'
				AND table_name = 'access_memberships'
				AND column_name = 'invitation_id'
		)
	`).Scan(&invitationColumnExists); err != nil {
		t.Fatal(err)
	}
	if invitationColumnExists {
		t.Fatal("legacy Membership invitation_id column still exists")
	}
}

func TestGoogleOnlyAccessMigrationRejectsUnmatchedLegacyMembership(t *testing.T) {
	pool := testdb.OpenThrough(t, "0026_ai_interactions.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (provisioning_key, name)
		VALUES ('unmatched-practice', 'Unmatched Practice')
	`); err != nil {
		t.Fatalf("seed unmatched Practice: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_invitations (
			provisioning_key, practice_id, token_hash, email, role,
			location_scope, expires_at, accepted_at, accepted_by_subject
		)
		SELECT
			'unmatched-user', id, decode(repeat('02', 32), 'hex'),
			'unmatched@example.com', 'STAFF', 'ALL', now() + interval '1 day',
			now(), 'unmatched-subject'
		FROM access_practices WHERE provisioning_key = 'unmatched-practice'
	`); err != nil {
		t.Fatalf("seed unmatched invitation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_memberships (
			user_subject, email, practice_id, role, location_scope, invitation_id
		)
		SELECT
			'unmatched-subject', 'unmatched@example.com', practice_id,
			'STAFF', 'ALL', id
		FROM access_invitations WHERE provisioning_key = 'unmatched-user'
	`); err != nil {
		t.Fatalf("seed unmatched legacy Membership: %v", err)
	}

	err := migrations.Apply(ctx, pool)
	if err == nil || !strings.Contains(err.Error(), "legacy invitation Membership has no compatible Access Grant") {
		t.Fatalf("Google-only migration error = %v", err)
	}
	var migrationRecorded bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM schema_migrations WHERE name = '0027_google_only_access.sql'
		)
	`).Scan(&migrationRecorded); err != nil {
		t.Fatal(err)
	}
	if migrationRecorded {
		t.Fatal("failed Google-only migration was recorded")
	}
}

func TestGoogleOnlyAccessMigrationRemovesOperatorSpecificAccess(t *testing.T) {
	pool := testdb.OpenThrough(t, "0026_ai_interactions.sql")
	ctx := context.Background()
	const operatorSubject = "operator-google-subject"
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (provisioning_key, name)
		VALUES ('operator-practice', 'Operator Practice')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_platform_operators (email, user_subject)
		VALUES ('operator@example.com', $1)
	`, operatorSubject); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_grants (
			provisioning_key, practice_id, email, role, location_scope,
			claimed_at, claimed_by_subject
		)
		SELECT
			'operator-staff', id, 'operator@example.com', 'STAFF', 'ALL',
			now(), $1
		FROM access_practices WHERE provisioning_key = 'operator-practice'
	`, operatorSubject); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_memberships (
			user_subject, email, practice_id, role, location_scope, access_grant_id
		)
		SELECT
			$1, access_grant.email, access_grant.practice_id,
			access_grant.role, access_grant.location_scope, access_grant.id
		FROM access_grants access_grant
		WHERE access_grant.email = 'operator@example.com'
	`, operatorSubject); err != nil {
		t.Fatal(err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply operator Access cleanup: %v", err)
	}
	var memberships, grants, operationalScopes, callingScopes int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM access_memberships WHERE email = 'operator@example.com'),
			(SELECT count(*) FROM access_grants WHERE email = 'operator@example.com'),
			(SELECT count(*) FROM access_operational_scopes WHERE user_subject = $1),
			(SELECT count(*) FROM access_calling_scopes WHERE user_subject = $1)
	`, operatorSubject).Scan(&memberships, &grants, &operationalScopes, &callingScopes); err != nil {
		t.Fatal(err)
	}
	if memberships != 0 || grants != 0 || operationalScopes != 1 || callingScopes != 1 {
		t.Fatalf(
			"operator Access = Memberships:%d Grants:%d OperationalScopes:%d CallingScopes:%d",
			memberships,
			grants,
			operationalScopes,
			callingScopes,
		)
	}
}

func TestHollywoodAccessExpansionUpdatesGrantsAndClaimedMemberships(t *testing.T) {
	pool := testdb.OpenThrough(t, "0027_google_only_access.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (provisioning_key, name, workspace_version)
		VALUES ('abita-eye-group', 'Abita Eye Group', 7);

		INSERT INTO access_locations (practice_id, provisioning_key, name)
		SELECT id, location.key, location.name
		FROM access_practices
		CROSS JOIN (VALUES
			('hollywood', 'Hollywood'),
			('sweetwater', 'Sweetwater'),
			('north-miami-beach-optical', 'North Miami Beach Optical')
		) location(key, name)
		WHERE provisioning_key = 'abita-eye-group';

		INSERT INTO access_grants (
			provisioning_key, practice_id, email, role, location_scope,
			claimed_at, claimed_by_subject
		)
		SELECT
			reviewed.key,
			practice.id,
			reviewed.email,
			'STAFF',
			'SELECTED',
			CASE WHEN reviewed.key = 'abel-alvarez' THEN now() END,
			CASE WHEN reviewed.key = 'abel-alvarez' THEN 'abel-subject' END
		FROM access_practices practice
		CROSS JOIN (VALUES
			('abel-alvarez', 'abel@abitaeye.com'),
			('ari-nussbaum', 'anussbaum@abitaeye.com'),
			('denise-rivera', 'denise@abitaeye.com'),
			('katie-einsohn', 'mobileoptical@abitaeye.com'),
			('sasha-ojinaga', 'sashao@abitaeye.com')
		) reviewed(key, email)
		WHERE practice.provisioning_key = 'abita-eye-group';

		INSERT INTO access_memberships (
			user_subject, email, practice_id, role, location_scope, access_grant_id
		)
		SELECT
			'abel-subject', access_grant.email, access_grant.practice_id,
			access_grant.role, access_grant.location_scope, access_grant.id
		FROM access_grants access_grant
		WHERE access_grant.provisioning_key = 'abel-alvarez';
	`); err != nil {
		t.Fatalf("seed Hollywood Access expansion: %v", err)
	}

	if err := migrations.ApplyThrough(ctx, pool, "0028_expand_hollywood_access.sql"); err != nil {
		t.Fatalf("apply Hollywood Access expansion: %v", err)
	}

	var grantLocations, membershipLocations, auditEvents int
	var workspaceVersion int64
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*)
			 FROM access_grant_locations allowed
			 JOIN access_locations location ON location.id = allowed.location_id
			 WHERE location.provisioning_key = 'hollywood'),
			(SELECT count(*)
			 FROM access_membership_locations allowed
			 JOIN access_locations location ON location.id = allowed.location_id
			 WHERE location.provisioning_key = 'hollywood'),
			(SELECT count(*)
			 FROM access_audit_events
			 WHERE action = 'access.grants_scope_expanded'),
			(SELECT workspace_version
			 FROM access_practices
			 WHERE provisioning_key = 'abita-eye-group')
	`).Scan(&grantLocations, &membershipLocations, &auditEvents, &workspaceVersion); err != nil {
		t.Fatal(err)
	}
	if grantLocations != 5 || membershipLocations != 1 || auditEvents != 1 || workspaceVersion != 8 {
		t.Fatalf(
			"Hollywood Access expansion = grants:%d memberships:%d audits:%d workspace:%d",
			grantLocations, membershipLocations, auditEvents, workspaceVersion,
		)
	}
}

func TestOutboundVoiceFallbackMigrationBackfillsAbita(t *testing.T) {
	pool := testdb.OpenThrough(t, "0028_expand_hollywood_access.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (provisioning_key, name)
		VALUES ('abita-eye-group', 'Abita Eye Group');

		INSERT INTO access_locations (practice_id, provisioning_key, name)
		SELECT practice.id, location.key, location.name
		FROM access_practices practice
		CROSS JOIN (VALUES
			('sweetwater-optical', 'Sweetwater Optical'),
			('sweetwater', 'Sweetwater')
		) location(key, name)
		WHERE practice.provisioning_key = 'abita-eye-group';

		INSERT INTO human_calling_location_voice_numbers (
			practice_id,
			location_id,
			phone
		)
		SELECT practice.id, location.id, '+17864654836'
		FROM access_practices practice
		JOIN access_locations location ON location.practice_id = practice.id
		WHERE practice.provisioning_key = 'abita-eye-group'
			AND location.provisioning_key = 'sweetwater';
	`); err != nil {
		t.Fatalf("seed Abita outbound voice fallback: %v", err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply outbound voice fallback migration: %v", err)
	}

	var fallbackPractice, fallbackLocation string
	if err := pool.QueryRow(ctx, `
		SELECT practice.provisioning_key, location.provisioning_key
		FROM human_calling_outbound_voice_fallbacks fallback
		JOIN access_practices practice ON practice.id = fallback.practice_id
		JOIN access_locations location
			ON location.practice_id = fallback.practice_id
			AND location.id = fallback.location_id
	`).Scan(&fallbackPractice, &fallbackLocation); err != nil {
		t.Fatalf("read migrated outbound voice fallback: %v", err)
	}
	if fallbackPractice != "abita-eye-group" || fallbackLocation != "sweetwater" {
		t.Fatalf(
			"migrated outbound voice fallback = %q/%q, want abita-eye-group/sweetwater",
			fallbackPractice,
			fallbackLocation,
		)
	}
}

func TestAIInteractionAttentionMigrationBackfillsAuthorizedOutcomes(t *testing.T) {
	pool := testdb.OpenThrough(t, "0031_correct_abita_access_grant_emails.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES ('00000000-0000-0000-0000-000000000601', 'attention-practice', 'Attention Practice');

		INSERT INTO access_locations (id, practice_id, provisioning_key, name) VALUES
			('00000000-0000-0000-0000-000000000602', '00000000-0000-0000-0000-000000000601', 'allowed', 'Allowed'),
			('00000000-0000-0000-0000-000000000603', '00000000-0000-0000-0000-000000000601', 'hidden', 'Hidden');

		INSERT INTO access_grants (
			id, provisioning_key, practice_id, email, role, location_scope,
			claimed_at, claimed_by_subject
		) VALUES
			('00000000-0000-0000-0000-000000000604', 'all-user', '00000000-0000-0000-0000-000000000601', 'all@example.com', 'ADMIN', 'ALL', now(), 'all-subject'),
			('00000000-0000-0000-0000-000000000605', 'selected-user', '00000000-0000-0000-0000-000000000601', 'selected@example.com', 'STAFF', 'SELECTED', now(), 'selected-subject');

		INSERT INTO access_memberships (
			id, user_subject, email, practice_id, role, location_scope, access_grant_id
		) VALUES
			('00000000-0000-0000-0000-000000000606', 'all-subject', 'all@example.com', '00000000-0000-0000-0000-000000000601', 'ADMIN', 'ALL', '00000000-0000-0000-0000-000000000604'),
			('00000000-0000-0000-0000-000000000607', 'selected-subject', 'selected@example.com', '00000000-0000-0000-0000-000000000601', 'STAFF', 'SELECTED', '00000000-0000-0000-0000-000000000605');

		INSERT INTO access_membership_locations (membership_id, practice_id, location_id)
		VALUES ('00000000-0000-0000-0000-000000000607', '00000000-0000-0000-0000-000000000601', '00000000-0000-0000-0000-000000000602');

		INSERT INTO ai_interactions (
			id, service_subject, practice_id, location_id, source_call_id,
			phone, office_phone, started_at, ended_at, status,
			appointment_outcome, appointment_occurred_at, lifecycle_stage
		) VALUES
			('00000000-0000-0000-0000-000000000608', 'abita-agent', '00000000-0000-0000-0000-000000000601', '00000000-0000-0000-0000-000000000602', 'allowed-booking', '+15555550101', '+15555550100', now() - interval '3 days', now() - interval '3 days' + interval '5 minutes', 'COMPLETED', 'BOOKING', now() - interval '3 days' + interval '4 minutes', 3),
			('00000000-0000-0000-0000-000000000609', 'abita-agent', '00000000-0000-0000-0000-000000000601', '00000000-0000-0000-0000-000000000603', 'hidden-cancellation', '+15555550102', '+15555550100', now() - interval '2 days', now() - interval '2 days' + interval '5 minutes', 'COMPLETED', 'CANCELLATION', now() - interval '2 days' + interval '4 minutes', 3),
			('00000000-0000-0000-0000-000000000610', 'abita-agent', '00000000-0000-0000-0000-000000000601', '00000000-0000-0000-0000-000000000602', 'partial-reschedule', '+15555550103', '+15555550100', now() - interval '1 day', now() - interval '1 day' + interval '5 minutes', 'COMPLETED', 'PARTIAL', now() - interval '1 day' + interval '4 minutes', 3);
	`); err != nil {
		t.Fatalf("seed existing AI Interaction outcomes: %v", err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply AI Interaction attention migration: %v", err)
	}

	var allAttention, selectedAttention, partialAttention int
	if err := pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE user_subject = 'all-subject'),
			count(*) FILTER (WHERE user_subject = 'selected-subject'),
			count(*) FILTER (WHERE interaction_id = '00000000-0000-0000-0000-000000000610')
		FROM ai_interaction_attention
	`).Scan(&allAttention, &selectedAttention, &partialAttention); err != nil {
		t.Fatalf("read migrated AI Interaction attention: %v", err)
	}
	if allAttention != 1 || selectedAttention != 1 || partialAttention != 2 {
		t.Fatalf(
			"migrated AI Interaction attention = all:%d selected:%d partial:%d",
			allAttention, selectedAttention, partialAttention,
		)
	}
}

func TestMadelynAccessGrantEmailCorrectionUpdatesUnclaimedGrant(t *testing.T) {
	pool := testdb.OpenThrough(t, "0029_outbound_voice_fallback.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (provisioning_key, name, workspace_version)
		VALUES ('abita-eye-group', 'Abita Eye Group', 4);

		INSERT INTO access_grants (
			provisioning_key, practice_id, email, role, location_scope
		)
		SELECT 'madelyn', id, 'madylen@abitaeye.com', 'STAFF', 'SELECTED'
		FROM access_practices
		WHERE provisioning_key = 'abita-eye-group';
	`); err != nil {
		t.Fatalf("seed Madelyn Access Grant: %v", err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply Madelyn email correction: %v", err)
	}

	var email string
	var workspaceVersion int64
	var auditEvents int
	if err := pool.QueryRow(ctx, `
		SELECT
			access_grant.email,
			practice.workspace_version,
			(SELECT count(*)
			 FROM access_audit_events
			 WHERE action = 'access.grant_email_corrected')
		FROM access_grants access_grant
		JOIN access_practices practice ON practice.id = access_grant.practice_id
		WHERE access_grant.provisioning_key = 'madelyn'
	`).Scan(&email, &workspaceVersion, &auditEvents); err != nil {
		t.Fatal(err)
	}
	if email != "madelyn@abitaeye.com" || workspaceVersion != 5 || auditEvents != 1 {
		t.Fatalf(
			"Madelyn correction = email:%s workspace:%d audits:%d",
			email, workspaceVersion, auditEvents,
		)
	}
}

func TestMadelynAccessGrantEmailCorrectionRejectsClaimedGrant(t *testing.T) {
	pool := testdb.OpenThrough(t, "0029_outbound_voice_fallback.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (provisioning_key, name)
		VALUES ('abita-eye-group', 'Abita Eye Group');

		INSERT INTO access_grants (
			provisioning_key, practice_id, email, role, location_scope,
			claimed_at, claimed_by_subject
		)
		SELECT
			'madelyn', id, 'madylen@abitaeye.com', 'STAFF', 'SELECTED',
			now(), 'madelyn-subject'
		FROM access_practices
		WHERE provisioning_key = 'abita-eye-group';
	`); err != nil {
		t.Fatalf("seed claimed Madelyn Access Grant: %v", err)
	}

	err := migrations.Apply(ctx, pool)
	if err == nil || !strings.Contains(err.Error(), "incompatible Access Grant state") {
		t.Fatalf("claimed Madelyn correction error = %v", err)
	}

	var email string
	if err := pool.QueryRow(ctx, `
		SELECT email FROM access_grants WHERE provisioning_key = 'madelyn'
	`).Scan(&email); err != nil {
		t.Fatal(err)
	}
	if email != "madylen@abitaeye.com" {
		t.Fatalf("claimed Madelyn Grant email = %s", email)
	}
}

func TestAbitaAccessGrantEmailCorrectionsUpdateUnclaimedGrants(t *testing.T) {
	pool := testdb.OpenThrough(t, "0030_correct_madelyn_access_grant_email.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (provisioning_key, name, workspace_version)
		VALUES ('abita-eye-group', 'Abita Eye Group', 8);

		INSERT INTO access_grants (
			id, provisioning_key, practice_id, email, role, location_scope
		)
		SELECT reviewed.id::uuid, reviewed.provisioning_key, practice.id,
			reviewed.email, 'STAFF', 'SELECTED'
		FROM access_practices practice
		CROSS JOIN (VALUES
			('00000000-0000-0000-0000-000000000401', 'ari-nussbaum', 'anussbaum@abitaeye.com'),
			('00000000-0000-0000-0000-000000000402', 'sherry', 'sherry@abitaeye.com')
		) reviewed(id, provisioning_key, email)
		WHERE practice.provisioning_key = 'abita-eye-group';
	`); err != nil {
		t.Fatalf("seed Abita Access Grants: %v", err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply Abita email corrections: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT id::text, provisioning_key, email, role::text, location_scope::text
		FROM access_grants
		ORDER BY provisioning_key
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	type grant struct {
		id       string
		key      string
		email    string
		role     string
		location string
	}
	var grants []grant
	for rows.Next() {
		var current grant
		if err := rows.Scan(
			&current.id,
			&current.key,
			&current.email,
			&current.role,
			&current.location,
		); err != nil {
			t.Fatal(err)
		}
		grants = append(grants, current)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []grant{
		{
			id:       "00000000-0000-0000-0000-000000000401",
			key:      "ari-nussbaum",
			email:    "ari@abitaeye.com",
			role:     "STAFF",
			location: "SELECTED",
		},
		{
			id:       "00000000-0000-0000-0000-000000000402",
			key:      "sherry",
			email:    "lutzoptical@abitaeye.com",
			role:     "STAFF",
			location: "SELECTED",
		},
	}
	if !reflect.DeepEqual(grants, want) {
		t.Fatalf("corrected Abita Access Grants = %#v, want %#v", grants, want)
	}

	var workspaceVersion int64
	var auditEvents int
	if err := pool.QueryRow(ctx, `
		SELECT
			workspace_version,
			(SELECT count(*)
			 FROM access_audit_events
			 WHERE actor_subject = 'migration:0031_correct_abita_access_grant_emails')
		FROM access_practices
		WHERE provisioning_key = 'abita-eye-group'
	`).Scan(&workspaceVersion, &auditEvents); err != nil {
		t.Fatal(err)
	}
	if workspaceVersion != 9 || auditEvents != 2 {
		t.Fatalf(
			"Abita email corrections = workspace:%d audits:%d",
			workspaceVersion,
			auditEvents,
		)
	}
}

func TestAbitaLocationSplitPreservesConfiguredRowsAndSeparatesRoutes(t *testing.T) {
	pool := testdb.OpenThrough(t, "0022_drop_support_mode.sql")
	ctx := context.Background()
	const (
		abitaPracticeID       = "00000000-0000-0000-0000-000000000201"
		southFloridaMedicalID = "00000000-0000-0000-0000-000000000202"
		southFloridaOpticalID = "00000000-0000-0000-0000-000000000203"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (id, provisioning_key, name, workspace_version)
		VALUES ($1, 'abita-eye-group', 'Abita Eye Group', 7)
	`, abitaPracticeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_locations (id, practice_id, provisioning_key, name) VALUES
			($2, $1, 'south-florida-medical', 'South Florida Medical'),
			($3, $1, 'south-florida-optical', 'South Florida Optical')
	`, abitaPracticeID, southFloridaMedicalID, southFloridaOpticalID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_abita_office_locations (practice_id, office_key, location_id) VALUES
			($1, 'hollywood', $2),
			($1, 'sweetwater', $2),
			($1, 'north-miami-beach-optical', $3)
	`, abitaPracticeID, southFloridaMedicalID, southFloridaOpticalID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_location_voice_numbers (
			practice_id, location_id, phone, voicemail_greeting
		) VALUES
			($1, $2, '+17864654836', 'Shared Abita greeting.'),
			($1, $3, '+13055095333', 'Shared Abita greeting.')
	`, abitaPracticeID, southFloridaMedicalID, southFloridaOpticalID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO messaging_location_configurations (
			practice_id, location_id, sender, messaging_profile_id
		) VALUES ($1, $2, '+17864654836', 'abita-messaging-profile');
	`, abitaPracticeID, southFloridaMedicalID); err != nil {
		t.Fatal(err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply Abita Location split: %v", err)
	}

	type location struct {
		Key, Name, ID string
	}
	locations := []location{}
	rows, err := pool.Query(ctx, `
		SELECT provisioning_key, name, id::text
		FROM access_locations
		WHERE practice_id = $1
		ORDER BY provisioning_key
	`, abitaPracticeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var candidate location
		if err := rows.Scan(&candidate.Key, &candidate.Name, &candidate.ID); err != nil {
			t.Fatal(err)
		}
		locations = append(locations, candidate)
	}
	wantLocations := []location{
		{Key: "hollywood", Name: "Hollywood"},
		{Key: "north-miami-beach-optical", Name: "North Miami Beach Optical", ID: southFloridaOpticalID},
		{Key: "sweetwater", Name: "Sweetwater", ID: southFloridaMedicalID},
		{Key: "sweetwater-optical", Name: "Sweetwater Optical"},
	}
	if len(locations) != len(wantLocations) {
		t.Fatalf("Locations = %#v, want %#v", locations, wantLocations)
	}
	for index, want := range wantLocations {
		if locations[index].Key != want.Key || locations[index].Name != want.Name ||
			(want.ID != "" && locations[index].ID != want.ID) {
			t.Fatalf("Locations = %#v, want %#v", locations, wantLocations)
		}
	}

	type route struct{ Office, Location string }
	routes := []route{}
	rows, err = pool.Query(ctx, `
		SELECT route.office_key, location.provisioning_key
		FROM access_abita_office_locations route
		JOIN access_locations location
			ON location.practice_id = route.practice_id
			AND location.id = route.location_id
		WHERE route.practice_id = $1
		ORDER BY route.office_key
	`, abitaPracticeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var candidate route
		if err := rows.Scan(&candidate.Office, &candidate.Location); err != nil {
			t.Fatal(err)
		}
		routes = append(routes, candidate)
	}
	wantRoutes := []route{
		{Office: "hollywood", Location: "hollywood"},
		{Office: "north-miami-beach-optical", Location: "north-miami-beach-optical"},
		{Office: "sweetwater", Location: "sweetwater"},
		{Office: "sweetwater-optical", Location: "sweetwater-optical"},
	}
	if len(routes) != len(wantRoutes) {
		t.Fatalf("routes = %#v, want %#v", routes, wantRoutes)
	}
	for index := range wantRoutes {
		if routes[index] != wantRoutes[index] {
			t.Fatalf("routes = %#v, want %#v", routes, wantRoutes)
		}
	}

	var voiceLocation, messagingLocation string
	if err := pool.QueryRow(ctx, `
		SELECT location.provisioning_key
		FROM human_calling_location_voice_numbers voice
		JOIN access_locations location
			ON location.practice_id = voice.practice_id
			AND location.id = voice.location_id
		WHERE voice.phone = '+17864654836'
	`).Scan(&voiceLocation); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT location.provisioning_key
		FROM messaging_location_configurations messaging
		JOIN access_locations location
			ON location.practice_id = messaging.practice_id
			AND location.id = messaging.location_id
		WHERE messaging.sender = '+17864654836'
	`).Scan(&messagingLocation); err != nil {
		t.Fatal(err)
	}
	if voiceLocation != "sweetwater" || messagingLocation != "sweetwater" {
		t.Fatalf("configured Locations = voice:%q messaging:%q", voiceLocation, messagingLocation)
	}

	var workspaceVersion int64
	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT workspace_version FROM access_practices WHERE id = $1
	`, abitaPracticeID).Scan(&workspaceVersion); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM access_audit_events
		WHERE practice_id = $1 AND action = 'access.locations_split'
	`, abitaPracticeID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if workspaceVersion != 8 || auditCount != 1 {
		t.Fatalf("workspace version = %d, split audits = %d", workspaceVersion, auditCount)
	}
}

func TestAbitaLocationSplitSkipsUnrelatedDevelopmentTopology(t *testing.T) {
	pool := testdb.OpenThrough(t, "0022_drop_support_mode.sql")
	ctx := context.Background()
	const (
		abitaPracticeID   = "00000000-0000-0000-0000-000000000221"
		fixtureLocationID = "00000000-0000-0000-0000-000000000222"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES ($1, 'abita-eye-group', 'Abita Eye Group')
	`, abitaPracticeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES ($2, $1, 'fixture-location-1', 'Fixture Location 1')
	`, abitaPracticeID, fixtureLocationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_invitations (
			provisioning_key, practice_id, token_hash, email, role,
			location_scope, expires_at
		) VALUES (
			'fixture-admin', $1, decode(repeat('02', 32), 'hex'),
			'admin@abita.test', 'ADMIN', 'ALL', now() + interval '1 day'
		)
	`, abitaPracticeID); err != nil {
		t.Fatal(err)
	}

	if err := migrations.ApplyThrough(ctx, pool, "0023_split_abita_locations.sql"); err != nil {
		t.Fatalf("apply Location split to development topology: %v", err)
	}

	var locationKey string
	var invitationCount int
	var splitApplied bool
	if err := pool.QueryRow(ctx, `
		SELECT provisioning_key FROM access_locations WHERE id = $1
	`, fixtureLocationID).Scan(&locationKey); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM access_invitations WHERE practice_id = $1
	`, abitaPracticeID).Scan(&invitationCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM schema_migrations
			WHERE name = '0023_split_abita_locations.sql'
		)
	`).Scan(&splitApplied); err != nil {
		t.Fatal(err)
	}
	if locationKey != "fixture-location-1" || invitationCount != 1 || !splitApplied {
		t.Fatalf(
			"development topology = location:%q invitations:%d applied:%t",
			locationKey,
			invitationCount,
			splitApplied,
		)
	}
}

func TestAbitaLocationSplitRejectsProvisionedAccounts(t *testing.T) {
	pool := testdb.OpenThrough(t, "0022_drop_support_mode.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES ($1, 'abita-eye-group', 'Abita Eye Group')
	`, "00000000-0000-0000-0000-000000000211"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_locations (id, practice_id, provisioning_key, name) VALUES
			($2, $1, 'south-florida-medical', 'South Florida Medical'),
			($3, $1, 'south-florida-optical', 'South Florida Optical')
	`,
		"00000000-0000-0000-0000-000000000211",
		"00000000-0000-0000-0000-000000000212",
		"00000000-0000-0000-0000-000000000213",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_invitations (
			provisioning_key, practice_id, token_hash, email, role,
			location_scope, expires_at
		) VALUES (
			'pending-account', $1, decode(repeat('01', 32), 'hex'),
			'pending@abita.test', 'STAFF', 'ALL', now() + interval '1 day'
		);
	`,
		"00000000-0000-0000-0000-000000000211",
	); err != nil {
		t.Fatal(err)
	}

	err := migrations.Apply(ctx, pool)
	if err == nil || !strings.Contains(err.Error(), "before account provisioning") {
		t.Fatalf("Location split error = %v", err)
	}

	var splitApplied bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM schema_migrations
			WHERE name = '0023_split_abita_locations.sql'
		)
	`).Scan(&splitApplied); err != nil {
		t.Fatal(err)
	}
	if splitApplied {
		t.Fatal("failed Location split was recorded")
	}
}

func TestCallLegCutoverBackfillsExactLegsAndProviderCommands(t *testing.T) {
	pool := testdb.OpenThrough(t, "0019_calling_recovery_continuity.sql")
	ctx := context.Background()
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	seedLegacyScope(t, pool, now)
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_calls (
			id, handoff_id, practice_id, location_id, state, offer_deadline,
			caller_call_control_id, caller_call_leg_id, call_session_id,
			claimant_subject, winner_subject, claimant_session_id,
			provider_termination, connected_at, ended_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'RESOLVED', $5, 'caller-control',
			'caller-leg', 'call-session', 'staff-subject', 'staff-subject',
			'staff-session', 'NORMAL_CLEARING', $6, $7, $5, $7)
	`, callID, handoffID, practiceID, locationID, now, now.Add(time.Second), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_connection_attempts (
			id, call_id, claimant_subject, claimant_session_id, connection_deadline,
			staff_call_control_id, staff_call_leg_id, staff_answered_at,
			bridge_occurred_at, ended_at, provider_termination, created_at, updated_at
		) VALUES ($1, $2, 'staff-subject', 'staff-session', $3,
			'staff-control', 'staff-leg', $4, $4, $5, 'NORMAL_CLEARING', $3, $5)
	`, attemptID, callID, now, now.Add(time.Second), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			call_id, attempt_id, user_subject, action, target_id, state,
			next_attempt_at, sent_at, created_at, updated_at
		) VALUES ($1, $2, 'staff-subject', 'HANGUP', 'staff-control', 'SENT',
			$3, $3, $3, $3)
	`, callID, attemptID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_recordings (
			call_id, practice_id, provider_recording_id, bucket, object_key,
			state, ready_at, created_at, updated_at
		) VALUES ($1, $2, 'historical-recording', 'legacy-bucket',
			'legacy-object', 'READY', $3, $3, $3)
	`, callID, practiceID, now); err != nil {
		t.Fatal(err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply CallLeg cutover: %v", err)
	}
	rows, err := pool.Query(ctx, `
		SELECT role, state, COALESCE(staff_subject, ''), provider_call_control_id,
			provider_call_leg_id, bridged_at IS NOT NULL
		FROM human_calling_call_legs WHERE call_id = $1 ORDER BY role
	`, callID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type leg struct {
		role, state, subject, controlID, legID string
		bridged                                bool
	}
	legs := []leg{}
	for rows.Next() {
		var current leg
		if err := rows.Scan(&current.role, &current.state, &current.subject,
			&current.controlID, &current.legID, &current.bridged); err != nil {
			t.Fatal(err)
		}
		legs = append(legs, current)
	}
	if len(legs) != 2 || legs[0].role != "CALLER" || legs[1].role != "STAFF" ||
		legs[0].controlID != "caller-control" || legs[1].controlID != "staff-control" ||
		legs[1].subject != "staff-subject" || !legs[0].bridged || !legs[1].bridged {
		t.Fatalf("backfilled CallLegs = %#v", legs)
	}

	var sourceHandoffID, recordingID, action, commandLegID string
	var recordingState, recordingBucket, recordingObject string
	var recordingReadyAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT call.source_handoff_id::text,
			call.historical_recording_evidence->>'providerRecordingId',
			command.action, command.call_leg_id::text,
			call.historical_recording_evidence->>'state',
			(call.historical_recording_evidence->>'readyAt')::timestamptz,
			call.historical_recording_evidence->>'bucket',
			call.historical_recording_evidence->>'objectKey'
		FROM human_calling_calls call
		JOIN human_calling_provider_commands command ON command.call_id = call.id
		WHERE call.id = $1 AND command.action = 'HANGUP_LEG'
	`, callID).Scan(&sourceHandoffID, &recordingID, &action, &commandLegID,
		&recordingState, &recordingReadyAt, &recordingBucket, &recordingObject); err != nil {
		t.Fatal(err)
	}
	if sourceHandoffID != handoffID || recordingID != "historical-recording" ||
		action != "HANGUP_LEG" || commandLegID != attemptID ||
		recordingState != "READY" || !recordingReadyAt.Equal(now) ||
		recordingBucket != "legacy-bucket" || recordingObject != "legacy-object" {
		t.Fatalf("cutover evidence = handoff:%s recording:%s action:%s leg:%s",
			sourceHandoffID, recordingID, action, commandLegID)
	}
}

func TestCallLegCutoverAbortsBeforeChangingActiveRuntime(t *testing.T) {
	pool := testdb.OpenThrough(t, "0019_calling_recovery_continuity.sql")
	ctx := context.Background()
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	seedLegacyScope(t, pool, now)
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_calls (
			id, handoff_id, practice_id, location_id, state, offer_deadline,
			caller_call_control_id, caller_call_leg_id, call_session_id
		) VALUES ($1, $2, $3, $4, 'OFFERING', $5,
			'active-control', 'active-leg', 'active-session')
	`, callID, handoffID, practiceID, locationID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	err := migrations.Apply(ctx, pool)
	if err == nil || !strings.Contains(err.Error(), "zero active Calls") {
		t.Fatalf("active-runtime cutover error = %v", err)
	}
	assertCutoverNotRecorded(t, pool)
}

func TestCallLegCutoverAbortsOnIncompleteProviderIdentity(t *testing.T) {
	pool := testdb.OpenThrough(t, "0019_calling_recovery_continuity.sql")
	ctx := context.Background()
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	seedLegacyScope(t, pool, now)
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_calls (
			id, handoff_id, practice_id, location_id, state, offer_deadline,
			caller_call_control_id, caller_call_leg_id, call_session_id,
			ended_at
		) VALUES ($1, $2, $3, $4, 'RESOLVED', $5,
			'incomplete-control', NULL, 'incomplete-session', $5)
	`, callID, handoffID, practiceID, locationID, now); err != nil {
		t.Fatal(err)
	}
	err := migrations.Apply(ctx, pool)
	if err == nil || !strings.Contains(err.Error(), "incomplete provider leg identity") {
		t.Fatalf("incomplete-identity cutover error = %v", err)
	}
	assertCutoverNotRecorded(t, pool)
}

func TestCallLegCutoverAllowsTerminalHistoryAndAppliedReceipts(t *testing.T) {
	pool := testdb.OpenThrough(t, "0019_calling_recovery_continuity.sql")
	ctx := context.Background()
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	seedLegacyScope(t, pool, now)
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_calls (
			id, handoff_id, practice_id, location_id, state, offer_deadline,
			caller_call_control_id, caller_call_leg_id, call_session_id, ended_at
		) VALUES ($1, $2, $3, $4, 'UNANSWERED', $5,
			'historical-control', 'historical-leg', 'historical-session', $5)
	`, callID, handoffID, practiceID, locationID, now); err != nil {
		t.Fatal(err)
	}
	for index, state := range []string{"APPLIED", "UNKNOWN", "FAILED"} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO human_calling_provider_receipts (
				event_id, call_id, event_type, occurred_at, received_at,
				signature_timestamp, raw_body, state, projected_at
			) VALUES ($1, $2, 'historical.event', $3, $3, $4, '{}'::bytea, $5, $3)
		`, "historical-receipt-"+state, callID, now, index+1, state); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("terminal history cutover: %v", err)
	}
}

func TestCallLegCutoverRejectsQuarantinedReceipt(t *testing.T) {
	pool := testdb.OpenThrough(t, "0019_calling_recovery_continuity.sql")
	ctx := context.Background()
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_provider_receipts (
			event_id, event_type, occurred_at, received_at, signature_timestamp,
			raw_body, state, quarantined_at
		) VALUES ('quarantined-cutover-receipt', 'call.answered', $1, $1, 1,
			'{}'::bytea, 'QUARANTINED', $1)
	`, now); err != nil {
		t.Fatal(err)
	}
	err := migrations.Apply(ctx, pool)
	if err == nil || !strings.Contains(err.Error(), "zero unprojected provider receipts") {
		t.Fatalf("quarantined-receipt cutover error = %v", err)
	}
	assertCutoverNotRecorded(t, pool)
}

func TestProviderReceiptRetryConstraintsRemainEnforced(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	assertViolation := func(eventID, state string, attempts int, last, quarantined *time.Time, constraint string) {
		t.Helper()
		_, err := pool.Exec(ctx, `
			INSERT INTO human_calling_provider_receipts (
				event_id, event_type, occurred_at, received_at, signature_timestamp,
				raw_body, state, projection_attempts, last_attempt_at, quarantined_at
			) VALUES ($1, 'call.answered', $2, $2, $3, '{}'::bytea, $4, $5, $6, $7)
		`, eventID, now, now.Unix(), state, attempts, last, quarantined)
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || databaseError.ConstraintName != constraint {
			t.Fatalf("insert %s error = %v, want %s", eventID, err, constraint)
		}
	}
	assertViolation("missing-attempt-time", "PENDING", 1, nil, nil,
		"human_calling_provider_receipts_attempt_visibility_check")
	assertViolation("missing-quarantine-time", "QUARANTINED", 0, nil, nil,
		"human_calling_provider_receipts_quarantine_check")
}

func TestRejectedProviderLegMigrationTerminalizesOnlyExactLifecycleMatches(t *testing.T) {
	pool := testdb.OpenThrough(t, "0031_correct_abita_access_grant_emails.sql")
	ctx := context.Background()
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	rows := []struct {
		eventID, eventType, state, controlID, legID, sessionID, errorCode string
	}{
		{"rejected-initiated", "call.initiated", "FAILED", "rejected-control", "rejected-leg", "rejected-session", "HANDOFF_REJECTED"},
		{"exact-answered", "call.answered", "PENDING", "rejected-control", "rejected-leg", "rejected-session", "WAITING_FOR_RELATED_FACT"},
		{"exact-bridged", "call.bridged", "QUARANTINED", "rejected-control", "rejected-leg", "rejected-session", "PROJECTION_RETRY_EXHAUSTED"},
		{"exact-hangup", "call.hangup", "PROCESSING", "rejected-control", "rejected-leg", "rejected-session", "PROJECTION_RETRY"},
		{"exact-applied", "call.hangup", "APPLIED", "rejected-control", "rejected-leg", "rejected-session", ""},
		{"other-session", "call.answered", "PENDING", "rejected-control", "rejected-leg", "other-session", "WAITING_FOR_RELATED_FACT"},
		{"unrelated-recording", "call.recording.saved", "PENDING", "rejected-control", "rejected-leg", "rejected-session", "PROJECTION_RETRY"},
	}
	originalBodies := make(map[string][]byte, len(rows))
	for index, row := range rows {
		body := []byte(fmt.Sprintf(
			`{"data":{"payload":{"call_control_id":"%s","call_leg_id":"%s","call_session_id":"%s"}}}`,
			row.controlID,
			row.legID,
			row.sessionID,
		))
		attempts := 1
		lastAttemptAt := now
		if row.eventID == "rejected-initiated" || row.state == "APPLIED" {
			attempts = 0
			lastAttemptAt = time.Time{}
		}
		var lastAttempt any
		if !lastAttemptAt.IsZero() {
			lastAttempt = lastAttemptAt
		}
		var processingStarted, projectedAt, quarantinedAt any
		if row.state == "PROCESSING" {
			processingStarted = now
		}
		if row.state == "FAILED" || row.state == "APPLIED" {
			projectedAt = now
		}
		if row.state == "QUARANTINED" {
			quarantinedAt = now
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO human_calling_provider_receipts (
				event_id, event_type, occurred_at, received_at, signature_timestamp,
				raw_body, state, projection_attempts, projection_error_code,
				processing_started_at, next_attempt_at, last_attempt_at,
				projected_at, quarantined_at
			) VALUES ($1, $2, $3, $3, $4, $5, $6, $7, NULLIF($8, ''),
				$9, $3, $10, $11, $12)
		`, row.eventID, row.eventType, now.Add(time.Duration(index)*time.Second),
			now.Unix(), body, row.state, attempts, row.errorCode,
			processingStarted, lastAttempt, projectedAt, quarantinedAt); err != nil {
			t.Fatalf("seed %s: %v", row.eventID, err)
		}
		originalBodies[row.eventID] = body
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("apply rejected provider leg migration: %v", err)
	}

	var remembered int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM human_calling_rejected_provider_legs
	`).Scan(&remembered); err != nil {
		t.Fatal(err)
	}
	if remembered != 1 {
		t.Fatalf("remembered rejected provider legs = %d, want 1", remembered)
	}
	for _, eventID := range []string{"exact-answered", "exact-bridged", "exact-hangup"} {
		var state, errorCode string
		var processingStarted, quarantinedAt *time.Time
		var projectedAt *time.Time
		if err := pool.QueryRow(ctx, `
			SELECT state, projection_error_code, processing_started_at,
				projected_at, quarantined_at
			FROM human_calling_provider_receipts
			WHERE event_id = $1
		`, eventID).Scan(
			&state, &errorCode, &processingStarted, &projectedAt, &quarantinedAt,
		); err != nil {
			t.Fatal(err)
		}
		if state != "FAILED" || errorCode != "RELATED_HANDOFF_REJECTED" ||
			processingStarted != nil || projectedAt == nil || quarantinedAt != nil {
			t.Fatalf("terminalized %s = %s/%s processing=%v projected=%v quarantined=%v",
				eventID, state, errorCode, processingStarted, projectedAt, quarantinedAt)
		}
	}
	for eventID, wantState := range map[string]string{
		"exact-applied":       "APPLIED",
		"other-session":       "PENDING",
		"unrelated-recording": "PENDING",
	} {
		var state string
		if err := pool.QueryRow(ctx, `
			SELECT state FROM human_calling_provider_receipts WHERE event_id = $1
		`, eventID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state != wantState {
			t.Fatalf("unrelated %s state = %s, want %s", eventID, state, wantState)
		}
	}
	for eventID, wantBody := range originalBodies {
		var gotBody []byte
		if err := pool.QueryRow(ctx, `
			SELECT raw_body FROM human_calling_provider_receipts WHERE event_id = $1
		`, eventID).Scan(&gotBody); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotBody, wantBody) {
			t.Fatalf("migration changed raw receipt %s", eventID)
		}
	}
}

func seedLegacyScope(t *testing.T, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, now time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO access_practices (id, provisioning_key, name)
		VALUES ($1, 'callleg-migration', 'CallLeg migration')
	`, practiceID); err != nil {
		t.Fatalf("seed legacy Practice: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO access_locations (id, practice_id, provisioning_key, name)
		VALUES ($2, $1, 'office', 'Office')
	`, practiceID, locationID); err != nil {
		t.Fatalf("seed legacy Location: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, token_hash, phone, expires_at,
			consumed_at, created_at
		) VALUES ($3, 'abita-migration', $1, $2, 'source-call', 'source-attempt',
			'fingerprint'::bytea, 'token-hash'::bytea, '+15555550100', $4, $5, $5)
	`, practiceID, locationID, handoffID, now.Add(time.Hour), now); err != nil {
		t.Fatalf("seed legacy Handoff: %v", err)
	}
}

func assertCutoverNotRecorded(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var recorded, tableExists bool
	if err := pool.QueryRow(context.Background(), `
		SELECT
			EXISTS (SELECT 1 FROM schema_migrations WHERE name = '0020_call_leg_cutover.sql'),
			to_regclass('public.human_calling_call_legs') IS NOT NULL
	`).Scan(&recorded, &tableExists); err != nil {
		t.Fatal(err)
	}
	if recorded || tableExists {
		t.Fatalf("failed cutover changed schema: recorded=%t CallLeg=%t", recorded, tableExists)
	}
}
