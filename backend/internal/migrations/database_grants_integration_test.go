package migrations_test

import (
	"context"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var runtimeRoles = []string{
	"acuity_auth",
	"acuity_portal",
	"acuity_provider",
	"acuity_realtime",
	"acuity_worker",
}

func TestDatabaseGrantsMatchRuntimeAuthority(t *testing.T) {
	pool := testdb.Open(t)
	createDatabaseRoles(t, pool)
	applyDatabaseGrants(t, pool)
	assertRepresentativeRuntimeQueries(t, pool)
	createFutureMigrationTables(t, pool)
	assertSequencePrivileges(t, pool)

	tablePrivileges := expectedTablePrivileges()
	columnPrivileges := expectedColumnPrivileges()
	assertSchemaPrivileges(t, pool)

	rows, err := pool.Query(context.Background(), `
		SELECT namespace.nspname, relation.relname
		FROM pg_class relation
		JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname IN ('public', 'auth')
			AND relation.relkind IN ('r', 'p', 'v', 'm')
		ORDER BY namespace.nspname, relation.relname
	`)
	if err != nil {
		t.Fatalf("list database relations: %v", err)
	}
	defer rows.Close()

	type relation struct {
		schema string
		name   string
	}
	var relations []relation
	for rows.Next() {
		var item relation
		if err := rows.Scan(&item.schema, &item.name); err != nil {
			t.Fatalf("scan database relation: %v", err)
		}
		relations = append(relations, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate database relations: %v", err)
	}

	for _, role := range runtimeRoles {
		for _, item := range relations {
			relationName := item.schema + "." + item.name
			for _, privilege := range []string{
				"SELECT",
				"INSERT",
				"UPDATE",
				"DELETE",
				"TRUNCATE",
				"REFERENCES",
				"TRIGGER",
			} {
				want := tablePrivileges[privilegeKey(role, relationName, privilege)]
				var got bool
				if err := pool.QueryRow(context.Background(),
					`SELECT has_table_privilege($1, $2, $3)`,
					role,
					relationName,
					privilege,
				).Scan(&got); err != nil {
					t.Fatalf("check %s %s on %s: %v", role, privilege, relationName, err)
				}
				if got != want {
					t.Errorf(
						"%s %s on %s = %t, want %t",
						role,
						privilege,
						relationName,
						got,
						want,
					)
				}
			}
		}
	}

	columnRows, err := pool.Query(context.Background(), `
		SELECT table_schema, table_name, column_name
		FROM information_schema.columns
		WHERE table_schema IN ('public', 'auth')
		ORDER BY table_schema, table_name, ordinal_position
	`)
	if err != nil {
		t.Fatalf("list database columns: %v", err)
	}
	defer columnRows.Close()
	for columnRows.Next() {
		var schema, table, column string
		if err := columnRows.Scan(&schema, &table, &column); err != nil {
			t.Fatalf("scan database column: %v", err)
		}
		relationName := schema + "." + table
		for _, role := range runtimeRoles {
			for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
				want := tablePrivileges[privilegeKey(role, relationName, privilege)] ||
					columnPrivileges[columnPrivilegeKey(
						role,
						relationName,
						column,
						privilege,
					)]
				var got bool
				if err := pool.QueryRow(context.Background(),
					`SELECT has_column_privilege($1, $2, $3, $4)`,
					role,
					relationName,
					column,
					privilege,
				).Scan(&got); err != nil {
					t.Fatalf(
						"check %s %s on %s.%s: %v",
						role,
						privilege,
						relationName,
						column,
						err,
					)
				}
				if got != want {
					t.Errorf(
						"%s %s on %s.%s = %t, want %t",
						role,
						privilege,
						relationName,
						column,
						got,
						want,
					)
				}
			}
		}
	}
	if err := columnRows.Err(); err != nil {
		t.Fatalf("iterate database columns: %v", err)
	}
}

func assertRepresentativeRuntimeQueries(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_provider_receipts (
			event_id,
			event_type,
			occurred_at,
			received_at,
			signature_timestamp,
			raw_body,
			next_attempt_at
		)
		VALUES (
			'grant-contract-rejected-initiation',
			'call.initiated',
			now(),
			now(),
			1,
			'{}'::bytea,
			now()
		)
	`); err != nil {
		t.Fatalf("seed runtime grant query receipt: %v", err)
	}
	queries := map[string][]string{
		"acuity_auth": {
			`SELECT id FROM auth."user" WHERE false`,
		},
		"acuity_portal": {
			`SELECT user_subject FROM access_operational_users WHERE false`,
			`SELECT id, expires_at, input_fingerprint
				 FROM human_calling_handoffs
				 WHERE false
				 FOR UPDATE`,
			`SELECT
					receipt.event_id,
					receipt.event_type,
					receipt.state,
					receipt.duplicate_count,
					receipt.projection_attempts
				 FROM human_calling_provider_receipts receipt
				 JOIN human_calling_calls call ON call.id = receipt.call_id
				 WHERE false
				 FOR UPDATE OF receipt`,
			`UPDATE human_calling_provider_receipts
				 SET
					state = 'PENDING',
					projection_attempts = 0,
					projection_error_code = 'MANUALLY_REQUEUED',
					processing_started_at = NULL,
					last_attempt_at = NULL,
					next_attempt_at = now(),
					projected_at = NULL,
					quarantined_at = NULL
				 WHERE false`,
		},
		"acuity_provider": {
			`INSERT INTO human_calling_provider_receipts (
				event_id,
				event_type,
				occurred_at,
				received_at,
				signature_timestamp,
				raw_body,
				next_attempt_at
			)
			VALUES (
				'grant-contract-event',
				'call.initiated',
				now(),
				now(),
				1,
				'{}'::bytea,
				now()
			)
			ON CONFLICT (event_id) DO NOTHING
			RETURNING event_id`,
			`SELECT event_type, raw_body, state, duplicate_count
			 FROM human_calling_provider_receipts
			 WHERE event_id = 'grant-contract-event'
			 FOR UPDATE`,
			`UPDATE human_calling_provider_receipts
			 SET duplicate_count = duplicate_count + 1
			 WHERE event_id = 'grant-contract-event'`,
		},
		"acuity_realtime": {
			`SELECT id, email, user_subject
			 FROM access_platform_operators
			 WHERE false
			 FOR UPDATE`,
			`UPDATE access_platform_operators
			 SET user_subject = 'grant-contract-subject'
			 WHERE false`,
		},
		"acuity_worker": {
			`SELECT user_subject FROM access_operational_users WHERE false`,
			`SELECT session_id
			 FROM human_calling_softphone_leases
			 WHERE false
			 FOR UPDATE`,
			`INSERT INTO human_calling_rejected_provider_legs (
				call_control_id,
				call_leg_id,
				call_session_id,
				initiated_event_id,
				rejected_at
			)
			VALUES (
				'grant-contract-control',
				'grant-contract-leg',
				'grant-contract-session',
				'grant-contract-rejected-initiation',
				now()
			)
			ON CONFLICT DO NOTHING`,
			`INSERT INTO human_calling_rejected_provider_legs (
				call_control_id,
				call_leg_id,
				call_session_id,
				initiated_event_id,
				rejected_at
			)
			VALUES (
				'grant-contract-control',
				'grant-contract-leg',
				'grant-contract-session',
				'grant-contract-rejected-initiation',
				now()
			)
			ON CONFLICT DO NOTHING`,
			`SELECT EXISTS (
				SELECT 1
				FROM human_calling_rejected_provider_legs
				WHERE call_control_id = 'grant-contract-control'
					AND call_leg_id = 'grant-contract-leg'
					AND call_session_id = 'grant-contract-session'
			)`,
			`INSERT INTO human_calling_projected_facts (
				event_id,
				event_type,
				applied_at
			)
			VALUES (
				'grant-contract-projected-fact',
				'call.initiated',
				now()
			)
			ON CONFLICT (event_id) DO NOTHING
			RETURNING event_id`,
		},
	}

	for _, role := range runtimeRoles {
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin %s grant query verification: %v", role, err)
		}
		if _, err := tx.Exec(
			context.Background(),
			"SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize(),
		); err != nil {
			_ = tx.Rollback(context.Background())
			t.Fatalf("set database role %s: %v", role, err)
		}
		for _, query := range queries[role] {
			if _, err := tx.Exec(context.Background(), query); err != nil {
				_ = tx.Rollback(context.Background())
				t.Fatalf("%s representative query failed: %v", role, err)
			}
		}
		if err := tx.Rollback(context.Background()); err != nil {
			t.Fatalf("rollback %s grant query verification: %v", role, err)
		}
	}
}

func createDatabaseRoles(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, role := range append(runtimeRoles, "acuity_migrate") {
		var exists bool
		if err := pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`,
			role,
		).Scan(&exists); err != nil {
			t.Fatalf("check database role %s: %v", role, err)
		}
		if exists {
			continue
		}
		if _, err := pool.Exec(
			context.Background(),
			"CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN",
		); err != nil {
			t.Fatalf("create database role %s: %v", role, err)
		}
	}
}

func applyDatabaseGrants(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := migrations.ApplyRuntimeGrants(context.Background(), pool); err != nil {
		t.Fatalf("apply database grant contract: %v", err)
	}
}

func createFutureMigrationTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		GRANT USAGE, CREATE ON SCHEMA public, auth TO acuity_migrate;
		SET ROLE acuity_migrate;
		CREATE TABLE public.future_product_table (id bigint PRIMARY KEY);
		CREATE TABLE auth.future_auth_table (id bigint PRIMARY KEY);
		CREATE SEQUENCE public.future_product_sequence;
		CREATE SEQUENCE auth.future_auth_sequence;
		RESET ROLE;
	`); err != nil {
		t.Fatalf("create future migration tables: %v", err)
	}
}

func assertSequencePrivileges(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, role := range runtimeRoles {
		for _, sequence := range []string{
			"public.future_product_sequence",
			"auth.future_auth_sequence",
		} {
			for _, privilege := range []string{"USAGE", "SELECT", "UPDATE"} {
				var got bool
				if err := pool.QueryRow(context.Background(),
					`SELECT has_sequence_privilege($1, $2, $3)`,
					role,
					sequence,
					privilege,
				).Scan(&got); err != nil {
					t.Fatalf(
						"check %s %s on %s: %v",
						role,
						privilege,
						sequence,
						err,
					)
				}
				if got {
					t.Errorf("%s unexpectedly has %s on %s", role, privilege, sequence)
				}
			}
		}
	}
}

func assertSchemaPrivileges(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, role := range runtimeRoles {
		for _, schema := range []string{"public", "auth"} {
			wantUsage := schema == "public" && role != "acuity_auth" ||
				schema == "auth" && role == "acuity_auth"
			var usage, create bool
			if err := pool.QueryRow(context.Background(), `
				SELECT
					has_schema_privilege($1, $2, 'USAGE'),
					has_schema_privilege($1, $2, 'CREATE')
			`, role, schema).Scan(&usage, &create); err != nil {
				t.Fatalf("check %s schema privileges on %s: %v", role, schema, err)
			}
			if usage != wantUsage {
				t.Errorf("%s USAGE on schema %s = %t, want %t", role, schema, usage, wantUsage)
			}
			if create {
				t.Errorf("%s unexpectedly has CREATE on schema %s", role, schema)
			}
		}
	}
}

func expectedTablePrivileges() map[string]bool {
	result := map[string]bool{}
	grant := func(role, privilege string, relations ...string) {
		for _, relation := range relations {
			result[privilegeKey(role, "public."+relation, privilege)] = true
		}
	}

	portalReads := []string{
		"access_abita_office_locations",
		"access_audit_events",
		"access_invitation_locations",
		"access_invitations",
		"access_locations",
		"access_membership_locations",
		"access_memberships",
		"access_operational_users",
		"access_platform_operators",
		"access_practices",
		"access_support_sessions",
		"human_calling_calls",
		"human_calling_connection_attempts",
		"human_calling_credentials",
		"human_calling_handoffs",
		"human_calling_location_voice_numbers",
		"human_calling_provider_commands",
		"human_calling_recordings",
		"human_calling_softphone_leases",
		"human_calling_timeline",
		"human_calling_voicemails",
		"messaging_attachments",
		"messaging_location_configurations",
		"messaging_messages",
		"messaging_thread_unreads",
		"messaging_threads",
		"work_task_activities",
		"work_tasks",
	}
	grant("acuity_portal", "SELECT", portalReads...)
	grant("acuity_portal", "INSERT",
		"access_audit_events",
		"access_locations",
		"access_membership_locations",
		"access_memberships",
		"access_support_sessions",
		"human_calling_connection_attempts",
		"human_calling_calls",
		"human_calling_credentials",
		"human_calling_handoffs",
		"human_calling_provider_commands",
		"human_calling_softphone_leases",
		"human_calling_timeline",
		"messaging_attachments",
		"messaging_messages",
		"messaging_provider_commands",
		"messaging_threads",
		"work_task_activities",
		"work_tasks",
	)
	grant("acuity_portal", "UPDATE",
		"access_invitations",
		"access_locations",
		"access_memberships",
		"access_practices",
		"access_support_sessions",
		"human_calling_calls",
		"human_calling_connection_attempts",
		"human_calling_credentials",
		"human_calling_provider_commands",
		"human_calling_handoffs",
		"human_calling_softphone_leases",
		"work_tasks",
	)
	grant("acuity_portal", "DELETE", "messaging_thread_unreads")

	grant("acuity_provider", "SELECT", "messaging_attachments")

	grant("acuity_realtime", "SELECT",
		"access_locations",
		"access_membership_locations",
		"access_memberships",
		"access_platform_operators",
		"access_practices",
		"access_support_sessions",
	)

	grant("acuity_worker", "SELECT",
		"access_locations",
		"access_membership_locations",
		"access_memberships",
		"access_operational_users",
		"access_practices",
		"human_calling_calls",
		"human_calling_connection_attempts",
		"human_calling_credentials",
		"human_calling_handoffs",
		"human_calling_location_voice_numbers",
		"human_calling_provider_commands",
		"human_calling_provider_receipts",
		"human_calling_recordings",
		"human_calling_softphone_leases",
		"human_calling_voicemails",
		"messaging_attachments",
		"messaging_location_configurations",
		"messaging_messages",
		"messaging_provider_commands",
		"messaging_provider_receipts",
		"messaging_thread_unreads",
		"messaging_threads",
		"work_task_activities",
		"work_tasks",
	)
	grant("acuity_worker", "INSERT",
		"human_calling_calls",
		"human_calling_credentials",
		"human_calling_projected_facts",
		"human_calling_provider_commands",
		"human_calling_recordings",
		"human_calling_timeline",
		"human_calling_voicemails",
		"messaging_attachments",
		"messaging_messages",
		"messaging_thread_unreads",
		"messaging_threads",
		"work_task_activities",
		"work_tasks",
	)
	grant("acuity_worker", "UPDATE",
		"access_practices",
		"human_calling_calls",
		"human_calling_connection_attempts",
		"human_calling_credentials",
		"human_calling_handoffs",
		"human_calling_provider_commands",
		"human_calling_provider_receipts",
		"human_calling_recordings",
		"human_calling_softphone_leases",
		"human_calling_voicemails",
		"work_tasks",
	)
	grant("acuity_worker", "DELETE", "messaging_attachments")

	for _, relation := range []string{"user", "session", "account", "verification", "jwks"} {
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			result[privilegeKey("acuity_auth", "auth."+relation, privilege)] = true
		}
	}
	return result
}

func expectedColumnPrivileges() map[string]bool {
	result := map[string]bool{}
	grant := func(role, relation, privilege string, columns ...string) {
		for _, column := range columns {
			result[columnPrivilegeKey(role, relation, column, privilege)] = true
		}
	}
	grant(
		"acuity_provider",
		"public.messaging_provider_receipts",
		"INSERT",
		"event_id",
		"event_type",
		"callback_token",
		"occurred_at",
		"received_at",
		"signature_timestamp",
		"raw_body",
	)
	grant(
		"acuity_provider",
		"public.messaging_provider_receipts",
		"SELECT",
		"event_id",
		"event_type",
		"callback_token",
		"occurred_at",
		"signature_timestamp",
		"raw_body",
		"duplicate_count",
	)
	grant(
		"acuity_portal",
		"public.messaging_provider_commands",
		"SELECT",
		"message_id",
		"practice_id",
		"actor_subject",
		"idempotency_key",
		"input_fingerprint",
	)
	grant(
		"acuity_portal",
		"public.messaging_threads",
		"UPDATE",
		"updated_at",
	)
	grant(
		"acuity_portal",
		"public.messaging_messages",
		"UPDATE",
		"task_id",
		"version",
		"updated_at",
	)
	grant(
		"acuity_portal",
		"public.messaging_attachments",
		"UPDATE",
		"message_id",
		"state",
		"expires_at",
		"copy_started_at",
		"updated_at",
	)
	grant(
		"acuity_worker",
		"public.messaging_threads",
		"UPDATE",
		"outbound_blocked",
		"opt_out_evidence_at",
		"opt_out_evidence_event_id",
		"updated_at",
	)
	grant(
		"acuity_worker",
		"public.messaging_messages",
		"UPDATE",
		"delivery_state",
		"safe_failure_code",
		"provider_message_id",
		"version",
		"updated_at",
	)
	grant(
		"acuity_worker",
		"public.messaging_attachments",
		"UPDATE",
		"state",
		"content_type",
		"byte_size",
		"object_key",
		"copy_started_at",
		"updated_at",
	)
	grant(
		"acuity_worker",
		"public.messaging_thread_unreads",
		"UPDATE",
		"unread_since",
		"latest_message_id",
	)
	grant(
		"acuity_worker",
		"public.messaging_provider_commands",
		"UPDATE",
		"state",
		"provider_message_id",
		"write_started_at",
		"completed_at",
		"next_attempt_at",
		"reconcile_until",
		"last_error_code",
		"updated_at",
	)
	grant(
		"acuity_worker",
		"public.messaging_provider_receipts",
		"UPDATE",
		"state",
		"processing_started_at",
		"projected_at",
		"projection_error_code",
	)
	grant(
		"acuity_worker",
		"public.human_calling_projected_facts",
		"SELECT",
		"event_id",
	)
	grant(
		"acuity_worker",
		"public.human_calling_rejected_provider_legs",
		"SELECT",
		"call_control_id",
		"call_leg_id",
		"call_session_id",
	)
	grant(
		"acuity_worker",
		"public.human_calling_rejected_provider_legs",
		"INSERT",
		"call_control_id",
		"call_leg_id",
		"call_session_id",
		"initiated_event_id",
		"rejected_at",
	)
	grant(
		"acuity_provider",
		"public.messaging_provider_receipts",
		"UPDATE",
		"duplicate_count",
	)
	grant(
		"acuity_provider",
		"public.human_calling_provider_receipts",
		"INSERT",
		"event_id",
		"event_type",
		"occurred_at",
		"received_at",
		"signature_timestamp",
		"raw_body",
		"next_attempt_at",
	)
	grant(
		"acuity_provider",
		"public.human_calling_provider_receipts",
		"SELECT",
		"event_id",
		"event_type",
		"raw_body",
		"state",
		"duplicate_count",
	)
	grant(
		"acuity_provider",
		"public.human_calling_provider_receipts",
		"UPDATE",
		"duplicate_count",
	)
	grant(
		"acuity_portal",
		"public.human_calling_provider_receipts",
		"SELECT",
		"event_id",
		"event_type",
		"occurred_at",
		"received_at",
		"call_id",
		"state",
		"projection_attempts",
		"projection_error_code",
		"duplicate_count",
	)
	grant(
		"acuity_portal",
		"public.human_calling_provider_receipts",
		"UPDATE",
		"state",
		"projection_attempts",
		"projection_error_code",
		"processing_started_at",
		"last_attempt_at",
		"next_attempt_at",
		"projected_at",
		"quarantined_at",
	)
	grant(
		"acuity_portal",
		"public.access_platform_operators",
		"UPDATE",
		"user_subject",
	)
	grant(
		"acuity_realtime",
		"public.access_platform_operators",
		"UPDATE",
		"user_subject",
	)
	return result
}

func privilegeKey(role, relation, privilege string) string {
	return role + "|" + relation + "|" + privilege
}

func columnPrivilegeKey(role, relation, column, privilege string) string {
	return role + "|" + relation + "|" + column + "|" + privilege
}
