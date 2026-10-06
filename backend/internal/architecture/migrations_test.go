package architecture

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const lastUnlintedMigration = 82

var (
	sqlLineComment      = regexp.MustCompile(`--[^\n]*`)
	sessionLockTimeout  = regexp.MustCompile(`(?i)\bset\s+lock_timeout\b`)
	configLockTimeout   = regexp.MustCompile(`(?i)\bset_config\s*\(\s*'lock_timeout'`)
	localLockTimeout    = regexp.MustCompile(`(?i)\bset\s+local\s+lock_timeout\b`)
	resetLockTimeout    = regexp.MustCompile(`(?i)\breset\s+lock_timeout\b`)
	indexDefinition     = regexp.MustCompile(`(?i)\b(?:create\s+(?:unique\s+)?|drop\s+)index\b(\s+concurrently\b)?`)
	concurrentIndexWord = regexp.MustCompile(`(?i)\bconcurrently\b`)
)

func migrationSafetyViolations(name, source string) []string {
	firstLine, _, _ := strings.Cut(source, "\n")
	firstLine = strings.TrimSpace(firstLine)
	if firstLine == "-- acuity:retired" {
		return nil
	}
	nonTransactional := firstLine == "-- acuity:no-transaction"
	sql := sqlLineComment.ReplaceAllString(source, "")
	var violations []string
	setsSession := sessionLockTimeout.MatchString(sql)
	if !setsSession && (nonTransactional || !localLockTimeout.MatchString(sql)) {
		violations = append(violations, name+": set lock_timeout (SET LOCAL in a transaction, SET and RESET in an -- acuity:no-transaction migration)")
	}
	if setsSession && !resetLockTimeout.MatchString(sql) {
		violations = append(violations, name+": RESET lock_timeout so the session setting does not leak into later migrations")
	}
	if configLockTimeout.MatchString(sql) {
		violations = append(violations, name+": set lock_timeout with SET LOCAL or SET and RESET, not set_config")
	}
	for _, match := range indexDefinition.FindAllStringSubmatch(sql, -1) {
		if match[1] == "" {
			violations = append(violations, name+": "+strings.Join(strings.Fields(match[0]), " ")+" must use CONCURRENTLY")
		}
	}
	if concurrentIndexWord.MatchString(sql) && !nonTransactional {
		violations = append(violations, name+": CONCURRENTLY requires the -- acuity:no-transaction header")
	}
	return violations
}

func TestNewMigrationsBoundLocksAndBuildIndexesConcurrently(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repositoryRoot(t), "backend", "internal", "migrations", "sql", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("list migrations: %v (found %d)", err, len(files))
	}
	for _, file := range files {
		name := filepath.Base(file)
		prefix, _, _ := strings.Cut(name, "_")
		number, err := strconv.Atoi(prefix)
		if err != nil {
			t.Errorf("%s: migration names must start with a number", name)
			continue
		}
		if number <= lastUnlintedMigration {
			continue
		}
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, violation := range migrationSafetyViolations(name, string(content)) {
			t.Error(violation)
		}
	}
}

func TestMigrationSafetyViolations(t *testing.T) {
	for _, test := range []struct {
		source string
		want   int
	}{
		{"SET LOCAL lock_timeout = '1s';\nALTER TABLE t ADD COLUMN c int;\n", 0},
		{"ALTER TABLE t ADD COLUMN c int;\n", 1},
		{"-- SET LOCAL lock_timeout = '1s';\nALTER TABLE t ADD COLUMN c int;\n", 1},
		{"SET LOCAL lock_timeout = '1s';\nCREATE INDEX i ON t (c);\n", 1},
		{"SET LOCAL lock_timeout = '1s';\nCREATE INDEX CONCURRENTLY i ON t (c);\n", 1},
		{"-- acuity:no-transaction\nSET LOCAL lock_timeout = '1s';\n-- acuity:next-statement\nCREATE INDEX CONCURRENTLY i ON t (c);\n", 1},
		{"-- acuity:no-transaction\nSET lock_timeout = '15s';\n-- acuity:next-statement\nCREATE INDEX CONCURRENTLY i ON t (c);\n", 1},
		{"-- acuity:no-transaction\nSET lock_timeout = '15s';\n-- acuity:next-statement\nDROP INDEX i;\n-- acuity:next-statement\nRESET lock_timeout;\n", 1},
		{"-- acuity:no-transaction\nSET lock_timeout = '15s';\n-- acuity:next-statement\nCREATE UNIQUE INDEX CONCURRENTLY i ON t (c);\n-- acuity:next-statement\nDROP INDEX CONCURRENTLY IF EXISTS j;\n-- acuity:next-statement\nRESET lock_timeout;\n", 0},
		{"SET lock_timeout = '1s';\nALTER TABLE t ADD COLUMN c int;\n", 1},
		{"SELECT set_config('lock_timeout', '1s', true);\nALTER TABLE t ADD COLUMN c int;\n", 2},
		{"-- acuity:no-transaction\nSELECT set_config('lock_timeout', '15s', false);\n-- acuity:next-statement\nCREATE INDEX CONCURRENTLY i ON t (c);\n", 2},
		{"SET LOCAL lock_timeout = '1s';\nSELECT set_config('lock_timeout', '0', false);\nALTER TABLE t ADD COLUMN c int;\n", 1},
		{"-- acuity:retired\n", 0},
	} {
		if got := migrationSafetyViolations("fixture.sql", test.source); len(got) != test.want {
			t.Errorf("migrationSafetyViolations(%q) = %v, want %d violations", test.source, got, test.want)
		}
	}
}
