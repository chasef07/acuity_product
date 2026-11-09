package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestMediaJWTCommandMigrationKeepsOtherProviderCommands(t *testing.T) {
	ctx := context.Background()
	pool := testdb.OpenThrough(t, "0119_accept_jury_call_labels.sql")
	if _, err := pool.Exec(ctx, `
 INSERT INTO human_calling_provider_commands(user_subject,action,target_id,payload,state,next_attempt_at)
 VALUES('synthetic-staff','CREATE_JWT','credential-1','{}','PENDING','2026-01-01'),
  ('synthetic-staff','CREATE_JWT','credential-1','{}','SENT','2026-01-01'),
  ('synthetic-staff','DISABLE_CREDENTIAL','credential-1','{}','PENDING','2026-01-01');
 `); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var mediaJWTs, others int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE action='CREATE_JWT'), count(*) FILTER (WHERE action<>'CREATE_JWT') FROM human_calling_provider_commands`).Scan(&mediaJWTs, &others); err != nil {
		t.Fatal(err)
	}
	if mediaJWTs != 0 || others != 1 {
		t.Fatalf("media JWT rows = %d, other rows = %d; want 0 and 1", mediaJWTs, others)
	}
}

func TestMediaJWTCommandMigrationIndexesDependencyForeignKey(t *testing.T) {
	ctx := context.Background()
	pool := testdb.OpenThrough(t, "0120_delete_media_jwt_commands.sql")
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `SET enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := connection.Query(ctx, `
		EXPLAIN (COSTS OFF)
		SELECT 1 FROM ONLY human_calling_provider_commands
		WHERE depends_on_command_id = '00000000-0000-0000-0000-000000000000'::uuid
	`)
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "human_calling_provider_commands_depends_on_idx") ||
		!strings.Contains(plan.String(), "Index Cond: (depends_on_command_id =") {
		t.Fatalf("dependency lookup plan = %q; want an index condition on human_calling_provider_commands_depends_on_idx", plan.String())
	}
}
