package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestNewTampaRenamePreservesLocationHistoryAndRoutes(t *testing.T) {
	pool := testdb.OpenThrough(t, "0061_location_ring_groups.sql")
	ctx := context.Background()
	module := access.New(pool, nil)
	input := access.Provisioning{
		Environment: "test", RequestedBy: "new-tampa-test",
		Practices: []access.PracticeProvision{{
			Key: "acuity-demo", Name: "Synthetic Demo",
			Locations: []access.LocationProvision{
				{Key: "mental-health-demo", Name: "Mental Health", AbitaOfficeKeys: []string{"mental-health-demo"}},
				{Key: "ophthalmology-demo", Name: "Other Demo", AbitaOfficeKeys: []string{"ophthalmology-demo"}},
			},
			AccessGrants: []access.AccessGrantProvision{{
				Key: "demo-staff", Email: "staff@synthetic.test", Role: access.RoleStaff,
				LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"mental-health-demo"},
			}},
		}, {
			Key: "unrelated-practice", Name: "Synthetic Other Practice",
			Locations: []access.LocationProvision{{Key: "mental-health-demo", Name: "Unrelated Mental Health"}},
		}},
	}
	if _, err := module.Provision(ctx, input); err != nil {
		t.Fatal(err)
	}
	var practice, location string
	if err := pool.QueryRow(ctx, `SELECT practice.id::text, location.id::text
 FROM access_practices practice JOIN access_locations location ON location.practice_id = practice.id
 WHERE practice.provisioning_key = 'acuity-demo' AND location.provisioning_key = 'mental-health-demo'`).Scan(&practice, &location); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interactions (
 service_subject, practice_id, location_id, source_call_id, phone, office_phone,
 started_at, status, lifecycle_stage, summary
 ) VALUES ('synthetic-agent', $1, $2, 'synthetic-history', '+12025550100', '+13207388132',
 now(), 'IN_PROGRESS', 1, 'Historical demo evidence')`, practice, location); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO human_calling_location_voice_numbers
 (practice_id, location_id, phone) VALUES ($1, $2, '+13207388132')`, practice, location); err != nil {
		t.Fatal(err)
	}
	var beforeVersion int64
	if err := pool.QueryRow(ctx, `SELECT workspace_version FROM access_practices WHERE id = $1`, practice).Scan(&beforeVersion); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrations.Apply(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	var key, name string
	var version int64
	if err := pool.QueryRow(ctx, `SELECT location.provisioning_key, location.name, practice.workspace_version
 FROM access_locations location JOIN access_practices practice ON practice.id = location.practice_id
 WHERE location.id = $1`, location).Scan(&key, &name, &version); err != nil {
		t.Fatal(err)
	}
	if key != "new-tampa-demo" || name != "New Tampa Eye Institute" || version != beforeVersion+1 {
		t.Fatalf("renamed Location = %s %s version %d (before %d)", key, name, version, beforeVersion)
	}
	// Reconcile the new desired topology twice: no replacement Location or lost grants.
	input.Practices[0].Locations[0].Key = "new-tampa-demo"
	input.Practices[0].Locations[0].Name = "New Tampa Eye Institute"
	input.Practices[0].Locations[0].AbitaOfficeKeys = []string{"new-tampa-demo", "mental-health-demo"}
	input.Practices[0].AccessGrants[0].SelectedLocationKeys = []string{"new-tampa-demo"}
	for range 2 {
		if _, err := module.Provision(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	for _, office := range []string{"mental-health-demo", "new-tampa-demo"} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		authorization, err := module.LockServiceAuthorization(ctx, tx, access.ServiceIdentity{
			Subject: "synthetic-agent", PracticeID: practice, LocationScope: access.LocationScopeAll,
			Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask},
		}, office, access.ServiceCapabilityCreateTask)
		_ = tx.Rollback(ctx)
		if err != nil || authorization.LocationID != location {
			t.Fatalf("route %s resolved to %s, want %s: %v", office, authorization.LocationID, location, err)
		}
	}
	for label, query := range map[string]string{
		"history": `SELECT count(*) FROM ai_interactions WHERE location_id = $1 AND source_call_id = 'synthetic-history' AND summary = 'Historical demo evidence'`,
		"grant":   `SELECT count(*) FROM access_grant_locations WHERE location_id = $1`,
		"voice":   `SELECT count(*) FROM human_calling_location_voice_numbers WHERE location_id = $1 AND phone = '+13207388132'`,
		"audit":   `SELECT count(*) FROM access_audit_events WHERE details->>'location_id' = $1 AND action = 'access.location_renamed'`,
	} {
		var count int
		if err := pool.QueryRow(ctx, query, location).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count = %d: %v", label, count, err)
		}
	}
	var locations, unrelated int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_locations WHERE practice_id = $1`, practice).Scan(&locations); err != nil || locations != 2 {
		t.Fatalf("demo Locations = %d: %v", locations, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_locations WHERE provisioning_key = 'mental-health-demo' AND name = 'Unrelated Mental Health'`).Scan(&unrelated); err != nil || unrelated != 1 {
		t.Fatalf("unrelated Location changed: %d %v", unrelated, err)
	}
}

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
