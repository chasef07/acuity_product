package humancalling

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestEveryProviderFactRouteIsDecided(t *testing.T) {
	kinds := append(slices.Clone(clientStateKinds), kindUnrecognized)
	decided := 0
	for _, fact := range providerFactTypes {
		if providerFactRoutes[factRoute{fact: fact}] == nil {
			t.Errorf("%s without client state has no route", fact)
		}
		decided++
		for _, role := range callLegRoles {
			for _, kind := range kinds {
				if providerFactRoutes[factRoute{fact: fact, role: role, kind: kind}] == nil {
					t.Errorf("%s for %s %s has no route", fact, role, kind)
				}
				decided++
			}
		}
	}
	if len(providerFactRoutes) != decided {
		t.Errorf("route table has %d routes, want exactly the %d decided ones", len(providerFactRoutes), decided)
	}
}

func TestUnknownClientStateKindParsesAsUnrecognized(t *testing.T) {
	state, ok := parseCallLegClientState(encodeCallLegClientState(
		"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002",
		roleStaff, "kind_from_a_newer_release",
	))
	if !ok || state.Kind != kindUnrecognized {
		t.Fatalf("parsed client state = %+v, %t; want an unrecognized kind", state, ok)
	}
	if _, ok := parseCallLegClientState(encodeCallLegClientState(
		"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002",
		"OPERATOR", kindDial,
	)); ok {
		t.Fatal("client state with an unknown role parsed")
	}
}

func TestUnrecognizedKindWaitsForANewerWorker(t *testing.T) {
	for _, fact := range providerFactTypes {
		if fact == FactCallHangup {
			continue
		}
		for _, role := range callLegRoles {
			state := callLegClientState{Role: role, Kind: kindUnrecognized}
			verdict, err := providerFactRoutes[factRoute{fact: fact, role: role, kind: kindUnrecognized}](
				nil, context.Background(), ProviderFact{Type: fact}, state)
			if err != nil || verdict != factWaiting {
				t.Errorf("%s for %s with an unrecognized kind = %q, %v; want %q", fact, role, verdict, err, factWaiting)
			}
		}
	}
}

func TestEveryDeclaredKindAndFactTypeIsRouted(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string][]string{}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			constants, ok := declaration.(*ast.GenDecl)
			if !ok || constants.Tok != token.CONST {
				continue
			}
			for _, spec := range constants.Specs {
				value := spec.(*ast.ValueSpec)
				typeName, ok := value.Type.(*ast.Ident)
				if !ok || (typeName.Name != "clientStateKind" && typeName.Name != "FactType") {
					continue
				}
				for _, literal := range value.Values {
					text, err := strconv.Unquote(literal.(*ast.BasicLit).Value)
					if err != nil {
						t.Fatal(err)
					}
					declared[typeName.Name] = append(declared[typeName.Name], text)
				}
			}
		}
	}
	for _, kind := range declared["clientStateKind"] {
		if clientStateKind(kind) != kindUnrecognized && !slices.Contains(clientStateKinds, clientStateKind(kind)) {
			t.Errorf("client state kind %q is declared but not listed in clientStateKinds", kind)
		}
	}
	for _, fact := range declared["FactType"] {
		if !slices.Contains(providerFactTypes, FactType(fact)) {
			t.Errorf("fact type %q is declared but not listed in providerFactTypes", fact)
		}
	}
	if len(declared["clientStateKind"]) != len(clientStateKinds)+1 || len(declared["FactType"]) != len(providerFactTypes) {
		t.Errorf("declared kinds=%d facts=%d, listed kinds=%d facts=%d", len(declared["clientStateKind"]),
			len(declared["FactType"]), len(clientStateKinds), len(providerFactTypes))
	}
}
