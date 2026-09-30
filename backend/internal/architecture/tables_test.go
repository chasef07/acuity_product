package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var tablePrefixes = []struct{ prefix, owner string }{
	{"access_", "access"},
	{"ai_", "interaction"},
	{"human_calling_", "humancalling"},
	{"knowledge_", "knowledge"},
	{"messaging_", "messaging"},
	{"work_", "work"},
}

var explicitTableOwners = map[string]string{"schema_migrations": "migrations"}

type access string

const (
	read  access = "read"
	lock  access = "lock"
	write access = "write"
)

type tableUse struct {
	file  string
	table string
	use   access
}

var sqlBaseline = []struct {
	file   string
	use    access
	tables string
	reason string
}{
	{"backend/cmd/backlog-recovery/main.go", lock, "work_task_acknowledgements", "one-off recovery command retires stuck acknowledgements directly"},
	{"backend/cmd/backlog-recovery/main.go", read, "access_audit_events access_platform_operators ai_interaction_receipts human_calling_call_legs human_calling_calls human_calling_provider_commands human_calling_provider_receipts schema_migrations work_tasks", "one-off recovery command selects and verifies backlog rows directly"},
	{"backend/cmd/knowledge-import/main.go", read, "knowledge_corpora", "import command confirms the published revision"},
	{"backend/cmd/knowledge-import/publication.go", read, "access_abita_office_locations access_platform_operators knowledge_corpora knowledge_passages knowledge_revisions", "import command resolves the operator and current office corpora"},
	{"backend/cmd/knowledge-import/source.go", read, "knowledge_corpora knowledge_passages", "import command diffs Git facts against the active revision"},
	{"backend/internal/access/access.go", read, "human_calling_location_voice_numbers", "service authorization resolves a Location by voice number"},
	{"backend/internal/humancalling/callleg_projection.go", read, "access_calling_scopes access_membership_locations access_memberships access_operational_scopes access_practices", "call projection resolves staff scope and practice policy (also FOR SHARE OF practice, appended with +=)"},
	{"backend/internal/humancalling/calls.go", read, "access_locations access_memberships access_platform_operators", "call views join Location names and staff emails"},
	{"backend/internal/humancalling/credentials.go", read, "access_operational_users", "credential reconciliation follows operational users"},
	{"backend/internal/humancalling/outbound.go", lock, "access_locations access_practices", "outbound fallback provisioning resolves Practice and Location keys"},
	{"backend/internal/humancalling/receipt_recovery.go", read, "access_locations", "recovery picks a Location to authorize the operator"},
	{"backend/internal/humancalling/ring_groups.go", lock, "access_locations", "ring group provisioning resolves Location keys"},
	{"backend/internal/humancalling/ring_groups.go", read, "access_practices", "ring group provisioning resolves Practice keys"},
	{"backend/internal/humancalling/softphone.go", read, "access_membership_locations access_operational_scopes", "softphone readiness checks staff scope"},
	{"backend/internal/humancalling/staff_transfer.go", read, "access_locations access_membership_locations access_memberships access_platform_operators", "transfer targets resolve eligible staff"},
	{"backend/internal/humancalling/staff_transfer_projection.go", read, "access_membership_locations access_memberships", "transfer projection resolves staff"},
	{"backend/internal/humancalling/state.go", read, "access_calling_scopes access_locations access_membership_locations access_memberships access_operational_scopes access_platform_operators", "calling state authorizes staff and Locations"},
	{"backend/internal/humancalling/webhook.go", read, "access_locations", "receipt requeue picks a Location to authorize the operator"},
	{"backend/internal/interaction/analytics.go", read, "access_locations", "analytics joins Location names"},
	{"backend/internal/interaction/call_issue_review.go", read, "access_memberships access_platform_operators", "issue review shows reporter and reviewer emails"},
	{"backend/internal/interaction/interaction.go", read, "access_locations", "AI Interaction views join Location names"},
	{"backend/internal/knowledge/knowledge.go", lock, "access_abita_office_locations", "publication holds the office route stable"},
	{"backend/internal/messaging/messaging.go", read, "access_locations access_practices work_task_acknowledgements", "messaging reads Practice/Location context; send claims join acknowledgements"},
	{"backend/internal/work/responsibilities.go", read, "access_grant_locations access_grants access_locations access_membership_locations access_memberships access_practices", "responsibility routing resolves eligible staff"},
	{"backend/internal/work/text_replies.go", lock, "messaging_threads", "text reply completion takes the thread lock inbound projection uses"},
	{"backend/internal/work/text_replies.go", read, "messaging_messages", "text reply completion reads delivery state"},
	{"backend/internal/work/work.go", read, "access_locations ai_interactions human_calling_call_legs human_calling_calls human_calling_handoffs human_calling_voicemails", "task queries and recovery resolution read call and AI evidence"},
}

var createdRelation = regexp.MustCompile(`(?i)\bcreate\s+(?:or\s+replace\s+)?(?:unlogged\s+)?(?:table|view)\s+(?:if\s+not\s+exists\s+)?(?:public\.)?([a-z][a-z0-9_]*)`)

func loadTableOwners(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repositoryRoot(t), "backend", "internal", "migrations", "sql", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("list migrations: %v (found %d)", err, len(files))
	}
	owners := map[string]string{}
	for table, owner := range explicitTableOwners {
		owners[table] = owner
	}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read migration: %v", err)
		}
		for _, match := range createdRelation.FindAllStringSubmatch(string(content), -1) {
			table := strings.ToLower(match[1])
			owner := ""
			for _, p := range tablePrefixes {
				if strings.HasPrefix(table, p.prefix) {
					owner = p.owner
				}
			}
			if owner == "" {
				t.Errorf("%s creates %s, which has no owning module: give it an owner prefix or add it to explicitTableOwners", filepath.Base(file), table)
				continue
			}
			owners[table] = owner
		}
	}
	return owners
}

func TestBackendSQLRespectsTableOwnership(t *testing.T) {
	root := repositoryRoot(t)
	owners := loadTableOwners(t)
	baseline := map[tableUse]bool{}
	for _, entry := range sqlBaseline {
		for _, table := range strings.Fields(entry.tables) {
			baseline[tableUse{entry.file, table, entry.use}] = true
		}
	}
	for _, p := range loadBackendPackages(t) {
		name := p.name()
		r, ok := roleOf(name)
		if !ok {
			t.Errorf("package %s is unclassified: add it to packageRoles", name)
			continue
		}
		if r == testSupport || name == "migrations" {
			continue
		}
		for _, file := range p.GoFiles {
			path := filepath.Join(p.Dir, file)
			relative, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatal(err)
			}
			uses, err := scanGoFile(path, owners)
			if err != nil {
				t.Fatalf("scan %s: %v", relative, err)
			}
			for table, use := range uses {
				key := tableUse{filepath.ToSlash(relative), table, use}
				if allowedTableUse(name, r, owners[table], use) {
					continue
				}
				if baseline[key] {
					delete(baseline, key)
					continue
				}
				t.Error(tableViolation(key, name, r, owners[table]))
			}
		}
	}
	for stale := range baseline {
		t.Errorf("sqlBaseline: %s no longer %s %s. Remove %s from that entry (or change the entry's use to what remains) so the boundary cannot regress.", stale.file, verb(stale.use), stale.table, stale.table)
	}
}

func allowedTableUse(pkg string, r role, owner string, use access) bool {
	switch r {
	case domain:
		return owner == pkg
	case query:
		return use == read
	default:
		return false
	}
}

func tableViolation(u tableUse, pkg string, r role, owner string) string {
	switch r {
	case domain:
		return fmt.Sprintf("%s: %s %s, owned by %s. Move this SQL into %s and call it through that module (workspace may read across modules for views). sqlBaseline records existing debt only; do not extend it without architecture review.", u.file, verb(u.use), u.table, owner, owner)
	case query:
		return fmt.Sprintf("%s: workspace %s %s. Workspace is read-only: move the %s into the owning module (%s) and call it from the adapter.", u.file, verb(u.use), u.table, u.use, owner)
	default:
		return fmt.Sprintf("%s: %s packages must not use tables directly, but %s %s %s. Call the owning module (%s) instead.", u.file, r, pkg, verb(u.use), u.table, owner)
	}
}

func verb(use access) string {
	switch use {
	case write:
		return "writes"
	case lock:
		return "locks"
	default:
		return "reads"
	}
}

func scanGoFile(path string, owners map[string]string) (map[string]access, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	uses := map[string]access{}
	ast.Inspect(file, func(node ast.Node) bool {
		expr, ok := node.(ast.Expr)
		if !ok {
			return true
		}
		text, ok := stringConstant(expr)
		if !ok {
			return true
		}
		for table, use := range scanSQL(text, owners) {
			if rank(use) > rank(uses[table]) {
				uses[table] = use
			}
		}
		return false
	})
	return uses, nil
}

func stringConstant(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(e.Value)
		return text, err == nil
	case *ast.ParenExpr:
		return stringConstant(e.X)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, ok := stringConstant(e.X)
		if !ok {
			return "", false
		}
		right, ok := stringConstant(e.Y)
		return left + right, ok
	}
	return "", false
}

func rank(use access) int {
	return map[access]int{read: 1, lock: 2, write: 3}[use]
}

type sqlToken struct {
	text  string
	ident bool
}

func tokenizeSQL(sql string) []sqlToken {
	var tokens []sqlToken
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '\'':
			for i++; i < len(sql); i++ {
				if sql[i] == '\'' {
					if i+1 < len(sql) && sql[i+1] == '\'' {
						i++
						continue
					}
					break
				}
			}
			i++
		case c == '-' && strings.HasPrefix(sql[i:], "--"):
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
		case c == '/' && strings.HasPrefix(sql[i:], "/*"):
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				i = len(sql)
			} else {
				i += end + 4
			}
		case c == '"':
			end := strings.IndexByte(sql[i+1:], '"')
			if end < 0 {
				end = len(sql) - i - 1
			}
			tokens = append(tokens, sqlToken{strings.ToLower(sql[i+1 : i+1+end]), true})
			i += end + 2
		case isIdentStart(c):
			start := i
			for i < len(sql) && (isIdentStart(sql[i]) || sql[i] >= '0' && sql[i] <= '9' || sql[i] == '$') {
				i++
			}
			tokens = append(tokens, sqlToken{strings.ToLower(sql[start:i]), true})
		case c >= '0' && c <= '9' || c == '$':
			for i++; i < len(sql) && (sql[i] >= '0' && sql[i] <= '9' || sql[i] == '.'); i++ {
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		default:
			tokens = append(tokens, sqlToken{string(c), false})
			i++
		}
	}
	return tokens
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

var notAlias = map[string]bool{
	"cross": true, "default": true, "do": true, "except": true, "for": true,
	"from": true, "full": true, "group": true, "having": true, "inner": true,
	"intersect": true, "join": true, "left": true, "limit": true, "natural": true,
	"offset": true, "on": true, "order": true, "returning": true, "right": true,
	"select": true, "set": true, "union": true, "using": true, "values": true,
	"where": true, "window": true,
}

func scanSQL(sql string, owners map[string]string) map[string]access {
	uses := map[string]access{}
	tokens := tokenizeSQL(sql)
	for start := 0; start < len(tokens); {
		end := start
		for end < len(tokens) && tokens[end].text != ";" {
			end++
		}
		for table, use := range scanStatement(tokens[start:end], owners) {
			if rank(use) > rank(uses[table]) {
				uses[table] = use
			}
		}
		start = end + 1
	}
	return uses
}

func scanStatement(tokens []sqlToken, owners map[string]string) map[string]access {
	word := func(i int) string {
		if i < 0 || i >= len(tokens) || !tokens[i].ident {
			return ""
		}
		return tokens[i].text
	}
	locksAll, lockNames := false, map[string]bool{}
	for i := range tokens {
		if word(i) != "for" {
			continue
		}
		j := i + 1
		if word(j) == "no" && word(j+1) == "key" {
			j += 2
		} else if word(j) == "key" {
			j++
		}
		if word(j) != "update" && word(j) != "share" {
			continue
		}
		if word(j+1) != "of" {
			locksAll = true
			continue
		}
		for k := j + 2; k < len(tokens); k += 2 {
			lockNames[word(k)] = true
			if k+1 >= len(tokens) || tokens[k+1].text != "," {
				break
			}
		}
	}

	uses := map[string]access{}
	for i, tok := range tokens {
		if _, known := owners[tok.text]; !known || !tok.ident {
			continue
		}
		if i > 0 && tokens[i-1].text == "." && word(i-2) != "public" {
			continue
		}
		before := i - 1
		if i > 1 && tokens[i-1].text == "." {
			before = i - 3
		}
		if word(before) == "only" || word(before) == "table" {
			before--
		}
		alias := word(i + 1)
		if alias == "as" {
			alias = word(i + 2)
		}
		if notAlias[alias] {
			alias = ""
		}
		use := read
		switch {
		case word(before) == "into" && (word(before-1) == "insert" || word(before-1) == "merge"),
			word(before) == "from" && word(before-1) == "delete",
			word(before) == "update" && !slices.Contains([]string{"for", "key", "do"}, word(before-1)),
			word(before) == "truncate":
			use = write
		case word(before) == "lock", locksAll, lockNames[tok.text], alias != "" && lockNames[alias]:
			use = lock
		}
		if rank(use) > rank(uses[tok.text]) {
			uses[tok.text] = use
		}
	}
	return uses
}
