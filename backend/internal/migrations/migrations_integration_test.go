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
	if migrationCount != 5 {
		t.Fatalf("migration count = %d, want 5", migrationCount)
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
	var operationalUsersView bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.views
			WHERE table_schema = 'public'
				AND table_name = 'access_operational_users'
		)
	`).Scan(&operationalUsersView); err != nil {
		t.Fatalf("inspect Access operational Users view: %v", err)
	}
	if !operationalUsersView {
		t.Fatal("Access operational Users view is missing")
	}
}
