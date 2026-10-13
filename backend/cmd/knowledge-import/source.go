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

	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/google/uuid"
	yaml "github.com/oasdiff/yaml3"
)

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

func sourceCommand(source Source, commit string) (knowledge.ImportCommand, error) {
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
	id := uuid.NewSHA1(uuid.NameSpaceURL, append([]byte(commit+"\n"), body...)).String()
	command := knowledge.ImportCommand{ID: id, PracticeID: source.PracticeID, OfficeKey: source.OfficeKey, Sections: sections, Provenance: "git:" + commit, Reason: "Set up office knowledge", ActorSubject: "validation-only"}
	if err := knowledge.ValidateImport(command); err != nil {
		return command, err
	}
	command.ActorSubject = ""
	return command, nil
}

func runSource(path, commit string, apply bool) error {
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
	command, err := sourceCommand(source, commit)
	if err != nil {
		return err
	}
	if apply {
		if err := verifySourceAtCommit(path, commit, body); err != nil {
			return err
		}
	}
	return applyPublication(command, apply)
}

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

func exportSource(ctx context.Context, module *knowledge.Module, practice, office, revision string, w io.Writer) (string, error) {
	if _, err := uuid.Parse(practice); err != nil || office == "" {
		return "", errors.New("export requires --practice UUID and --office key")
	}
	exported, sections, err := module.ReadRevision(ctx, practice, office, revision)
	if err != nil {
		return "", fmt.Errorf("could not read the office revision: %w", err)
	}
	source := Source{PracticeID: practice, OfficeKey: office, Entries: make([]Entry, len(sections))}
	for i, section := range sections {
		source.Entries[i] = Entry{ID: section.ID, Title: section.Title, Text: section.Text}
	}
	encoder := yaml.NewEncoder(w)
	encoder.SetIndent(2)
	if err := encoder.Encode(source); err != nil {
		return "", err
	}
	return exported, encoder.Close()
}
