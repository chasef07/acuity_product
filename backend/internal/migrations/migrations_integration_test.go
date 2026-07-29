package migrations_test

import (
	"context"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
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
	if migrationCount != 7 {
		t.Fatalf("migration count = %d, want 7", migrationCount)
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
