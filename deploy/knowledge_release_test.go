package deploy_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeConfigurationOnlyReachesPortalAPI(t *testing.T) {
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	command := exec.Command("bash", filepath.Join(releaseDeployDirectory(t), "deploy-production-release.sh"))
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
		"KNOWLEDGE_GOOGLE_PROJECT=acuity-test",
		"KNOWLEDGE_GOOGLE_LOCATION=us-east1",
	}, releaseEnvironment()...)
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
