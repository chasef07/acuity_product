package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oasdiff/yaml3"
)

func runMiddlewareRelease(t *testing.T, extra ...string) ([]string, []byte, error) {
	t.Helper()
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	command := exec.Command("bash", filepath.Join(releaseDeployDirectory(t), "deploy-production-release.sh"))
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
	}, releaseEnvironment()...)
	command.Env = append(command.Env, extra...)
	output, err := command.CombinedOutput()
	var commands []string
	if _, statErr := os.Stat(gcloudCapture); statErr == nil {
		commands = capturedGcloudCommands(t, gcloudCapture)
	}
	return commands, output, err
}

func TestProductionReleaseMiddlewareLeavesCommandsUnchangedUnlessProvided(t *testing.T) {
	baseline, output, err := runMiddlewareRelease(t)
	if err != nil {
		t.Fatalf("release: %v\n%s", err, output)
	}
	for _, command := range baseline {
		if strings.Contains(command, "MIDDLEWARE_") || strings.Contains(command, "--update-secrets") {
			t.Fatalf("release changed middleware configuration without inputs: %s", command)
		}
	}
	empty, output, err := runMiddlewareRelease(t, "MIDDLEWARE_BASE_URL=", "MIDDLEWARE_API_SECRET_SECRET=")
	if err != nil {
		t.Fatalf("release with empty inputs: %v\n%s", err, output)
	}
	if strings.Join(empty, "\n") != strings.Join(baseline, "\n") {
		t.Fatalf("empty middleware inputs changed release commands:\n%s\nwant:\n%s", strings.Join(empty, "\n"), strings.Join(baseline, "\n"))
	}
}

func TestProductionReleaseMiddlewareReachesOnlyPortalAPI(t *testing.T) {
	commands, output, err := runMiddlewareRelease(t,
		"MIDDLEWARE_BASE_URL=https://middleware.example",
		"MIDDLEWARE_API_SECRET_SECRET=middleware-api-secret",
	)
	if err != nil {
		t.Fatalf("release: %v\n%s", err, output)
	}
	assertCapturedCommand(t, commands, "run\tdeploy\tacuity-portal-api",
		"--update-env-vars\tDATABASE_POOL_MAX=4,DATABASE_ACQUIRE_TIMEOUT_MS=1500,HUMAN_CALLING_RING_WINDOW_SECONDS=20,MIDDLEWARE_BASE_URL=https://middleware.example",
		"--update-secrets\tMIDDLEWARE_API_SECRET=middleware-api-secret:latest",
	)
	for _, command := range commands {
		if strings.HasPrefix(command, "run\tdeploy\tacuity-portal-api\t") {
			if strings.Contains(command, "--set-secrets") || strings.Contains(command, "--set-env-vars") {
				t.Fatalf("middleware release replaced the portal-api configuration: %s", command)
			}
			continue
		}
		if strings.Contains(command, "MIDDLEWARE_") || strings.Contains(command, "--update-secrets") {
			t.Fatalf("middleware configuration leaked into another runtime: %s", command)
		}
	}
}

func TestProductionReleaseMiddlewareRejectsInvalidInputsBeforeCloudMutation(t *testing.T) {
	for _, scenario := range []struct {
		name, url, secret, message string
	}{
		{"base URL without secret", "https://middleware.example", "", "must be set together"},
		{"secret without base URL", "", "middleware-api-secret", "must be set together"},
		{"plain http", "http://middleware.example", "middleware-api-secret", "MIDDLEWARE_BASE_URL must be an https:// base URL"},
		{"missing scheme", "middleware.example", "middleware-api-secret", "MIDDLEWARE_BASE_URL must be an https:// base URL"},
		{"comma", "https://middleware.example,EXTRA=1", "middleware-api-secret", "MIDDLEWARE_BASE_URL must be an https:// base URL"},
		{"query", "https://middleware.example/?token=1", "middleware-api-secret", "MIDDLEWARE_BASE_URL must be an https:// base URL"},
		{"credentials", "https://user:pass@middleware.example", "middleware-api-secret", "MIDDLEWARE_BASE_URL must be an https:// base URL"},
		{"secret path", "https://middleware.example", "projects/acuity-test/secrets/middleware", "MIDDLEWARE_API_SECRET_SECRET must be a Secret Manager secret name"},
		{"secret version", "https://middleware.example", "middleware-api-secret:1", "MIDDLEWARE_API_SECRET_SECRET must be a Secret Manager secret name"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			commands, output, err := runMiddlewareRelease(t,
				"MIDDLEWARE_BASE_URL="+scenario.url,
				"MIDDLEWARE_API_SECRET_SECRET="+scenario.secret,
			)
			if err == nil {
				t.Fatalf("release accepted %s", scenario.name)
			}
			if !strings.Contains(string(output), scenario.message) {
				t.Fatalf("release error = %q, want %q", output, scenario.message)
			}
			for _, command := range commands {
				if !strings.HasPrefix(command, "artifacts\tdocker\timages\tdescribe\t") {
					t.Fatalf("invalid middleware inputs reached Cloud Run: %s", command)
				}
			}
		})
	}
}

func TestMiddlewareReleaseInputsReachDeployStep(t *testing.T) {
	root := filepath.Dir(releaseDeployDirectory(t))
	raw, err := os.ReadFile(filepath.Join(root, "cloudbuild.release.yaml"))
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
	var deployEnvironment []string
	for _, step := range build.Steps {
		if step.ID == "deploy" {
			deployEnvironment = step.Env
		}
	}
	for _, required := range []string{
		"MIDDLEWARE_BASE_URL=${_MIDDLEWARE_BASE_URL}",
		"MIDDLEWARE_API_SECRET_SECRET=${_MIDDLEWARE_API_SECRET_SECRET}",
	} {
		found := false
		for _, value := range deployEnvironment {
			found = found || value == required
		}
		if !found {
			t.Errorf("Cloud Build deploy step omits %q", required)
		}
	}
	for _, name := range []string{"_MIDDLEWARE_BASE_URL", "_MIDDLEWARE_API_SECRET_SECRET"} {
		value, ok := build.Substitutions[name]
		if !ok || value != "" {
			t.Errorf("Cloud Build substitution %s must default to empty, got %q (defined %t)", name, value, ok)
		}
	}
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"MIDDLEWARE_BASE_URL: ${{ vars.MIDDLEWARE_BASE_URL }}",
		"MIDDLEWARE_API_SECRET_SECRET: ${{ vars.MIDDLEWARE_API_SECRET_SECRET }}",
		"_MIDDLEWARE_BASE_URL=${MIDDLEWARE_BASE_URL},_MIDDLEWARE_API_SECRET_SECRET=${MIDDLEWARE_API_SECRET_SECRET}",
	} {
		if !strings.Contains(string(workflow), required) {
			t.Errorf("release workflow omits %q", required)
		}
	}
}

func TestCloudRunCommandsBindOptionalMiddlewareToPortalAPI(t *testing.T) {
	directory := productionDeployDirectory(t)
	run := func(t *testing.T, extra ...string) ([]string, []byte, error) {
		t.Helper()
		path, capture := installFakeGcloud(t)
		command := exec.Command("sh", filepath.Join(directory, "cloud-run-commands.example.sh"))
		command.Env = append([]string{"PATH=" + path, "GCLOUD_CAPTURE=" + capture}, productionRuntimeEnvironment()...)
		command.Env = append(command.Env, extra...)
		output, err := command.CombinedOutput()
		var commands []string
		if _, statErr := os.Stat(capture); statErr == nil {
			commands = capturedGcloudCommands(t, capture)
		}
		return commands, output, err
	}
	t.Run("not provided", func(t *testing.T) {
		commands, output, err := run(t)
		if err != nil {
			t.Fatalf("render: %v\n%s", err, output)
		}
		for _, command := range commands {
			if strings.Contains(command, "MIDDLEWARE_") {
				t.Fatalf("middleware configuration rendered without inputs: %s", command)
			}
		}
	})
	t.Run("provided", func(t *testing.T) {
		commands, output, err := run(t,
			"MIDDLEWARE_BASE_URL=https://middleware.example",
			"MIDDLEWARE_API_SECRET_SECRET=middleware-api-secret",
		)
		if err != nil {
			t.Fatalf("render: %v\n%s", err, output)
		}
		assertCapturedCommand(t, commands, "run\tdeploy\tacuity-portal-api",
			"TELNYX_API_KEY=telnyx-api-key:latest,MIDDLEWARE_API_SECRET=middleware-api-secret:latest",
			"MIDDLEWARE_BASE_URL=https://middleware.example",
		)
		for _, command := range commands {
			if strings.Contains(command, "MIDDLEWARE_") && !strings.HasPrefix(command, "run\tdeploy\tacuity-portal-api\t") {
				t.Fatalf("middleware configuration leaked into another runtime: %s", command)
			}
		}
	})
	for _, scenario := range []struct {
		name, url, secret, message string
	}{
		{"only base URL", "https://middleware.example", "", "must be set together"},
		{"only secret", "", "middleware-api-secret", "must be set together"},
		{"plain http", "http://middleware.example", "middleware-api-secret", "MIDDLEWARE_BASE_URL must be an https:// base URL"},
		{"query", "https://middleware.example/?token=1", "middleware-api-secret", "MIDDLEWARE_BASE_URL must be an https:// base URL"},
		{"secret path", "https://middleware.example", "projects/acuity-test/secrets/middleware", "MIDDLEWARE_API_SECRET_SECRET must be a Secret Manager secret name"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			commands, output, err := run(t,
				"MIDDLEWARE_BASE_URL="+scenario.url,
				"MIDDLEWARE_API_SECRET_SECRET="+scenario.secret,
			)
			if err == nil {
				t.Fatalf("Cloud Run commands accepted %s", scenario.name)
			}
			if !strings.Contains(string(output), scenario.message) {
				t.Fatalf("error = %q, want %q", output, scenario.message)
			}
			if len(commands) != 0 {
				t.Fatalf("invalid middleware inputs reached gcloud: %s", strings.Join(commands, "\n"))
			}
		})
	}
}
