package humancalling

import (
	"slices"
	"testing"
)

func RouteClientStateKindLike(t testing.TB, kind, like string) {
	t.Helper()
	added, existing := clientStateKind{kind}, clientStateKind{like}
	if slices.Contains(clientStateKinds, added) || !slices.Contains(clientStateKinds, existing) {
		t.Fatalf("cannot route client state kind %q like %q", kind, like)
	}
	addedRoutes := map[factRoute]factHandler{}
	for route, handler := range providerFactRoutes {
		if route.kind == existing {
			route.kind = added
			addedRoutes[route] = handler
		}
	}
	kinds := clientStateKinds
	clientStateKinds = append(slices.Clone(kinds), added)
	for route, handler := range addedRoutes {
		providerFactRoutes[route] = handler
	}
	t.Cleanup(func() {
		clientStateKinds = kinds
		for route := range addedRoutes {
			delete(providerFactRoutes, route)
		}
	})
}

const StaleCallLegCandidateQuery = staleCallLegCandidateQuery
