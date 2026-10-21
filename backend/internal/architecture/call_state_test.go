package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func typedStringConstants(t *testing.T, directory, typeName string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("list %s: %v (found %d)", directory, err, len(files))
	}
	fset := token.NewFileSet()
	var values []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, declaration := range parsed.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value := spec.(*ast.ValueSpec)
				declared, ok := value.Type.(*ast.Ident)
				if !ok || declared.Name != typeName {
					continue
				}
				for _, expression := range value.Values {
					literal, ok := expression.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						t.Fatalf("%s: %s constant is not a string literal", fset.Position(expression.Pos()), typeName)
					}
					text, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatalf("%s: %v", fset.Position(literal.Pos()), err)
					}
					values = append(values, text)
				}
			}
		}
	}
	return values
}

func TestCallStateMatchesPublishedSchema(t *testing.T) {
	root := repositoryRoot(t)
	domain := typedStringConstants(t, filepath.Join(root, "backend", "internal", "humancalling"), "CallState")
	published := typedStringConstants(t, filepath.Join(root, "backend", "internal", "api"), "CallState")
	if len(domain) == 0 {
		t.Fatal("humancalling declares no CallState constants")
	}
	for _, state := range domain {
		if !slices.Contains(published, state) {
			t.Errorf("humancalling.CallState %s is missing from components.schemas.CallState in api/openapi.yaml; add it and run go generate ./backend/internal/api", state)
		}
	}
	for _, state := range published {
		if !slices.Contains(domain, state) {
			t.Errorf("components.schemas.CallState %s has no humancalling.CallState constant; remove it from api/openapi.yaml or add the constant to backend/internal/humancalling/core.go", state)
		}
	}
}
