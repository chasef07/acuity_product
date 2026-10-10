package deploy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oasdiff/yaml3"
)

func TestKnowledgePublishesAfterEachReleaseDeploy(t *testing.T) {
	root := filepath.Dir(releaseDeployDirectory(t))
	if _, err := os.Stat(filepath.Join(root, ".github", "workflows", "knowledge.yml")); !os.IsNotExist(err) {
		t.Fatal("knowledge must publish only through the Release workflow")
	}
	raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var release map[string]any
	if err := yaml.Unmarshal(raw, &release); err != nil {
		t.Fatal(err)
	}
	knowledge, ok := release["jobs"].(map[string]any)["knowledge"].(map[string]any)
	if !ok {
		t.Fatal("release workflow must publish knowledge")
	}
	if knowledge["continue-on-error"] != true {
		t.Fatal("knowledge publication must not determine application release status")
	}
	if knowledge["if"] != "${{ !cancelled() && needs.deploy.result == 'success' }}" {
		t.Fatal("knowledge must publish after every successful deploy, including when verification was already done by CI")
	}
	concurrency, _ := knowledge["concurrency"].(map[string]any)
	if concurrency["group"] != "knowledge-production" || concurrency["cancel-in-progress"] != false {
		t.Fatal("knowledge publications must run one at a time")
	}
	needs, _ := knowledge["needs"].([]any)
	if len(needs) != 2 || needs[0] != "release-please" || needs[1] != "deploy" {
		t.Fatalf("knowledge publication must follow the deploy: %v", needs)
	}
	steps := knowledge["steps"].([]any)
	if steps[0].(map[string]any)["with"].(map[string]any)["ref"] != "${{ needs.release-please.outputs.release_sha }}" {
		t.Fatal("knowledge must publish from the released commit")
	}
	published := false
	for _, value := range steps {
		step := value.(map[string]any)
		if step["name"] != "Publish and verify office knowledge" {
			continue
		}
		env := step["env"].(map[string]any)
		published = env["KNOWLEDGE_COMMIT"] == "${{ needs.release-please.outputs.release_sha }}" &&
			env["KNOWLEDGE_SERVICE_TOKEN_SECRETS"] == "${{ vars.KNOWLEDGE_SERVICE_TOKEN_SECRETS }}"
	}
	if !published {
		t.Fatal("publisher must receive the released commit and practice-scoped verification secret mappings")
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
