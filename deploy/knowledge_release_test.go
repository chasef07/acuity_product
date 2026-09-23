package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oasdiff/yaml3"
)

func TestKnowledgePublicationIsIndependentOfRelease(t *testing.T) {
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
	for name, value := range jobs {
		job := value.(map[string]any)
		if name == "knowledge" || job["uses"] == "./.github/workflows/knowledge.yml" {
			t.Fatal("knowledge publication must not determine application release status")
		}
	}
	knowledge := read("knowledge.yml")
	publish := knowledge["jobs"].(map[string]any)["publish"].(map[string]any)
	if publish["if"] != "github.ref == 'refs/heads/main' && github.event_name == 'workflow_dispatch'" {
		t.Fatal("knowledge publication must require manual dispatch on main")
	}
	steps := publish["steps"].([]any)
	checkout := steps[0].(map[string]any)
	if checkout["with"].(map[string]any)["ref"] != "${{ github.sha }}" {
		t.Fatal("publisher must check out the selected commit")
	}
	foundCredentials := false
	for _, value := range steps {
		step := value.(map[string]any)
		if step["name"] == "Publish and verify office knowledge" {
			env := step["env"].(map[string]any)
			foundCredentials = env["KNOWLEDGE_SERVICE_TOKEN_SECRETS"] == "${{ vars.KNOWLEDGE_SERVICE_TOKEN_SECRETS }}"
		}
	}
	if !foundCredentials {
		t.Fatal("publisher must receive practice-scoped verification secret mappings")
	}
}

func TestKnowledgePublicationFreshness(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(releaseDeployDirectory(t)), ".github/workflows/knowledge.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow map[string]any
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	steps := workflow["jobs"].(map[string]any)["publish"].(map[string]any)["steps"].([]any)
	var guard string
	for _, value := range steps {
		step := value.(map[string]any)
		script, _ := step["run"].(string)
		if env, ok := step["env"].(map[string]any); ok && env["KNOWLEDGE_COMMIT"] != nil && strings.Contains(script, "git ") {
			guard = script
		}
	}
	if guard == "" {
		t.Fatal("publication freshness guard is missing")
	}
	for _, scenario := range []struct {
		name, changedPath string
		wantSuccess       bool
	}{
		{"current release", "", true},
		{"unrelated merge during deployment", "web/example.txt", true},
		{"newer office knowledge", "knowledge/offices/alpha.yaml", false},
		{"newer retrieval expectations", "knowledge/evals/alpha.json", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			remote, checkout := t.TempDir(), filepath.Join(t.TempDir(), "checkout")
			git := func(directory string, args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = directory
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, output)
				}
				return strings.TrimSpace(string(output))
			}
			write := func(path string) {
				t.Helper()
				path = filepath.Join(remote, path)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("synthetic knowledge\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			git(remote, "init", "-b", "main")
			git(remote, "config", "user.email", "test@example.com")
			git(remote, "config", "user.name", "Synthetic Test")
			write("knowledge/offices/original.yaml")
			git(remote, "add", ".")
			git(remote, "commit", "-m", "Released knowledge")
			release := git(remote, "rev-parse", "HEAD")
			git(remote, "clone", "--depth=1", "file://"+remote, checkout)
			git(checkout, "checkout", "--detach", release)
			if scenario.changedPath != "" {
				write(scenario.changedPath)
				git(remote, "add", ".")
				git(remote, "commit", "-m", "Merge during deployment")
			}
			cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", guard)
			cmd.Dir = checkout
			cmd.Env = append(os.Environ(), "KNOWLEDGE_COMMIT="+release)
			output, err := cmd.CombinedOutput()
			if (err == nil) != scenario.wantSuccess {
				t.Fatalf("publication guard: %v; want success=%t\n%s", err, scenario.wantSuccess, output)
			}
			if got := git(checkout, "rev-parse", "HEAD"); got != release {
				t.Fatalf("guard changed released checkout to %s", got)
			}
		})
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
