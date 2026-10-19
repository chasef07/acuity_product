package testaccess

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Activate(t testing.TB, module *access.Module, identity access.Identity) access.Authorization {
	t.Helper()
	discovery, err := module.DiscoverActor(context.Background(), identity)
	if err != nil {
		t.Fatalf("activate provisioned Google identity: %v", err)
	}
	if len(discovery.Practices) != 1 {
		t.Fatalf("activated Google identity Practices = %d, want 1", len(discovery.Practices))
	}
	authorization, err := module.ResolveActor(
		context.Background(),
		identity,
		discovery.Practices[0].ID,
		"",
	)
	if err != nil {
		t.Fatalf("resolve activated Google identity: %v", err)
	}
	return authorization
}

func SeedRevokedMembership(t testing.TB, pool *pgxpool.Pool, authorization access.Authorization, revokedAt time.Time) {
	t.Helper()
	result, err := pool.Exec(context.Background(), `
		UPDATE access_memberships SET revoked_at = $3
		WHERE id = $1 AND practice_id = $2
	`, authorization.Membership.ID, authorization.Practice.ID, revokedAt)
	if err != nil {
		t.Fatalf("seed revoked Membership: %v", err)
	}
	if result.RowsAffected() != 1 {
		t.Fatalf("seed revoked Membership affected %d rows, want 1", result.RowsAffected())
	}
}
