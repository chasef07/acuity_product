// audit-database-access indexes production Go database operations for review.
// Run: go run ./scripts/audit-database-access.go > database-access.csv
// This is a source index, not a proof of runtime reachability or query safety.
package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var databaseMethods = map[string]string{
	"Query": "statement", "QueryRow": "statement", "Exec": "statement",
	"Begin": "transaction", "BeginTx": "transaction",
	"Commit": "transaction", "Rollback": "transaction",
	"SendBatch": "batch", "CopyFrom": "copy", "Queue": "batch-statement",
	"Acquire": "connection", "Ping": "connection", "WaitForNotification": "listen",
	"Prepare": "prepare",
}

var tablePattern = regexp.MustCompile(`(?i)\b(?:FROM|JOIN|UPDATE|INTO|TABLE)\s+([a-z_][a-z_0-9.]*)`)
var verbPattern = regexp.MustCompile(`(?i)\b(SELECT|INSERT|UPDATE|DELETE|MERGE|CREATE|ALTER|DROP|GRANT|REVOKE|LISTEN|NOTIFY)\b`)

func main() {
	files := token.NewFileSet()
	var records [][]string
	err := filepath.WalkDir("backend", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == "backend/internal/testdb" || path == "backend/internal/testaccess" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".gen.go") {
			return nil
		}
		file, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			name := function.Name.Name
			if function.Recv != nil && len(function.Recv.List) > 0 {
				name = expression(files, function.Recv.List[0].Type) + "." + name
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				kind, ok := databaseMethods[selector.Sel.Name]
				if !ok {
					return true
				}
				receiver := expression(files, selector.X)
				if selector.Sel.Name == "Acquire" && strings.HasSuffix(receiver, ".admission") {
					// The HTTP and executor permit gates share this method name,
					// but acquiring their permits does not access PostgreSQL.
					return true
				}
				position := files.Position(call.Pos())
				var sql string
				var references []string
				argument := 1
				if selector.Sel.Name == "Queue" {
					argument = 0
				}
				if selector.Sel.Name == "Prepare" {
					argument = 2
				}
				if (kind == "statement" || kind == "batch-statement" || kind == "prepare") && len(call.Args) > argument {
					sql, references = sqlParts(files, call.Args[argument])
				}
				records = append(records, []string{
					filepath.ToSlash(path), strconv.Itoa(position.Line), name, kind,
					receiver, selector.Sel.Name,
					matches(verbPattern, sql), matches(tablePattern, sql),
					strings.Join(references, " | "),
				})
				return true
			})
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	writer := csv.NewWriter(os.Stdout)
	_ = writer.Write([]string{"file", "line", "function", "kind", "receiver", "operation", "literal_sql_verbs", "literal_sql_tables_or_ctes", "unresolved_sql_expressions"})
	writer.WriteAll(records)
	if err := writer.Error(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func expression(files *token.FileSet, node ast.Node) string {
	var output bytes.Buffer
	_ = printer.Fprint(&output, files, node)
	return output.String()
}

func sqlParts(files *token.FileSet, node ast.Expr) (string, []string) {
	switch value := node.(type) {
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			text, err := strconv.Unquote(value.Value)
			if err == nil {
				return text, nil
			}
		}
	case *ast.BinaryExpr:
		if value.Op == token.ADD {
			left, leftReferences := sqlParts(files, value.X)
			right, rightReferences := sqlParts(files, value.Y)
			return left + " " + right, append(leftReferences, rightReferences...)
		}
	}
	return "", []string{expression(files, node)}
}

func matches(pattern *regexp.Regexp, text string) string {
	unique := make(map[string]bool)
	for _, match := range pattern.FindAllStringSubmatch(text, -1) {
		unique[strings.ToLower(match[1])] = true
	}
	values := make([]string, 0, len(unique))
	for value := range unique {
		values = append(values, value)
	}
	sort.Strings(values)
	return strings.Join(values, " | ")
}
