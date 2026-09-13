package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oasdiff/yaml3"
)

func TestKnowledgePublicationWaitsForDeployedRelease(t *testing.T) {
	root := filepath.Dir(releaseDeployDirectory(t))
	read := func(name string) map[string]any {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		var workflow map[string]any
		if err := yaml.Unmarshal(raw, &workflow); err != nil {
			t.Fatal(err)
		}
		return workflow
	}
	release := read("release.yml")
	jobs := release["jobs"].(map[string]any)
	publication, ok := jobs["knowledge"].(map[string]any)
	if !ok {
		t.Fatal("release must publish knowledge after successful deployment")
	}
	if publication["uses"] != "./.github/workflows/knowledge.yml" {
		t.Fatal("release must use the knowledge workflow")
	}
	needs := publication["needs"].([]any)
	if len(needs) != 2 || needs[0] != "release-please" || needs[1] != "deploy" {
		t.Fatal("publication must depend on deployment and its release SHA")
	}
	if _, ok := publication["if"]; ok {
		t.Fatal("publication must retain the default success gate")
	}
	if publication["with"].(map[string]any)["release_sha"] != "${{ needs.release-please.outputs.release_sha }}" {
		t.Fatal("publication must use the deployed release")
	}
	knowledge := read("knowledge.yml")
	publish := knowledge["jobs"].(map[string]any)["publish"].(map[string]any)
	if strings.Contains(publish["if"].(string), "github.event_name == 'push'") {
		t.Fatal("a push must not publish ahead of deployment")
	}
	steps := publish["steps"].([]any)
	checkout := steps[0].(map[string]any)
	if checkout["with"].(map[string]any)["ref"] != "${{ inputs.release_sha || github.sha }}" {
		t.Fatal("publisher must check out the deployed commit")
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
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	command := exec.Command("bash", filepath.Join(releaseDeployDirectory(t), "deploy-production-release.sh"))
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
	}, releaseEnvironment()...)
	// Exercise the environment supplied by Cloud Build, not manually injected
	// knowledge settings that can hide a broken release configuration.
	for _, line := range strings.Split(deployStep, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "- KNOWLEDGE_"); ok {
			command.Env = append(command.Env, os.Expand("KNOWLEDGE_"+value, func(key string) string {
				return map[string]string{"PROJECT_ID": "acuity-test", "_REGION": "us-east1"}[key]
			}))
		}
	}
	if output, err := command.CombinedOutput(); err != nil {
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
			path, gcloudCapture, curlCapture := installReleaseFakes(t)
			command := exec.Command("bash", filepath.Join(releaseDeployDirectory(t), "deploy-production-release.sh"))
			command.Env = append([]string{"PATH=" + path, "GCLOUD_CAPTURE=" + gcloudCapture, "CURL_CAPTURE=" + curlCapture}, releaseEnvironment()...)
			if explicitDisable {
				command.Env = append(command.Env, "KNOWLEDGE_GOOGLE_PROJECT=")
			}
			if output, err := command.CombinedOutput(); err != nil {
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
