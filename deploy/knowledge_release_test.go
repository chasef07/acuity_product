package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
