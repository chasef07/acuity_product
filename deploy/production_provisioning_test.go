package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const (
	provisioningCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	provisioningDigest = "us-central1-docker.pkg.dev/acuity-health-prod/acuity-product/backend@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestProductionProvisioningWorkflowIsManualAndProductionScoped(t *testing.T) {
	directory := provisioningDeployDirectory(t)
	workflow, err := os.ReadFile(filepath.Join(
		directory,
		"..",
		".github",
		"workflows",
		"reconcile-production-provisioning.yml",
	))
	if err != nil {
		t.Fatalf("read provisioning workflow: %v", err)
	}

	for _, expected := range []string{
		"workflow_dispatch:",
		"group: production-${{ github.ref }}",
		"environment: production",
		"id-token: write",
		"Confirmation must be exactly PROVISION.",
		"refs/heads/main",
		"expected_access_grants_created",
		"deploy/reconcile-production-provisioning.sh",
	} {
		if !strings.Contains(string(workflow), expected) {
			t.Errorf("provisioning workflow omits %q", expected)
		}
	}
}

func TestProductionProvisioningUsesOneExecutionOverrideAndVerifiesReceipt(t *testing.T) {
	output, commands, err := runProductionProvisioning(t, "1", provisioningDigest)
	if err != nil {
		t.Fatalf("reconcile production provisioning: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "acuity-migrate-test123 created 1 Access Grants") {
		t.Fatalf("provisioning output = %q", output)
	}
	if strings.Contains(commands, "run\tjobs\tupdate") {
		t.Fatalf("provisioning mutated the shared job:\n%s", commands)
	}
	for _, expected := range []string{
		"run\tjobs\tdescribe\tacuity-migrate",
		"artifacts\tdocker\timages\tdescribe\tus-central1-docker.pkg.dev/acuity-health-prod/acuity-product/backend:" + provisioningCommit,
		"run\tjobs\texecute\tacuity-migrate",
		"--tasks\t1",
		"--update-env-vars\tPROVISIONING_INPUT=/etc/acuity/production-provisioning.json,PROVISIONING_OUTPUT=/tmp/production-provisioning-output.json",
		"run\tjobs\texecutions\tdescribe\tacuity-migrate-test123",
		"logging\tread",
	} {
		if !strings.Contains(commands, expected) {
			t.Errorf("provisioning commands omit %q:\n%s", expected, commands)
		}
	}
}

func TestProductionProvisioningWaitsForDelayedReceipt(t *testing.T) {
	output, commands, err := runProductionProvisioningWithEmptyLogReads(
		t,
		"1",
		provisioningDigest,
		1,
	)
	if err != nil {
		t.Fatalf("reconcile production provisioning: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "acuity-migrate-test123 created 1 Access Grants") {
		t.Fatalf("provisioning output = %q", output)
	}
	if reads := strings.Count(commands, "logging\tread"); reads != 2 {
		t.Fatalf("logging reads = %d, want 2:\n%s", reads, commands)
	}
}

func TestProductionProvisioningRejectsUnexpectedReceipt(t *testing.T) {
	output, commands, err := runProductionProvisioning(t, "0", provisioningDigest)
	if err == nil {
		t.Fatal("provisioning accepted an unexpected Access Grant count")
	}
	if !strings.Contains(string(output), "Expected 0 new Access Grants") {
		t.Fatalf("receipt mismatch output = %q", output)
	}
	if strings.Contains(commands, "run\tjobs\tupdate") {
		t.Fatalf("receipt mismatch mutated the shared job:\n%s", commands)
	}
}

func TestProductionProvisioningRejectsAStaleJobImageBeforeExecution(t *testing.T) {
	staleDigest := strings.Replace(provisioningDigest, "bbbb", "cccc", 1)
	output, commands, err := runProductionProvisioning(t, "1", staleDigest)
	if err == nil {
		t.Fatal("provisioning accepted a job image from another commit")
	}
	if !strings.Contains(string(output), "not the image built from the selected main commit") {
		t.Fatalf("stale-image output = %q", output)
	}
	if strings.Contains(commands, "run\tjobs\texecute") {
		t.Fatalf("stale image reached job execution:\n%s", commands)
	}
}

func runProductionProvisioning(
	t *testing.T,
	expectedCount string,
	jobImage string,
) ([]byte, string, error) {
	t.Helper()
	return runProductionProvisioningWithEmptyLogReads(t, expectedCount, jobImage, 0)
}

func runProductionProvisioningWithEmptyLogReads(
	t *testing.T,
	expectedCount string,
	jobImage string,
	emptyLogReads int,
) ([]byte, string, error) {
	t.Helper()
	directory := provisioningDeployDirectory(t)
	fakeDirectory := t.TempDir()
	capture := filepath.Join(fakeDirectory, "gcloud-commands")
	logReadCount := filepath.Join(fakeDirectory, "gcloud-log-read-count")
	gcloud := filepath.Join(fakeDirectory, "gcloud")
	fake := `#!/usr/bin/env bash
set -Eeuo pipefail
{
  first=true
  for argument in "$@"; do
    if [[ "$first" == true ]]; then
      first=false
    else
      printf '\t'
    fi
    printf '%s' "$argument"
  done
  printf '\n'
} >>"$GCLOUD_CAPTURE"
case "$*" in
  "run jobs describe acuity-migrate "*)
    printf '{"spec":{"template":{"spec":{"template":{"spec":{"containers":[{"image":"%s","env":[]}]}}}}}}\n' "$JOB_IMAGE"
    ;;
  "artifacts docker images describe "*)
    printf '%s\n' "$TAG_IMAGE"
    ;;
  "run jobs execute acuity-migrate "*)
    printf 'acuity-migrate-test123\n'
    ;;
  "run jobs executions describe acuity-migrate-test123 "*)
    printf '{"status":{"conditions":[{"type":"Completed","status":"True"}],"succeededCount":1}}\n'
    ;;
  "logging read "*)
    read_count=0
    if [[ -f "$GCLOUD_LOG_READ_COUNT" ]]; then
      IFS= read -r read_count <"$GCLOUD_LOG_READ_COUNT"
    fi
    read_count=$((read_count + 1))
    printf '%s\n' "$read_count" >"$GCLOUD_LOG_READ_COUNT"
    if (( read_count <= EMPTY_LOG_READS )); then
      printf '[]\n'
    else
      printf '[{"jsonPayload":{"msg":"migrations_applied","provisioning":true,"access_grant_count":1}}]\n'
    fi
    ;;
  *)
    printf 'unexpected gcloud command: %s\n' "$*" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(gcloud, []byte(fake), 0o755); err != nil {
		t.Fatalf("write fake gcloud: %v", err)
	}
	sleep := filepath.Join(fakeDirectory, "sleep")
	if err := os.WriteFile(sleep, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake sleep: %v", err)
	}

	command := exec.Command("bash", filepath.Join(directory, "reconcile-production-provisioning.sh"))
	command.Env = append(os.Environ(),
		"EMPTY_LOG_READS="+strconv.Itoa(emptyLogReads),
		"PATH="+fakeDirectory+":"+os.Getenv("PATH"),
		"EXPECTED_ACCESS_GRANTS_CREATED="+expectedCount,
		"GCLOUD_CAPTURE="+capture,
		"GCLOUD_LOG_READ_COUNT="+logReadCount,
		"GCP_PROJECT_ID=acuity-health-prod",
		"GCP_REGION=us-east1",
		"GITHUB_SHA="+provisioningCommit,
		"JOB_IMAGE="+jobImage,
		"PROVISION_CONFIRMATION=PROVISION",
		"TAG_IMAGE="+provisioningDigest,
	)
	output, runErr := command.CombinedOutput()
	captured, readErr := os.ReadFile(capture)
	if readErr != nil {
		t.Fatalf("read gcloud capture: %v", readErr)
	}
	return output, string(captured), runErr
}

func provisioningDeployDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate production provisioning test")
	}
	return filepath.Dir(filename)
}
