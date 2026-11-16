package gitsource

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyRequiresExactCommittedBytes(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git failed: %v: %s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "--quiet")
	path := filepath.Join(dir, "source.yaml")
	body := []byte("officeKey: synthetic-office\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "source.yaml")
	git("-c", "user.name=Synthetic Test", "-c", "user.email=synthetic@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "test source")
	commit := git("rev-parse", "HEAD")
	if err := Verify(path, commit, body); err != nil {
		t.Fatal(err)
	}
	if err := Verify(path, commit, []byte("changed")); err == nil {
		t.Fatal("accepted content not present at the declared commit")
	}
	if err := Verify(path, strings.Repeat("0", 40), body); err == nil {
		t.Fatal("accepted an unknown commit")
	}
	for _, malformed := range []string{"", commit[:12], strings.ToUpper(commit)} {
		if err := Verify(path, malformed, body); err == nil {
			t.Fatalf("accepted malformed commit %q", malformed)
		}
	}
	outside := filepath.Join(t.TempDir(), "source.yaml")
	if err := os.WriteFile(outside, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(outside, commit, body); err == nil {
		t.Fatal("accepted a file outside any Git repository")
	}
}
