package migrations_test

import (
	"context"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestRingGroupMigrationRestrictsOnlyExistingNMBLocation(t *testing.T) {
	pool := testdb.OpenThrough(t, "0060_attachment_storage_claims.sql")
	ctx := context.Background()
	input := access.Provisioning{
		Environment: "test", RequestedBy: "ring-migration-test",
		Practices: []access.PracticeProvision{{
			Key: "abita-eye-group", Name: "Synthetic Optical Practice",
			Locations: []access.LocationProvision{
				{Key: "north-miami-beach-optical", Name: "Synthetic North Office"},
				{Key: "sweetwater", Name: "Synthetic South Office"},
			},
			AccessGrants: []access.AccessGrantProvision{{
				Key: "bright-vu-miami", Email: "optical@synthetic.test",
				Role: access.RoleStaff, LocationScope: access.LocationScopeAll,
			}},
		}},
	}
	if _, err := access.New(pool, nil).Provision(ctx, input); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrations.Apply(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var key string
	var emails []string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM human_calling_location_ring_groups`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ring groups=%d err=%v", count, err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT location.provisioning_key, ring_group.member_emails
		FROM human_calling_location_ring_groups ring_group
		JOIN access_locations location ON location.id = ring_group.location_id
	`).Scan(&key, &emails); err != nil || key != "north-miami-beach-optical" ||
		len(emails) != 1 || emails[0] != "optical@synthetic.test" {
		t.Fatalf("migrated group=%s %v err=%v", key, emails, err)
	}
}
