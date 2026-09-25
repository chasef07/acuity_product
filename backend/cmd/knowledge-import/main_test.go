package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportFileCannotForgeActorOrIgnoreExtraInput(t *testing.T) {
	for _, body := range []string{`{"actorSubject":"someone-else"}`, `{} {}`, `{"patientId":"synthetic-patient"}`} {
		if _, err := readCommand(strings.NewReader(body)); err == nil {
			t.Fatalf("accepted unsafe import input %s", body)
		}
	}
	if _, err := readCommand(strings.NewReader(`{"officeKey":"synthetic-office","sections":[{"id":"hours","title":"Hours","text":"Monday closes at 5pm."}]}`)); err != nil {
		t.Fatal(err)
	}
}

func TestSourceRejectsUnreviewedMetadataAndTrailingDocuments(t *testing.T) {
	for _, body := range []string{
		"practiceId: x\nactorSubject: forged\n",
		"officeKey: a\nofficeKey: b\n",
		"officeKey: a\n---\nofficeKey: b\n",
		"officeKey: a\nentries:\n- id: hours\n  patientId: forbidden\n",
		"officeKey: a\n" + strings.Repeat(" ", 1<<20) + "---\n",
	} {
		if _, err := readSource(strings.NewReader(body)); err == nil {
			t.Fatalf("accepted invalid source: %s", body)
		}
	}
}

func TestSourceProvenanceRequiresExactCommittedBytes(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git failed: %v: %s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "--quiet")
	path := filepath.Join(dir, "office.yaml")
	body := []byte("officeKey: synthetic-office\n")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "office.yaml")
	git("-c", "user.name=Synthetic Test", "-c", "user.email=synthetic@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "test source")
	commit := git("rev-parse", "HEAD")
	if err := verifySourceAtCommit(path, commit, body); err != nil {
		t.Fatal(err)
	}
	if err := verifySourceAtCommit(path, commit, []byte("changed")); err == nil {
		t.Fatal("accepted content not present at declared commit")
	}
	if err := verifySourceAtCommit(path, strings.Repeat("0", 40), body); err == nil {
		t.Fatal("accepted unknown commit")
	}
}
