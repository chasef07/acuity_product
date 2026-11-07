package migrations_test

import (
	"context"
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
