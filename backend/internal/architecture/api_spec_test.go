package architecture

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var deprecatedSpecMarker = regexp.MustCompile(`(?m)^\s*deprecated:\s*true\b`)

func TestAPISpecDeletesReplacedFieldsInsteadOfDeprecating(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join(repositoryRoot(t), "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if deprecatedSpecMarker.Match(spec) {
		t.Fatal("api/openapi.yaml marks something deprecated: true; delete the replaced field, parameter, or operation and update every caller in the same change instead of keeping a shim")
	}
}
