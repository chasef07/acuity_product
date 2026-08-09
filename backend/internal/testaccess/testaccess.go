package testaccess

import (
	"context"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
)

// Activate claims one provisioned Google email grant and returns its current
// authorization. It keeps integration fixtures on the same sign-in path as the
// production portal.
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
