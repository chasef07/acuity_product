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

func TestSourcePublicationKeepsReviewedContentAndReplayIdentity(t *testing.T) {
	body := "practiceId: 11111111-1111-4111-8111-111111111111\nofficeKey: synthetic-office\nentries:\n- id: closure\n  title: Former office\n  text: >-\n    The former office is permanently closed.\n    Do not book appointments there.\n"
	source, err := readSource(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.Repeat("a", 40)
	first, err := sourceCommand(source, commit, "none")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := sourceCommand(source, commit, "none")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != replay.ID || first.ActorSubject != "" || first.ExpectedRevisionID != "" || first.Provenance != "git:"+commit || first.Sections[0].Text != "The former office is permanently closed. Do not book appointments there." {
		t.Fatalf("unexpected publication: %#v", first)
	}
	source.Entries[0].Text = "Reviewed replacement."
	changed, err := sourceCommand(source, commit, "none")
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID == first.ID {
		t.Fatal("changed content reused publication identity")
	}
	changed, err = sourceCommand(source, commit, "22222222-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID == first.ID {
		t.Fatal("different parent reused publication identity")
	}
	if _, err := sourceCommand(source, "main", "none"); err == nil {
		t.Fatal("accepted mutable revision as provenance")
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
