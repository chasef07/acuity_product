package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/google/uuid"
	"github.com/oasdiff/yaml3"
)

// Source contains only reviewed content and routing, never execution credentials.
type Source struct {
	PracticeID string  `yaml:"practiceId" json:"practiceId"`
	OfficeKey  string  `yaml:"officeKey" json:"officeKey"`
	Entries    []Entry `yaml:"entries" json:"entries"`
}
type Entry struct {
	ID    string `yaml:"id" json:"id"`
	Title string `yaml:"title" json:"title"`
	Text  string `yaml:"text" json:"text"`
}

func readSource(r io.Reader) (Source, error) {
	var source Source
	body, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil {
		return source, errors.New("could not read knowledge source")
	}
	if len(body) > 1<<20 {
		return source, errors.New("knowledge source exceeds 1 MiB limit")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&source); err != nil {
		return source, fmt.Errorf("invalid knowledge YAML: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return source, errors.New("knowledge source must contain exactly one YAML document")
	}
	return source, nil
}

func sourceCommand(source Source, commit, expected string) (knowledge.ImportCommand, error) {
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(commit) {
		return knowledge.ImportCommand{}, errors.New("--commit must be a full lowercase Git commit SHA")
	}
	sections := make([]knowledge.Section, len(source.Entries))
	for i, entry := range source.Entries {
		sections[i] = knowledge.Section{ID: entry.ID, Title: entry.Title, Text: entry.Text}
	}
	body, err := json.Marshal(source)
	if err != nil {
		return knowledge.ImportCommand{}, err
	}
	// Same reviewed commit/content/parent is replay-safe. Republishing after another
	// revision receives a distinct ID; concurrency is still enforced by ReplaceCorpus.
	id := uuid.NewSHA1(uuid.NameSpaceURL, append([]byte(commit+"\n"+expected+"\n"), body...)).String()
	if expected == "none" {
		expected = ""
	}
	command := knowledge.ImportCommand{ID: id, PracticeID: source.PracticeID, OfficeKey: source.OfficeKey, Sections: sections, ExpectedRevisionID: expected, Provenance: "git:" + commit, Reason: "Publish reviewed Git knowledge", ActorSubject: "validation-only"}
	if err := knowledge.ValidateImport(command); err != nil {
		return command, err
	}
	command.ActorSubject = ""
	return command, nil
}

func runSource(path, commit, expected string, apply bool) error {
	if !apply && commit == "" {
		commit = "0000000000000000000000000000000000000000"
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return errors.New("could not open knowledge source")
	}
	source, err := readSource(bytes.NewReader(body))
	if err != nil {
		return err
	}
	command, err := sourceCommand(source, commit, expected)
	if err != nil {
		return err
	}
	if apply {
		if err := verifySourceAtCommit(path, commit, body); err != nil {
			return err
		}
	}
	return applyPublication(command, apply, expected == "")
}

// Publication provenance must identify the exact reviewed bytes, not a caller claim.
func verifySourceAtCommit(path, commit string, body []byte) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	directory, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return errors.New("could not resolve knowledge source directory")
	}
	absolute = filepath.Join(directory, filepath.Base(absolute))
	rootBytes, err := exec.Command("git", "-C", directory, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return errors.New("knowledge source must be inside its Git repository")
	}
	root := strings.TrimSpace(string(rootBytes))
	relative, err := filepath.Rel(root, absolute)
	if err != nil || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("knowledge source is outside its Git repository")
	}
	committed, err := exec.Command("git", "-C", root, "show", commit+":"+filepath.ToSlash(relative)).Output()
	if err != nil {
		return errors.New("knowledge source does not exist at the declared Git commit")
	}
	if !bytes.Equal(committed, body) {
		return errors.New("knowledge source differs from the declared Git commit; commit reviewed changes before publishing")
	}
	return nil
}

func exportSource(practice, office string) error {
	if _, err := uuid.Parse(practice); err != nil || office == "" {
		return errors.New("export requires --practice UUID and --office key")
	}
	if os.Getenv("KNOWLEDGE_IMPORT_DATABASE_URL") == "" {
		return errors.New("KNOWLEDGE_IMPORT_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	// A single SQL statement observes one active revision and all its passages.
	rows, err := pool.Query(ctx, `SELECT c.revision_id::text,p.section_id,p.title,p.text FROM knowledge_corpora c JOIN knowledge_passages p ON p.revision_id=c.revision_id WHERE c.practice_id=$1 AND c.office_key=$2 ORDER BY p.section_id`, practice, office)
	if err != nil {
		return errors.New("could not read active corpus")
	}
	defer rows.Close()
	source := Source{PracticeID: practice, OfficeKey: office}
	var revision string
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&revision, &entry.ID, &entry.Title, &entry.Text); err != nil {
			return err
		}
		source.Entries = append(source.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(source.Entries) == 0 {
		return errors.New("no active office corpus found")
	}
	encoder := yaml.NewEncoder(os.Stdout)
	encoder.SetIndent(2)
	if err := encoder.Encode(source); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return json.NewEncoder(os.Stderr).Encode(map[string]any{"officeKey": office, "revisionId": revision, "entries": len(source.Entries)})
}
