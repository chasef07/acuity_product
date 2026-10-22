package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

const (
	callVersionTriggers = "the human_calling_calls_version and human_calling_*_touch_call triggers " +
		"in backend/internal/migrations/sql/0106_call_version_trigger.sql"
	callingHintTriggers = "the human_calling_calls_publish and human_calling_softphone_leases_publish_takeover triggers " +
		"in backend/internal/migrations/sql/0106_call_version_trigger.sql"
	callingHintChannel  = "acuity_calling_hints"
	callingHintListener = "backend/internal/realtime/realtime.go"
)

func setsCallVersion(sql string) bool {
	tokens := tokenizeSQL(sql)
	for start := 0; start < len(tokens); {
		end := start
		for end < len(tokens) && tokens[end].text != ";" {
			end++
		}
		if statementSetsCallVersion(tokens[start:end]) {
			return true
		}
		start = end + 1
	}
	return false
}

func statementSetsCallVersion(tokens []sqlToken) bool {
	text := func(i int) string {
		if i < 0 || i >= len(tokens) {
			return ""
		}
		return tokens[i].text
	}
	writesCalls := false
	for i := range tokens {
		if (text(i) == "update" || text(i) == "into") && text(i+1) == "human_calling_calls" {
			writesCalls = true
		}
		if text(i) == "into" && text(i+1) == "human_calling_calls" && text(i+2) == "(" {
			for j := i + 3; j < len(tokens) && text(j) != ")"; j++ {
				if tokens[j].ident && text(j) == "version" {
					return true
				}
			}
		}
	}
	if !writesCalls {
		return false
	}
	depth := 0
	for i, current := range tokens {
		switch current.text {
		case "(":
			depth++
		case ")":
			depth--
		}
		if depth == 0 && current.ident && current.text == "version" && text(i+1) == "=" &&
			(text(i-1) == "set" || text(i-1) == ",") {
			return true
		}
	}
	return false
}

func callVersionViolations(fset *token.FileSet, file *ast.File, relative string) []string {
	var violations []string
	ast.Inspect(file, func(node ast.Node) bool {
		expr, ok := node.(ast.Expr)
		if !ok {
			return true
		}
		text, ok := stringConstant(expr)
		if !ok {
			return true
		}
		if setsCallVersion(text) {
			violations = append(violations, fmt.Sprintf("%s: SQL sets human_calling_calls.version by hand; %s own it, so write the client-visible columns and let them advance the version", fset.Position(expr.Pos()), callVersionTriggers))
		}
		if strings.Contains(text, callingHintChannel) && relative != callingHintListener {
			violations = append(violations, fmt.Sprintf("%s: Go code names the %s channel; %s send every calling hint when a Call version or softphone session changes", fset.Position(expr.Pos()), callingHintChannel, callingHintTriggers))
		}
		return false
	})
	return violations
}

func TestCallVersionAndCallingHintsBelongToTheDatabase(t *testing.T) {
	root := repositoryRoot(t)
	fset := token.NewFileSet()
	for _, p := range loadBackendPackages(t) {
		for _, name := range p.GoFiles {
			path := filepath.Join(p.Dir, name)
			relative, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatal(err)
			}
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, violation := range callVersionViolations(fset, file, filepath.ToSlash(relative)) {
				t.Error(violation)
			}
		}
	}
}

func TestSetsCallVersionFindsHandWrittenVersionWrites(t *testing.T) {
	for _, test := range []struct {
		sql  string
		want bool
	}{
		{`UPDATE human_calling_calls SET version = version + 1, updated_at = $2 WHERE id = $1`, true},
		{`UPDATE human_calling_calls call SET terminal_outcome = 'ENDED', version = call.version + 1 WHERE call.id = $1`, true},
		{`INSERT INTO human_calling_calls (id, practice_id, version) VALUES ($1, $2, 1)`, true},
		{`INSERT INTO human_calling_calls (id) VALUES ($1) ON CONFLICT (id) DO UPDATE SET version = EXCLUDED.version`, true},
		{`SELECT 1; UPDATE human_calling_calls SET version = 7 WHERE id = $1`, true},
		{`UPDATE human_calling_calls SET terminal_outcome = 'ENDED', updated_at = $2 WHERE id = $1 RETURNING version`, false},
		{`UPDATE human_calling_calls SET updated_at = $2 WHERE id = $1 AND version = $3`, false},
		{`SELECT version FROM human_calling_calls WHERE id = $1`, false},
		{`UPDATE human_calling_softphone_leases SET version = version + 1 WHERE user_subject = $1`, false},
		{`UPDATE work_tasks SET version = version + 1 FROM human_calling_calls call WHERE call.task_id = work_tasks.id`, false},
		{`INSERT INTO human_calling_call_legs (call_id, version) SELECT id, version FROM human_calling_calls`, false},
	} {
		if got := setsCallVersion(test.sql); got != test.want {
			t.Errorf("setsCallVersion(%q) = %t, want %t", test.sql, got, test.want)
		}
	}
}
