package testdb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRuntimeRolePoolsRunWithOnlyTheirGrants(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	if _, err := db.Owner.Exec(ctx, `
		INSERT INTO access_practices (provisioning_key, name)
		VALUES ('role-pool-test', 'Role Pool Test')
	`); err != nil {
		t.Fatalf("seed Practice as owner: %v", err)
	}
	for _, role := range []testdb.Role{testdb.Portal, testdb.Provider, testdb.Realtime, testdb.Worker} {
		pool := db.As(role)
		if pool != db.As(role) {
			t.Fatalf("%s pool was opened twice", role)
		}
		var current string
		if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&current); err != nil || current != string(role) {
			t.Fatalf("%s pool current_user = %q, %v", role, current, err)
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO access_practices (provisioning_key, name)
			VALUES ('role-pool-denied', 'Denied')
		`)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("%s seeded a Practice: %v, want permission denied", role, err)
		}
	}
	var practices int
	if err := db.As(testdb.Portal).QueryRow(ctx, `SELECT count(*) FROM access_practices`).Scan(&practices); err != nil || practices != 1 {
		t.Fatalf("portal Practice read = %d, %v; want the owner's seed", practices, err)
	}
}
