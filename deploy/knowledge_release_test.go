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

func TestKnowledgeConfigurationOnlyReachesPortalAPI(t *testing.T) {
	config, err := os.ReadFile(filepath.Join(filepath.Dir(releaseDeployDirectory(t)), "cloudbuild.release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	_, deployStep, found := strings.Cut(string(config), "  - id: deploy\n")
	if !found {
		t.Fatal("Cloud Build deploy step is missing")
	}
	deployStep, _, _ = strings.Cut(deployStep, "\nsubstitutions:")
	var environment []string
	for _, line := range strings.Split(deployStep, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "- KNOWLEDGE_"); ok {
			environment = append(environment, os.Expand("KNOWLEDGE_"+value, func(key string) string {
				return map[string]string{"PROJECT_ID": "acuity-test", "_REGION": "us-east1"}[key]
			}))
		}
	}
	gcloudCapture, output, err := runRelease(t, environment...)
	if err != nil {
		t.Fatalf("release: %v\n%s", err, output)
	}
	commands := capturedGcloudCommands(t, gcloudCapture)
	assertCapturedCommand(t, commands, "run\tdeploy\tacuity-portal-api", "KNOWLEDGE_GOOGLE_PROJECT=acuity-test,KNOWLEDGE_GOOGLE_LOCATION=us-east1")
	for _, command := range commands {
		if strings.Contains(command, "KNOWLEDGE_") && !strings.HasPrefix(command, "run\tdeploy\tacuity-portal-api\t") {
			t.Fatalf("embedding config leaked into another runtime: %s", command)
		}
	}
}

func TestKnowledgeReleaseLeavesConfigurationUnchangedUnlessExplicit(t *testing.T) {
	for _, explicitDisable := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserve", true: "disable"}[explicitDisable], func(t *testing.T) {
			var extra []string
			if explicitDisable {
				extra = append(extra, "KNOWLEDGE_GOOGLE_PROJECT=")
			}
			gcloudCapture, output, err := runRelease(t, extra...)
			if err != nil {
				t.Fatalf("release: %v\n%s", err, output)
			}
			commands := capturedGcloudCommands(t, gcloudCapture)
			if explicitDisable {
				assertCapturedCommand(t, commands, "run\tdeploy\tacuity-portal-api", "KNOWLEDGE_GOOGLE_PROJECT=,KNOWLEDGE_GOOGLE_LOCATION=us-east1")
			} else {
				for _, command := range commands {
					if strings.Contains(command, "KNOWLEDGE_") {
						t.Fatalf("implicit knowledge reconfiguration: %s", command)
					}
				}
			}
		})
	}
}
