package deploy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductReleaseDoesNotConfigureJudgeCredential(t *testing.T) {
	root := filepath.Dir(releaseDeployDirectory(t))
	for _, name := range []string{
		".github/workflows/release.yml", "cloudbuild.release.yaml",
		"deploy/deploy-production-release.sh", "deploy/cloud-run-commands.example.sh",
	} {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "OPENROUTER") {
			t.Errorf("%s still configures a Product judge credential", name)
		}
	}
}

func TestDeploymentWithoutClassifierCredential(t *testing.T) {
	for _, script := range []struct {
		name string
		run  func(*testing.T, ...string) (string, []byte, error)
	}{
		{"release", runRelease},
		{"reference", runCloudRunCommands},
	} {
		t.Run(script.name, func(t *testing.T) {
			capture, output, err := script.run(t)
			if err != nil {
				t.Fatalf("deploy without classifier credential: %v %s", err, output)
			}
			commands := capturedGcloudCommands(t, capture)
			assertCapturedCommand(t, commands, "run\tworker-pools\tdeploy\tacuity-worker")
			for _, command := range commands {
				if strings.Contains(command, "OPENROUTER") {
					t.Fatalf("Product configured a judge credential: %s", command)
				}
			}
		})
	}
}
