package deploy_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oasdiff/yaml3"
)

func TestCloudBuildPassesEmptyMiddlewareInputsByDefault(t *testing.T) {
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
	var deployEnvironment []string
	for _, step := range build.Steps {
		if step.ID == "deploy" {
			deployEnvironment = step.Env
		}
	}
	for _, name := range []string{"MIDDLEWARE_BASE_URL", "MIDDLEWARE_API_SECRET_SECRET"} {
		if value, ok := build.Substitutions["_"+name]; !ok || value != "" {
			t.Errorf("substitution _%s must default to empty, got %q (defined %t)", name, value, ok)
		}
		if !slices.Contains(deployEnvironment, name+"=${_"+name+"}") {
			t.Errorf("Cloud Build deploy step does not pass %s", name)
		}
	}
}

func TestProductionReleaseUpdatesPortalAPIMiddlewareInPlace(t *testing.T) {
	gcloudCapture, output, err := runRelease(t,
		"MIDDLEWARE_BASE_URL=https://middleware.example",
		"MIDDLEWARE_API_SECRET_SECRET=middleware-api-secret",
	)
	if err != nil {
		t.Fatalf("release: %v\n%s", err, output)
	}
	assertCapturedCommand(t, capturedGcloudCommands(t, gcloudCapture), "run\tdeploy\tacuity-portal-api",
		"--update-env-vars\tDATABASE_POOL_MAX=4,DATABASE_ACQUIRE_TIMEOUT_MS=1500,HUMAN_CALLING_RING_WINDOW_SECONDS=20,MIDDLEWARE_BASE_URL=https://middleware.example",
		"--update-secrets\tMIDDLEWARE_API_SECRET=middleware-api-secret:latest",
	)
}

func TestMiddlewareInputsReachOnlyPortalAPIOrFailBeforeCloudAccess(t *testing.T) {
	for _, script := range []struct {
		name string
		run  func(*testing.T, ...string) (string, []byte, error)
	}{
		{"release", runRelease},
		{"cloud-run-commands", runCloudRunCommands},
	} {
		for _, scenario := range []struct {
			name, url, secret, message string
		}{
			{"neither", "", "", ""},
			{"port and path", "https://middleware.example:8443/api", "middleware-api-secret", ""},
			{"base URL without secret", "https://middleware.example", "", "must be set together"},
			{"secret without base URL", "", "middleware-api-secret", "must be set together"},
			{"plain http", "http://middleware.example", "middleware-api-secret", "MIDDLEWARE_BASE_URL must be an https:// base URL"},
			{"missing scheme", "middleware.example", "middleware-api-secret", "MIDDLEWARE_BASE_URL must be an https:// base URL"},
			{"non-numeric port", "https://middleware.example:abc", "middleware-api-secret", "MIDDLEWARE_BASE_URL must be an https:// base URL"},
			{"comma", "https://middleware.example,EXTRA=1", "middleware-api-secret", "MIDDLEWARE_BASE_URL must be an https:// base URL"},
			{"query", "https://middleware.example/?token=1", "middleware-api-secret", "MIDDLEWARE_BASE_URL must be an https:// base URL"},
			{"credentials", "https://user:pass@middleware.example", "middleware-api-secret", "MIDDLEWARE_BASE_URL must be an https:// base URL"},
			{"secret path", "https://middleware.example", "projects/acuity-test/secrets/middleware", "MIDDLEWARE_API_SECRET_SECRET must be a Secret Manager secret name"},
			{"secret version", "https://middleware.example", "middleware-api-secret:1", "MIDDLEWARE_API_SECRET_SECRET must be a Secret Manager secret name"},
		} {
			t.Run(script.name+"/"+scenario.name, func(t *testing.T) {
				capture, output, err := script.run(t,
					"MIDDLEWARE_BASE_URL="+scenario.url,
					"MIDDLEWARE_API_SECRET_SECRET="+scenario.secret,
				)
				if scenario.message != "" {
					if err == nil || !strings.Contains(string(output), scenario.message) {
						t.Fatalf("error = %v %q, want %q", err, output, scenario.message)
					}
					assertNoReleaseCloudAccess(t, capture)
					return
				}
				if err != nil {
					t.Fatalf("%v\n%s", err, output)
				}
				commands := capturedGcloudCommands(t, capture)
				for _, command := range commands {
					if (strings.Contains(command, "MIDDLEWARE_") || strings.Contains(command, "--update-secrets")) &&
						(scenario.url == "" || !strings.HasPrefix(command, "run\tdeploy\tacuity-portal-api\t")) {
						t.Fatalf("unexpected middleware configuration: %s", command)
					}
				}
				if scenario.url != "" {
					assertCapturedCommand(t, commands, "run\tdeploy\tacuity-portal-api",
						"MIDDLEWARE_BASE_URL="+scenario.url,
						"MIDDLEWARE_API_SECRET="+scenario.secret+":latest",
					)
				}
			})
		}
	}
}
