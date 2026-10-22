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
		roleStaff, clientStateKind{"kind_from_a_newer_release"},
	))
	if !ok || state.Kind != kindUnrecognized {
		t.Fatalf("parsed client state = %+v, %t; want an unrecognized kind", state, ok)
	}
	if _, ok := parseCallLegClientState(encodeCallLegClientState(
		"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002",
		callLegRole{"OPERATOR"}, kindDial,
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
	listed := map[string][]string{"clientStateKind": {kindUnrecognized.name}}
	for _, kind := range clientStateKinds {
		listed["clientStateKind"] = append(listed["clientStateKind"], kind.name)
	}
	for _, role := range callLegRoles {
		listed["callLegRole"] = append(listed["callLegRole"], role.name)
	}
	fix := map[string]string{
		"clientStateKind": "declare it once beside the other kinds in fact_routes.go, " +
			"list it in clientStateKinds, and route it in routeProviderFacts",
		"callLegRole": "use roleCaller, roleStaff, or roleDestination",
	}
	fset := token.NewFileSet()
	var facts []string
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.CompositeLit:
				typeName, ok := node.Type.(*ast.Ident)
				if !ok || listed[typeName.Name] == nil || len(node.Elts) == 0 {
					return true
				}
				name, ok := stringLiteral(node.Elts[0])
				if !ok {
					t.Errorf("%s: %s is built from a non-literal; %s", fset.Position(node.Pos()), typeName.Name,
						fix[typeName.Name])
				} else if !slices.Contains(listed[typeName.Name], name) {
					t.Errorf("%s: %s %q is not a declared member; %s", fset.Position(node.Pos()), typeName.Name,
						name, fix[typeName.Name])
				}
			case *ast.GenDecl:
				if node.Tok != token.CONST {
					return true
				}
				for _, spec := range node.Specs {
					value := spec.(*ast.ValueSpec)
					if typeName, ok := value.Type.(*ast.Ident); !ok || typeName.Name != "FactType" {
						continue
					}
					for _, expression := range value.Values {
						name, ok := stringLiteral(expression)
						if !ok {
							t.Errorf("%s: FactType constant is not a string literal", fset.Position(expression.Pos()))
						}
						facts = append(facts, name)
					}
				}
			}
			return true
		})
	}
	for _, fact := range facts {
		if !slices.Contains(providerFactTypes, FactType(fact)) {
			t.Errorf("fact type %q is declared but not listed in providerFactTypes", fact)
		}
	}
	if len(facts) != len(providerFactTypes) {
		t.Errorf("declared facts=%d, listed facts=%d", len(facts), len(providerFactTypes))
	}
}

func stringLiteral(expression ast.Expr) (string, bool) {
	if field, ok := expression.(*ast.KeyValueExpr); ok {
		expression = field.Value
	}
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	text, err := strconv.Unquote(literal.Value)
	return text, err == nil
}
