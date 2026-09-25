package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestNewTampaRenameRejectsConflictingTopology(t *testing.T) {
	for _, conflict := range []string{"location", "route"} {
		t.Run(conflict, func(t *testing.T) {
			pool := testdb.OpenThrough(t, "0061_location_ring_groups.sql")
			ctx := context.Background()
			locations := []access.LocationProvision{
				{Key: "mental-health-demo", Name: "Original", AbitaOfficeKeys: []string{"mental-health-demo"}},
				{Key: "other-demo", Name: "Conflicting"},
			}
			if conflict == "location" {
				locations[1].Key = "new-tampa-demo"
			} else {
				locations[1].AbitaOfficeKeys = []string{"new-tampa-demo"}
			}
			if _, err := access.New(pool, nil).Provision(ctx, access.Provisioning{
				Environment: "test", RequestedBy: "new-tampa-conflict-test",
				Practices: []access.PracticeProvision{{Key: "acuity-demo", Name: "Synthetic Demo", Locations: locations}},
			}); err != nil {
				t.Fatal(err)
			}
			err := migrations.Apply(ctx, pool)
			if err == nil || !strings.Contains(err.Error(), "conflicting Location or office route") {
				t.Fatalf("expected conflict, got %v", err)
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_locations WHERE provisioning_key = 'mental-health-demo' AND name = 'Original'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("conflict mutated original Location: %d %v", count, err)
			}
		})
	}
}
