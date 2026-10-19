package humancalling_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestCallingStateIncludesEveryDiscoveredPractice(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	member := access.Identity{Subject: "calling-member", Email: "member@calling.test", EmailVerified: true}
	operator := access.Identity{Subject: "calling-operator", Email: "operator@calling.test", EmailVerified: true}
	practices := []access.PracticeProvision{}
	for _, key := range []string{"first", "second"} {
		practices = append(practices, access.PracticeProvision{
			Key: key, Name: key,
			Locations:    []access.LocationProvision{{Key: "office", Name: "Office"}},
			AccessGrants: []access.AccessGrantProvision{{Key: "member", Email: member.Email, Role: access.RoleStaff, LocationScope: access.LocationScopeAll}},
		})
	}
	if _, err := accessModule.Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "discovered-practices-test",
		PlatformOperators: []string{operator.Email}, Practices: practices,
	}); err != nil {
		t.Fatal(err)
	}
	calling := humancalling.New(pool, accessModule, nil, humancalling.Config{}, func() time.Time { return now })
	for _, identity := range []access.Identity{member, operator} {
		t.Run(identity.Subject, func(t *testing.T) {
			discovery, err := accessModule.DiscoverActor(ctx, identity)
			if err != nil {
				t.Fatal(err)
			}
			if len(discovery.Practices) != 2 {
				t.Fatalf("discovered %d practices, want 2", len(discovery.Practices))
			}
			state, err := calling.ReadCallingState(ctx, identity)
			if err != nil {
				t.Fatal(err)
			}
			if len(state.PracticeIDs) != len(discovery.Practices) {
				t.Fatalf("calling practices = %v", state.PracticeIDs)
			}
			for _, practice := range discovery.Practices {
				if !slices.Contains(state.PracticeIDs, practice.ID) {
					t.Fatalf("calling state missing discovered practice %s", practice.ID)
				}
			}
		})
	}
}
