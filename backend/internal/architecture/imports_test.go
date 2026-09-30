// Package architecture holds tests that enforce the backend's module
// boundaries described in README.md ("Code ownership"). It has no runtime code.
package architecture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const backendPath = "github.com/chasef07/acuity_product/backend/"

type role string

const (
	domain      role = "domain"       // owns durable behavior and its tables
	query       role = "query"        // cross-domain reads; owns no writes
	adapter     role = "adapter"      // thin edge: HTTP, auth, worker, realtime, generated API
	platform    role = "platform"     // shared infrastructure with no product rules
	composition role = "composition"  // wires the process: config, schema, commands
	testSupport role = "test support" // imported only by _test.go files
)

// packageRoles classifies every backend package, keyed by its path below
// backend/internal/. Packages under backend/cmd/ are composition roots.
var packageRoles = map[string]role{
	"access":         domain,
	"contactcontext": domain,
	"humancalling":   domain,
	"interaction":    domain,
	"knowledge":      domain,
	"messaging":      domain,
	"work":           domain,

	"workspace": query,

	"api":      adapter,
	"authn":    adapter,
	"httpapi":  adapter,
	"realtime": adapter,
	"worker":   adapter,

	"observability": platform,
	"postgres":      platform,

	"app":        composition,
	"migrations": composition,

	"architecture": testSupport,
	"testaccess":   testSupport,
	"testdb":       testSupport,
}

// allowedDomainImports is the reviewed domain-to-domain import graph. Adding
// an edge couples two modules' behavior; prefer calling through the owning
// module from an adapter or workspace, and add an edge only with review.
var allowedDomainImports = []string{
	"humancalling -> access",
	"humancalling -> work",
	"interaction -> access",
	"interaction -> work",
	"knowledge -> access",
	"messaging -> access",
	"messaging -> contactcontext",
	"messaging -> work",
	"work -> access",
	"work -> contactcontext",
}

type goPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Imports    []string
}

func (p goPackage) name() string { return packageName(p.ImportPath) }

// packageName shortens a backend import path: "work", "cmd/acuity".
func packageName(importPath string) string {
	return strings.TrimPrefix(strings.TrimPrefix(importPath, backendPath), "internal/")
}

func roleOf(name string) (role, bool) {
	if strings.HasPrefix(name, "cmd/") {
		return composition, true
	}
	r, ok := packageRoles[name]
	return r, ok
}

// repositoryRoot relies on go test running in this package's directory.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// loadBackendPackages reads the real build graph, excluding _test.go files.
func loadBackendPackages(t *testing.T) []goPackage {
	t.Helper()
	command := exec.Command("go", "list", "-json=ImportPath,Dir,GoFiles,Imports", "./backend/...")
	command.Dir = repositoryRoot(t)
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("go list ./backend/...: %v\n%s", err, exitErr.Stderr)
		}
		t.Fatalf("go list ./backend/...: %v", err)
	}
	var packages []goPackage
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var p goPackage
		if err := decoder.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		packages = append(packages, p)
	}
	if len(packages) == 0 {
		t.Fatal("go list returned no backend packages")
	}
	return packages
}

func TestBackendImportsRespectModuleBoundaries(t *testing.T) {
	for _, violation := range importViolations(loadBackendPackages(t)) {
		t.Error(violation)
	}
}

func importViolations(packages []goPackage) []string {
	var violations []string
	usedDomainEdges := map[string]bool{}
	for _, p := range packages {
		from := p.name()
		fromRole, ok := roleOf(from)
		if !ok {
			violations = append(violations, fmt.Sprintf("package %s is unclassified: add it to packageRoles in backend/internal/architecture/imports_test.go", from))
			continue
		}
		for _, imported := range p.Imports {
			if !strings.HasPrefix(imported, backendPath) {
				continue
			}
			to := packageName(imported)
			toRole, ok := roleOf(to)
			if !ok {
				continue // reported when the imported package itself is visited
			}
			edge := from + " -> " + to
			if to == "api" && from != "httpapi" {
				violations = append(violations, fmt.Sprintf("%s: only httpapi may import the generated api package; convert to domain types at the httpapi boundary", edge))
				continue
			}
			if toRole == testSupport {
				violations = append(violations, fmt.Sprintf("%s: test helpers may be imported only from _test.go files", edge))
				continue
			}
			switch fromRole {
			case domain:
				if toRole == domain {
					usedDomainEdges[edge] = true
					if !slices.Contains(allowedDomainImports, edge) {
						violations = append(violations, fmt.Sprintf("%s: new domain-to-domain import; route through the owning module (%s) from an adapter or workspace instead, or add %q to allowedDomainImports with architecture review", edge, to, edge))
					}
				} else if toRole != platform {
					violations = append(violations, fmt.Sprintf("%s: domain modules must not import %s packages; invert the dependency so the %s package calls the domain", edge, toRole, toRole))
				}
			case query:
				if toRole != domain && toRole != platform {
					violations = append(violations, fmt.Sprintf("%s: workspace is a read layer over domain modules and must not import %s packages", edge, toRole))
				}
			case platform:
				if toRole != platform {
					violations = append(violations, fmt.Sprintf("%s: platform packages carry no product rules and must not import %s packages", edge, toRole))
				}
			case adapter:
				if from == "worker" && toRole != platform {
					violations = append(violations, fmt.Sprintf("%s: worker depends only on the interfaces in worker/runner.go; wire %s implementations in cmd/acuity", edge, to))
				}
			}
		}
	}
	for _, edge := range allowedDomainImports {
		if !usedDomainEdges[edge] {
			violations = append(violations, fmt.Sprintf("allowedDomainImports lists %q, which no longer exists: delete it so the edge cannot return unreviewed", edge))
		}
	}
	return violations
}
