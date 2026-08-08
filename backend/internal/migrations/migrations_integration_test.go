package migrations_test

import (
	"context"
	"errors"
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
	if migrationCount != 25 {
		t.Fatalf("migration count = %d, want 25", migrationCount)
	}

	for _, relation := range []string{
		"access_grants",
		"access_grant_locations",
		"human_calling_handoffs",
		"human_calling_calls",
		"human_calling_call_legs",
		"human_calling_provider_commands",
		"human_calling_provider_receipts",
		"human_calling_projected_facts",
		"human_calling_timeline",
		"human_calling_location_voice_numbers",
		"human_calling_voicemails",
		"work_task_interactions",
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
		"access_support_sessions",
		"human_calling_connection_attempts",
		"human_calling_recordings",
		"human_calling_rejected_provider_legs",
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

	if err := migrations.Apply(ctx, pool); err != nil {
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
