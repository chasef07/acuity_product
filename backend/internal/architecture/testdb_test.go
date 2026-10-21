package architecture

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var domainModules = map[string]bool{
	"access":       true,
	"humancalling": true,
	"interaction":  true,
	"knowledge":    true,
	"messaging":    true,
	"work":         true,
	"workspace":    true,
}

var ownerRunBaseline = []struct {
	file         string
	opens        int
	constructors int
	reason       string
}{
	{"backend/cmd/knowledge-import/publication_test.go", 3, 0, "the import command connects with operator-supplied credentials whose runtime role is unknown"},
	{"backend/cmd/knowledge-import/retrieval_integration_test.go", 1, 0, "the import command connects with operator-supplied credentials whose runtime role is unknown"},
	{"backend/internal/humancalling/calllegs_integration_test.go", 2, 0, "shared fixtures drive provider, worker, and portal paths through one module"},
	{"backend/internal/humancalling/receipt_claim_integration_test.go", 1, 0, "receipt helpers drive worker claims through the test's only module"},
	{"backend/internal/humancalling/staff_transfer_integration_test.go", 1, 0, "shared fixtures drive provider, worker, and portal paths through one module"},
	{"backend/internal/interaction/analytics_projection_integration_test.go", 1, 0, "projects against an earlier migration, before the runtime grant contract applies"},
	{"backend/internal/interaction/cost_usage_projection_integration_test.go", 1, 0, "projects against an earlier migration, before the runtime grant contract applies"},
	{"backend/internal/interaction/recovery_integration_test.go", 0, 4, "RecoverSourceClock has no production caller, so no runtime role owns it"},
	{"backend/internal/work/responsibility_integration_test.go", 0, 2, "task-maintenance connects with operator-supplied credentials whose runtime role is unknown"},
	{"backend/internal/workspace/phone_lookup_integration_test.go", 1, 0, "queries an earlier migration, before the runtime grant contract applies"},
}

type ownerRuns struct{ opens, constructors int }

func TestIntegrationTestsRunModulesAsRuntimeRoles(t *testing.T) {
	root := repositoryRoot(t)
	baseline := map[string]ownerRuns{}
	for _, entry := range ownerRunBaseline {
		baseline[entry.file] = ownerRuns{entry.opens, entry.constructors}
	}
	fset := token.NewFileSet()
	for _, name := range trackedGoFiles(t) {
		if !strings.HasSuffix(name, "_test.go") ||
			strings.HasPrefix(name, "backend/internal/migrations/") ||
			strings.HasPrefix(name, "backend/internal/testdb/") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		file, err := parser.ParseFile(fset, name, content, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		got := countOwnerRuns(file)
		want := baseline[name]
		delete(baseline, name)
		if got.opens > want.opens {
			t.Errorf("%s: %d owner-only database opens (testdb.OpenAsOwner or testdb.OpenThrough), baseline allows %d. Use testdb.Open: seed through db.Owner and construct modules with db.As(testdb.Portal, Worker, Provider, or Realtime), the role that runs that code in production.", name, got.opens, want.opens)
		}
		if got.constructors > want.constructors {
			t.Errorf("%s: %d modules constructed on db.Owner, baseline allows %d. Construct them with db.As(<role>) for the role that runs that code in production; db.Owner is for seeding SQL and provisioning, as in access.New(db.Owner, clock).Provision(...).", name, got.constructors, want.constructors)
		}
		if got.opens < want.opens || got.constructors < want.constructors {
			t.Errorf("ownerRunBaseline: %s now has %d owner opens and %d owner constructors. Lower its entry to match so it cannot grow back.", name, got.opens, got.constructors)
		}
	}
	for file := range baseline {
		t.Errorf("ownerRunBaseline: %s no longer exists. Remove its entry.", file)
	}
}

func countOwnerRuns(file *ast.File) ownerRuns {
	internal := strings.TrimSuffix(file.Name.Name, "_test")
	if file.Name.Name != internal || !domainModules[internal] {
		internal = ""
	}
	var runs ownerRuns
	provisioners := map[*ast.CallExpr]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "testdb" &&
			(sel.Sel.Name == "OpenAsOwner" || sel.Sel.Name == "OpenThrough") {
			runs.opens++
		}
		if receiver, ok := sel.X.(*ast.CallExpr); ok && strings.HasPrefix(sel.Sel.Name, "Provision") {
			ast.Inspect(receiver, func(node ast.Node) bool {
				if nested, ok := node.(*ast.CallExpr); ok {
					provisioners[nested] = true
				}
				return true
			})
		}
		return true
	})
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ownerNames := map[string]bool{}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) != len(assign.Rhs) {
				return true
			}
			for i, rhs := range assign.Rhs {
				if isOwnerSelector(rhs) {
					if ident, ok := assign.Lhs[i].(*ast.Ident); ok {
						ownerNames[ident.Name] = true
					}
				}
			}
			return true
		})
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 || provisioners[call] || !isModuleConstructor(call, internal) {
				return true
			}
			arg := call.Args[0]
			if ident, ok := arg.(*ast.Ident); (ok && ownerNames[ident.Name]) || isOwnerSelector(arg) {
				runs.constructors++
			}
			return true
		})
	}
	return runs
}

func isOwnerSelector(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Owner"
}

func isModuleConstructor(call *ast.CallExpr, internal string) bool {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		pkg, ok := fun.X.(*ast.Ident)
		return ok && fun.Sel.Name == "New" && domainModules[pkg.Name]
	case *ast.Ident:
		return fun.Name == "New" && internal != ""
	}
	return false
}

func TestCountOwnerRunsSeparatesSeedingFromModuleCode(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "fixture_test.go", `package work_test
func TestFixture(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	legacy := testdb.OpenAsOwner(t)
	_ = testdb.OpenThrough(t, "0001_initial.sql")
	_, _ = access.New(pool, nil).Provision(ctx, provisioning)
	_ = messaging.New(pool, nil, work.New(pool, nil, nil)).Provision(ctx, locations)
	_ = work.New(db.As(testdb.Portal), access.New(db.As(testdb.Portal), nil), nil)
	_ = work.New(pool, nil, nil)
	_ = access.New(db.Owner, nil)
	_ = work.New(legacy, nil, nil)
}
`, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := countOwnerRuns(file), (ownerRuns{opens: 2, constructors: 2}); got != want {
		t.Fatalf("countOwnerRuns = %+v, want %+v", got, want)
	}
}
