package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func typedStringConstants(t *testing.T, directory, typeName string) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("list %s: %v (found %d)", directory, err, len(files))
	}
	fset := token.NewFileSet()
	values := map[string]string{}
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
				for index, expression := range value.Values {
					literal, ok := expression.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						t.Fatalf("%s: %s constant is not a string literal", fset.Position(expression.Pos()), typeName)
					}
					text, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatalf("%s: %v", fset.Position(literal.Pos()), err)
					}
					values[value.Names[index].Name] = text
				}
			}
		}
	}
	return values
}

const callStateClassifier = "the classifier view from backend/internal/migrations/sql/0104_call_state_classifier.sql: " +
	"(SELECT call_state.state FROM human_calling_call_states call_state WHERE call_state.call_id = call.id)"

var classifierOnlyCallStateNames = []string{
	"CallPreparing", "CallConnecting", "CallConnected",
	"CallVoicemailGreeting", "CallVoicemailRecording", "CallNeedsDisposition",
}

func classifiedCallStateBranch(t *testing.T) *regexp.Regexp {
	t.Helper()
	declared := typedStringConstants(t, filepath.Join(repositoryRoot(t), "backend", "internal", "humancalling"), "CallState")
	states := make([]string, 0, len(classifierOnlyCallStateNames))
	for _, name := range classifierOnlyCallStateNames {
		state, ok := declared[name]
		if !ok {
			t.Fatalf("classifierOnlyCallStateNames lists %s, which humancalling does not declare as a CallState", name)
		}
		states = append(states, state)
	}
	return regexp.MustCompile(`(?i)\b(?:then|else)\s+'(` + strings.Join(states, "|") + `)'`)
}

func callStateClassifications(fset *token.FileSet, file *ast.File, branch *regexp.Regexp) []string {
	var violations []string
	producedState := func(expressions []ast.Expr) {
		for _, expression := range expressions {
			name := ""
			switch value := expression.(type) {
			case *ast.Ident:
				if file.Name.Name == "humancalling" {
					name = value.Name
				}
			case *ast.SelectorExpr:
				if pkg, ok := value.X.(*ast.Ident); ok && pkg.Name == "humancalling" {
					name = value.Sel.Name
				}
			}
			if slices.Contains(classifierOnlyCallStateNames, name) {
				violations = append(violations, fmt.Sprintf("%s: Go code produces humancalling.%s; read call state from %s instead of classifying it in Go", fset.Position(expression.Pos()), name, callStateClassifier))
			}
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.ReturnStmt:
			producedState(node.Results)
		case *ast.AssignStmt:
			producedState(node.Rhs)
		case *ast.ValueSpec:
			producedState(node.Values)
		case *ast.KeyValueExpr:
			producedState([]ast.Expr{node.Value})
		case *ast.CompositeLit:
			producedState(node.Elts)
		case ast.Expr:
			text, ok := stringConstant(node)
			if !ok {
				return true
			}
			for _, match := range branch.FindAllStringSubmatch(text, -1) {
				violations = append(violations, fmt.Sprintf("%s: SQL CASE yields call state %s; read it from %s", fset.Position(node.Pos()), match[1], callStateClassifier))
			}
			return false
		}
		return true
	})
	return violations
}

func TestCallStateIsClassifiedOnlyByTheClassifierView(t *testing.T) {
	branch := classifiedCallStateBranch(t)
	fset := token.NewFileSet()
	for _, p := range loadBackendPackages(t) {
		for _, name := range p.GoFiles {
			path := filepath.Join(p.Dir, name)
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, violation := range callStateClassifications(fset, file, branch) {
				t.Error(violation)
			}
		}
	}
}

func TestCallStateGuardCatchesTheRetiredClassifiers(t *testing.T) {
	branch := classifiedCallStateBranch(t)
	retired := map[string]string{
		"deriveCallState": `package humancalling
func deriveCallState(terminalOutcome, disposition, staffState, destinationState, voicemailPhase string, direction CallDirection, connected bool) CallState {
	if terminalOutcome == "ENDED" {
		if connected {
			return CallNeedsDisposition
		}
		return CallUnanswered
	}
	if voicemailPhase == string(CallVoicemailRecording) {
		return CallVoicemailRecording
	}
	if voicemailPhase == string(CallVoicemailGreeting) {
		return CallVoicemailGreeting
	}
	if staffState == "BRIDGED" || destinationState == "BRIDGED" {
		return CallConnected
	}
	if staffState == "ANSWERED" || staffState == "BRIDGE_PENDING" {
		return CallConnecting
	}
	if direction == CallOutbound {
		return CallPreparing
	}
	return CallRinging
}`,
		"workspace timeline CASE": "package workspace\nconst callProjectionSQL = `SELECT CASE\n" +
			"WHEN call.terminal_outcome IS NOT NULL AND bridged_staff.id IS NOT NULL THEN 'NEEDS_DISPOSITION'\n" +
			"WHEN bridged_staff.state = 'BRIDGED' THEN 'CONNECTED'\n" +
			"WHEN bridged_staff.state = 'BRIDGE_PENDING' THEN 'CONNECTING'\n" +
			"ELSE 'RINGING' END FROM human_calling_calls call`",
		"calling-state voicemail CASE": "package humancalling\nvar voicemailState = `SELECT CASE\n" +
			"WHEN call.terminal_outcome = 'VOICEMAIL' THEN 'VOICEMAIL'\n" +
			"WHEN EXISTS (SELECT 1) THEN 'VOICEMAIL_RECORDING'\n" +
			"ELSE 'VOICEMAIL_GREETING' END`",
		"projection voicemail phase": "package humancalling\nfunc read() { query(`SELECT CASE command.action\n" +
			"WHEN 'START_VOICEMAIL_RECORDING' THEN 'VOICEMAIL_RECORDING'\n" +
			"ELSE 'VOICEMAIL_GREETING' END`) }",
	}
	for name, source := range retired {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name+".go", source, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if violations := callStateClassifications(fset, file, branch); len(violations) == 0 {
			t.Errorf("%s is not caught", name)
		}
	}
	readers := map[string]string{
		"filter on classified state": "package workspace\nconst q = `SELECT 1 FROM human_calling_call_states call_state\n" +
			"WHERE call_state.state IN ('VOICEMAIL_GREETING', 'VOICEMAIL_RECORDING') OR call_state.state = 'CONNECTED'`",
		"leg and terminal outcomes": "package humancalling\nconst q = `SELECT CASE WHEN leg.answered_at IS NULL THEN 'RINGING'\n" +
			"WHEN call.terminal_outcome IS NULL THEN 'VOICEMAIL' ELSE 'MISSED' END`",
		"comparisons in Go": `package humancalling
func retry(call Call) bool {
	switch call.State {
	case CallConnected, CallConnecting:
		return false
	}
	return call.State == CallUnanswered || call.State != CallPreparing
}`,
	}
	for name, source := range readers {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name+".go", source, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if violations := callStateClassifications(fset, file, branch); len(violations) != 0 {
			t.Errorf("%s is flagged: %v", name, violations)
		}
	}
}

func TestCallStateMatchesPublishedSchema(t *testing.T) {
	root := repositoryRoot(t)
	domain := slices.Collect(maps.Values(typedStringConstants(t, filepath.Join(root, "backend", "internal", "humancalling"), "CallState")))
	published := slices.Collect(maps.Values(typedStringConstants(t, filepath.Join(root, "backend", "internal", "api"), "CallState")))
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
