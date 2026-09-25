package migrations_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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

	// Verify the complete applied registry, including independently added migrations.
	files, err := filepath.Glob("sql/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("read migration sources: %v", err)
	}
	wantNames := make([]string, 0, len(files))
	for _, file := range files {
		wantNames = append(wantNames, filepath.Base(file))
	}
	rows, err := pool.Query(ctx, `SELECT name FROM schema_migrations ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var appliedNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		appliedNames = append(appliedNames, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(appliedNames, wantNames) {
		t.Fatalf("applied migrations = %v, want %v", appliedNames, wantNames)
	}
	var bookingSearchPreciseExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema='public' AND table_name='ai_interactions'
				AND column_name='booking_search_precise'
		)
	`).Scan(&bookingSearchPreciseExists); err != nil {
		t.Fatal(err)
	}
	if !bookingSearchPreciseExists {
		t.Fatal("booking_search_precise column is missing")
	}
	var bookingFactsIndex string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname='public' AND indexname='ai_interactions_booking_facts_idx'
	`).Scan(&bookingFactsIndex); err != nil {
		t.Fatalf("read booking facts index: %v", err)
	}
	if !strings.Contains(bookingFactsIndex, "booking_search_precise") {
		t.Fatalf("booking facts index omits precise-search coverage: %s", bookingFactsIndex)
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
	for name, fragments := range map[string][]string{
		"human_calling_ready_commands_idx": {
			"(next_attempt_at, created_at, id)",
			"INCLUDE (call_id, call_leg_id, action, depends_on_command_id)",
			"state = 'PENDING'::text",
		},
		"human_calling_interrupted_commands_idx": {
			"(updated_at, id)",
			"INCLUDE (call_id, call_leg_id, action, created_at)",
			"call_id IS NOT NULL",
			"state = 'SENDING'::text",
		},
		"human_calling_interrupted_credential_commands_idx": {
			"(updated_at, id)",
			"call_id IS NULL",
			"CREATE_CREDENTIAL",
			"DISABLE_CREDENTIAL",
			"state = 'SENDING'::text",
		},
	} {
		var definition string
		if err := pool.QueryRow(ctx, `
			SELECT indexdef FROM pg_indexes
			WHERE schemaname = 'public' AND indexname = $1
		`, name).Scan(&definition); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, fragment := range fragments {
			if !strings.Contains(definition, fragment) {
				t.Errorf("%s omits %q: %s", name, fragment, definition)
			}
		}
	}
	var callingStateValidatorIndex string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public'
			AND indexname = 'human_calling_state_active_practice_idx'
	`).Scan(&callingStateValidatorIndex); err != nil {
		t.Fatalf("read Calling state validator index: %v", err)
	}
	for _, fragment := range []string{
		"(practice_id, id)",
		"terminal_outcome IS NULL",
		"disposition_at IS NULL",
		"'ENDED'::text",
		"'VOICEMAIL'::text",
	} {
		if !strings.Contains(callingStateValidatorIndex, fragment) {
			t.Errorf("Calling state validator index omits %q: %s",
				fragment, callingStateValidatorIndex)
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
