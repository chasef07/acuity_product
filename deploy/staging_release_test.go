package deploy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oasdiff/yaml3"
)

type releaseWorkflowJob struct {
	If          string   `yaml:"if"`
	Needs       []string `yaml:"needs"`
	Environment struct {
		Name string `yaml:"name"`
	} `yaml:"environment"`
	Steps []struct {
		With map[string]string `yaml:"with"`
		Run  string            `yaml:"run"`
	} `yaml:"steps"`
}

func readReleaseWorkflowJobs(t *testing.T) map[string]releaseWorkflowJob {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(releaseDeployDirectory(t)), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]releaseWorkflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow.Jobs
}

func TestStagingDeploysEveryVerifiedMainPushBeforeProduction(t *testing.T) {
	jobs := readReleaseWorkflowJobs(t)
	staging, ok := jobs["staging"]
	if !ok {
		t.Fatal("release workflow has no staging job")
	}
	if staging.If != jobs["release-please"].If {
		t.Errorf("staging runs on %q, want every verified push to main like release-please (%q)", staging.If, jobs["release-please"].If)
	}
	if staging.Environment.Name != "staging" {
		t.Errorf("staging job environment = %q", staging.Environment.Name)
	}
	var script string
	var checkoutRef string
	var auth map[string]string
	for _, step := range staging.Steps {
		script += step.Run
		if ref, ok := step.With["ref"]; ok {
			checkoutRef = ref
		}
		if _, ok := step.With["workload_identity_provider"]; ok {
			auth = step.With
		}
	}
	if checkoutRef != "${{ github.event.workflow_run.head_sha }}" {
		t.Errorf("staging checks out %q, want the verified push", checkoutRef)
	}
	if auth["workload_identity_provider"] != "projects/730635653216/locations/global/workloadIdentityPools/github-actions/providers/github" ||
		auth["service_account"] != "acuity-product-cloud-deploy@acuity-health-staging.iam.gserviceaccount.com" {
		t.Errorf("staging must authenticate only to the staging project, got %v", auth)
	}
	for _, required := range []string{
		"--project=acuity-health-staging",
		"_RELEASE_ENVIRONMENT=staging",
		"_PORTAL_API_URL=https://acuity-portal-api-2tl3beo4pq-ue.a.run.app",
		"_REALTIME_URL=https://acuity-realtime-2tl3beo4pq-ue.a.run.app",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("staging release omits %q", required)
		}
	}
	for _, forbidden := range []string{"vars.", "MIDDLEWARE", "cbuqwpsdsq", "acuity-health-prod"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("staging release reads %q, which can resolve to production", forbidden)
		}
	}

	deploy := jobs["deploy"]
	if !strings.Contains(strings.Join(deploy.Needs, ","), "staging") ||
		!strings.Contains(deploy.If, "needs.staging.result == 'success'") {
		t.Errorf("production deploys without staging passing: needs %v, if %q", deploy.Needs, deploy.If)
	}
}
