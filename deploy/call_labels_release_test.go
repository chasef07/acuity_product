package deploy_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oasdiff/yaml3"
)

func TestCallLabelSecretReachesReleaseBuild(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(releaseDeployDirectory(t)), "cloudbuild.release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var build struct {
		Steps []struct {
			ID  string   `yaml:"id"`
			Env []string `yaml:"env"`
		} `yaml:"steps"`
		Substitutions map[string]string `yaml:"substitutions"`
	}
	if err := yaml.Unmarshal(raw, &build); err != nil {
		t.Fatal(err)
	}
	if build.Substitutions["_OPENROUTER_API_KEY_SECRET"] != "DO_NOT_DEPLOY" {
		t.Error("classifier secret name must require explicit production configuration")
	}
	var environment []string
	for _, step := range build.Steps {
		if step.ID == "deploy" {
			environment = step.Env
		}
	}
	if !slices.Contains(environment, "OPENROUTER_API_KEY_SECRET=${_OPENROUTER_API_KEY_SECRET}") {
		t.Error("Cloud Build does not pass classifier secret name to deployment")
	}
	raw, err = os.ReadFile(filepath.Join(filepath.Dir(releaseDeployDirectory(t)), ".github/workflows/release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"OPENROUTER_API_KEY_SECRET: ${{ vars.OPENROUTER_API_KEY_SECRET }}",
		"_OPENROUTER_API_KEY_SECRET=${OPENROUTER_API_KEY_SECRET}",
	} {
		if !strings.Contains(string(raw), required) {
			t.Errorf("release workflow omits %q", required)
		}
	}
}

func TestCallLabelSecretIsRequiredAndBoundOnlyToWorker(t *testing.T) {
	for _, script := range []struct {
		name string
		run  func(*testing.T, ...string) (string, []byte, error)
	}{
		{"release", runRelease},
		{"reference", runCloudRunCommands},
	} {
		for _, secret := range []string{"", "DO_NOT_DEPLOY", "projects/test/secrets/key", "key:1", "key,EXTRA=1", "openrouter-api-key"} {
			t.Run(script.name+"/"+secret, func(t *testing.T) {
				capture, output, err := script.run(t, "OPENROUTER_API_KEY_SECRET="+secret)
				if secret != "openrouter-api-key" {
					if err == nil || !strings.Contains(string(output), "OPENROUTER_API_KEY_SECRET") {
						t.Fatalf("invalid secret name: %v %s", err, output)
					}
					assertNoReleaseCloudAccess(t, capture)
					return
				}
				if err != nil {
					t.Fatalf("deploy: %v %s", err, output)
				}
				commands := capturedGcloudCommands(t, capture)
				assertCapturedCommand(t, commands, "run\tworker-pools\tdeploy\tacuity-worker", "OPENROUTER_API_KEY=openrouter-api-key:latest")
				for _, command := range commands {
					if strings.Contains(command, "OPENROUTER_API_KEY") && !strings.HasPrefix(command, "run\tworker-pools\tdeploy\tacuity-worker\t") {
						t.Fatalf("classifier credential exposed outside worker: %s", command)
					}
				}
			})
		}
	}
}
