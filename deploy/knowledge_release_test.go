package deploy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oasdiff/yaml3"
)

func TestNoWorkflowPublishesKnowledge(t *testing.T) {
	root := filepath.Dir(releaseDeployDirectory(t))
	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.y*ml"))
	if err != nil || len(workflows) == 0 {
		t.Fatalf("workflows: %v, %v", workflows, err)
	}
	for _, path := range workflows {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var workflow map[string]any
		if err := yaml.Unmarshal(raw, &workflow); err != nil {
			t.Fatal(err)
		}
		if jobs, _ := workflow["jobs"].(map[string]any); jobs["knowledge"] != nil || strings.Contains(string(raw), "knowledge-import") || strings.Contains(string(raw), "knowledge/offices") {
			t.Fatalf("%s publishes knowledge; each office's active database revision is its only source", filepath.Base(path))
		}
	}
}
