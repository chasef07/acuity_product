package gitsource

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var commitSHA = regexp.MustCompile(`^[a-f0-9]{40}$`)

func Verify(path, commit string, body []byte) error {
	if !commitSHA.MatchString(commit) {
		return errors.New("the commit must be a full lowercase Git commit SHA")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	directory, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return errors.New("could not resolve the source file's directory")
	}
	absolute = filepath.Join(directory, filepath.Base(absolute))
	rootBytes, err := exec.Command("git", "-C", directory, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return errors.New("the source file must be inside its Git repository")
	}
	root := strings.TrimSpace(string(rootBytes))
	relative, err := filepath.Rel(root, absolute)
	if err != nil || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("the source file is outside its Git repository")
	}
	committed, err := exec.Command("git", "-C", root, "show", commit+":"+filepath.ToSlash(relative)).Output()
	if err != nil {
		return errors.New("the source file does not exist at the declared Git commit")
	}
	if !bytes.Equal(committed, body) {
		return errors.New("the source file differs from the declared Git commit; commit the reviewed changes before publishing")
	}
	return nil
}
