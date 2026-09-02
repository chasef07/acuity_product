package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionRuntimeVerifier(t *testing.T) {
	command := exec.Command("python3", "-B", filepath.Join(releaseDeployDirectory(t), "verify-production-runtime_test.py"), "-v")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("runtime verifier: %v\n%s", err, output)
	}
}

func TestProductionReleaseRejectsMissingCommandsBeforeCloudAccess(t *testing.T) {
	for _, missing := range []string{"dirname", "awk", "curl", "gcloud", "python3", "node"} {
		t.Run(missing, func(t *testing.T) {
			path, gcloudCapture, curlCapture := installReleaseFakes(t)
			bin := strings.Split(path, ":")[0]
			for _, name := range []string{"dirname", "awk", "python3"} {
				if name == missing {
					continue
				}
				executable, err := exec.LookPath(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(executable, filepath.Join(bin, name)); err != nil {
					t.Fatal(err)
				}
			}
			if missing == "curl" || missing == "gcloud" {
				if err := os.Remove(filepath.Join(bin, missing)); err != nil {
					t.Fatal(err)
				}
			}
			environment := releaseEnvironment()
			if missing == "node" {
				environment = append(environment, "CALLLEG_DESTRUCTIVE_CUTOVER=true")
			}
			command := exec.Command("bash", filepath.Join(releaseDeployDirectory(t), "deploy-production-release.sh"))
			command.Env = append(environment,
				"PATH="+bin,
				"GCLOUD_CAPTURE="+gcloudCapture,
				"CURL_CAPTURE="+curlCapture,
				"GCLOUD_STALE_TRAFFIC_SERVICE=acuity-portal-api",
			)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "Required deployment command is unavailable: "+missing) {
				t.Fatalf("missing %s: %v\n%s", missing, err, output)
			}
			assertNoReleaseCloudAccess(t, gcloudCapture)
		})
	}
}

func TestProductionReleaseValidatesLocalArtifactsBeforeCloudAccess(t *testing.T) {
	for _, scenario := range []struct {
		name, file, contents string
	}{
		{"missing verifier", "verify-production-runtime.py", ""},
		{"broken verifier", "verify-production-runtime.py", "invalid python syntax!"},
		{"missing contract", "production-runtime-contract.json", ""},
		{"malformed contract", "production-runtime-contract.json", "{"},
		{"missing runtime fields", "production-runtime-contract.json", `{"runtimes":[{"name":"web","kind":"service"}]}`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			directory := t.TempDir()
			for _, name := range []string{"deploy-production-release.sh", "verify-production-runtime.py", "production-runtime-contract.json"} {
				raw, err := os.ReadFile(filepath.Join(releaseDeployDirectory(t), name))
				if err != nil {
					t.Fatal(err)
				}
				if name == scenario.file {
					if scenario.contents == "" {
						continue
					}
					raw = []byte(scenario.contents)
				}
				if err := os.WriteFile(filepath.Join(directory, name), raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path, gcloudCapture, curlCapture := installReleaseFakes(t)
			command := exec.Command("bash", filepath.Join(directory, "deploy-production-release.sh"))
			command.Env = append(releaseEnvironment(),
				"PATH="+path,
				"GCLOUD_CAPTURE="+gcloudCapture,
				"CURL_CAPTURE="+curlCapture,
				"GCLOUD_STALE_TRAFFIC_SERVICE=acuity-portal-api",
			)
			if output, err := command.CombinedOutput(); err == nil {
				t.Fatalf("accepted %s:\n%s", scenario.name, output)
			}
			assertNoReleaseCloudAccess(t, gcloudCapture)
		})
	}
}

func assertNoReleaseCloudAccess(t *testing.T, capture string) {
	t.Helper()
	raw, err := os.ReadFile(capture)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(raw) != 0 {
		t.Fatalf("invalid deployment prerequisites reached gcloud:\n%s", raw)
	}
}
