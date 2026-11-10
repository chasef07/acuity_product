package deploy_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oasdiff/yaml3"
)

type releaseCloudBuild struct {
	Steps []struct {
		ID   string   `yaml:"id"`
		Args []string `yaml:"args"`
		Env  []string `yaml:"env"`
	} `yaml:"steps"`
	Substitutions map[string]string `yaml:"substitutions"`
}

func readReleaseCloudBuild(t *testing.T) releaseCloudBuild {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(releaseDeployDirectory(t)), "cloudbuild.release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var build releaseCloudBuild
	if err := yaml.Unmarshal(raw, &build); err != nil {
		t.Fatal(err)
	}
	return build
}

func TestCloudBuildRequiresTheWebAPIURLsItChecksAtDeploy(t *testing.T) {
	build := readReleaseCloudBuild(t)
	steps := map[string][]string{}
	for _, step := range build.Steps {
		steps[step.ID] = append(step.Args, step.Env...)
	}
	for _, name := range []string{"PORTAL_API_URL", "REALTIME_URL"} {
		if value := build.Substitutions["_"+name]; value != "DO_NOT_DEPLOY" {
			t.Errorf("substitution _%s must have no deployable default, got %q", name, value)
		}
		if !slices.Contains(steps["build-web"], "NEXT_PUBLIC_"+name+"=${_"+name+"}") {
			t.Errorf("web build does not bake in _%s", name)
		}
		if !slices.Contains(steps["deploy"], name+"=${_"+name+"}") {
			t.Errorf("deploy step does not receive _%s to check it", name)
		}
	}
}

func TestBackendImageCarriesOnlyItsEnvironmentsProvisioning(t *testing.T) {
	root := filepath.Dir(releaseDeployDirectory(t))
	build := readReleaseCloudBuild(t)
	if value := build.Substitutions["_RELEASE_ENVIRONMENT"]; value != "DO_NOT_DEPLOY" {
		t.Errorf("substitution _RELEASE_ENVIRONMENT must have no deployable default, got %q", value)
	}
	var backendArgs []string
	for _, step := range build.Steps {
		if step.ID == "build-backend" {
			backendArgs = step.Args
		}
	}
	if !slices.Contains(backendArgs, "RELEASE_ENVIRONMENT=${_RELEASE_ENVIRONMENT}") {
		t.Error("backend build does not choose its environment's provisioning")
	}
	dockerfile, err := os.ReadFile(filepath.Join(root, "Dockerfile.backend"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"COPY config/${RELEASE_ENVIRONMENT}-provisioning.json /etc/acuity/provisioning.json",
		"COPY config/${RELEASE_ENVIRONMENT}-task-responsibilities.json /etc/acuity/task-responsibilities.json",
	} {
		if !strings.Contains(string(dockerfile), required) {
			t.Errorf("Dockerfile.backend omits %q", required)
		}
	}
	for _, environment := range []string{"production", "staging"} {
		for _, file := range []string{"-provisioning.json", "-task-responsibilities.json"} {
			if _, err := os.Stat(filepath.Join(root, "config", environment+file)); err != nil {
				t.Errorf("%s release has no %s: %v", environment, environment+file, err)
			}
		}
	}
}
