package deploy_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestProductionReleaseMigratesStagesAndPromotesOneImmutableBuild(t *testing.T) {
	directory := releaseDeployDirectory(t)
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	command := exec.Command(
		"bash",
		filepath.Join(directory, "deploy-production-release.sh"),
	)
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
	}, releaseEnvironment()...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("release current main: %v\n%s", err, output)
	}

	commands := capturedGcloudCommands(t, gcloudCapture)
	migrationUpdate := commandIndex(commands, "run\tjobs\tupdate\tacuity-migrate")
	migrationExecute := commandIndex(commands, "run\tjobs\texecute\tacuity-migrate")
	firstServiceDeploy := commandIndex(commands, "run\tdeploy\tacuity-portal-api")
	if migrationUpdate < 0 ||
		migrationExecute <= migrationUpdate ||
		firstServiceDeploy <= migrationExecute {
		t.Fatalf("migration was not completed before service staging:\n%s", strings.Join(commands, "\n"))
	}
	assertCapturedCommand(t, commands, "run\tjobs\tupdate\tacuity-migrate",
		"--update-env-vars\tDATABASE_POOL_MAX=1,DATABASE_ACQUIRE_TIMEOUT_MS=5000,PROVISIONING_INPUT=/etc/acuity/production-provisioning.json,PROVISIONING_OUTPUT=/tmp/production-provisioning-output.json",
	)
	if strings.Contains(commands[migrationUpdate], "--remove-env-vars") {
		t.Fatalf("release must apply production provisioning:\n%s", commands[migrationUpdate])
	}

	for _, service := range []string{
		"acuity-portal-api",
		"acuity-provider-ingress",
		"acuity-realtime",
	} {
		assertCapturedCommand(t, commands, "run\tdeploy\t"+service,
			"--image\tus-central1-docker.pkg.dev/acuity-test/acuity-product/backend@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"--no-traffic",
			"--revision-suffix\trelease-1234",
			"--startup-probe",
			"httpGet.path=/health/live",
			"--readiness-probe",
			"httpGet.path=/health/ready",
		)
		assertCapturedCommand(t, commands, "run\tservices\tupdate-traffic\t"+service,
			"--to-revisions\t"+service+"-release-1234=100",
		)
	}
	assertCapturedCommand(t, commands, "run\tdeploy\tacuity-portal-api",
		"--cpu\t1",
		"--memory\t512Mi",
		"--concurrency\t8",
		"--min\t1",
		"--max\t3",
		"--update-env-vars\tDATABASE_POOL_MAX=4,DATABASE_ACQUIRE_TIMEOUT_MS=1500,HUMAN_CALLING_RING_WINDOW_SECONDS=20",
	)
	assertCapturedCommand(t, commands, "run\tdeploy\tacuity-provider-ingress",
		"--cpu\t1",
		"--memory\t512Mi",
		"--concurrency\t20",
		"--min\t1",
		"--max\t2",
		"--update-env-vars\tDATABASE_POOL_MAX=1,DATABASE_ACQUIRE_TIMEOUT_MS=1500",
	)
	assertCapturedCommand(t, commands, "run\tdeploy\tacuity-realtime",
		"--cpu\t1",
		"--memory\t512Mi",
		"--concurrency\t50",
		"--min\t1",
		"--max\t2",
		"--timeout\t300",
		"--update-env-vars\tDATABASE_POOL_MAX=1,DATABASE_ACQUIRE_TIMEOUT_MS=1500,REALTIME_STREAM_SECONDS=270,REALTIME_STREAM_JITTER_SECONDS=30",
	)

	assertCapturedCommand(t, commands, "run\tworker-pools\tdeploy\tacuity-worker",
		"--image\tus-central1-docker.pkg.dev/acuity-test/acuity-product/backend@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"--no-promote",
		"--revision-suffix\trelease-1234",
		"--cpu\t1",
		"--memory\t512Mi",
		"--instances\t1",
		"--update-env-vars\tDATABASE_POOL_MAX=2,DATABASE_ACQUIRE_TIMEOUT_MS=1500,HUMAN_CALLING_RING_WINDOW_SECONDS=20",
	)
	assertCapturedCommand(t, commands, "run\tworker-pools\tupdate-instance-split\tacuity-worker",
		"--to-revisions\tacuity-worker-release-1234=100",
	)
	assertCapturedCommand(t, commands, "run\tdeploy\tacuity-web",
		"--image\tus-central1-docker.pkg.dev/acuity-test/acuity-product/web@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"--no-traffic",
		"--revision-suffix\trelease-1234",
		"--cpu\t1",
		"--memory\t512Mi",
		"--concurrency\t40",
		"--min\t1",
		"--max\t2",
		"--update-env-vars\tAUTH_DB_POOL_MAX=1,AUTH_DB_ACQUIRE_TIMEOUT_MS=1500",
	)
	assertCapturedCommand(t, commands, "run\tservices\tupdate-traffic\tacuity-web",
		"--to-revisions\tacuity-web-release-1234=100",
	)
	webPromotion := commandIndex(commands, "run\tservices\tupdate-traffic\tacuity-web")
	runtimeVerification := -1
	for index, captured := range commands {
		if strings.HasPrefix(captured, "run\tservices\tdescribe\tacuity-web") &&
			strings.Contains(captured, "--format\tjson") {
			runtimeVerification = index
			break
		}
	}
	if runtimeVerification <= webPromotion {
		t.Fatalf("runtime contract was not verified after rollout:\n%s", strings.Join(commands, "\n"))
	}

	curlCalls, err := os.ReadFile(curlCapture)
	if err != nil {
		t.Fatalf("read curl capture: %v", err)
	}
	for _, expected := range []string{
		"https://portal.example/health/ready",
		"https://ingress.example/health/ready",
		"https://realtime.example/health/ready",
		"https://portal-web.example/sign-in",
		"https://portal-web.example/api/auth/get-session",
	} {
		if !strings.Contains(string(curlCalls), expected) {
			t.Errorf("release smoke omits %s:\n%s", expected, curlCalls)
		}
	}
}

func TestProductionReleaseFailsClosedOnLiveRuntimeContractDrift(t *testing.T) {
	directory := releaseDeployDirectory(t)
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	environment := append(
		releaseEnvironment(),
		"GCLOUD_RUNTIME_DRIFT_SERVICE=acuity-portal-api",
	)
	command := exec.Command("bash", filepath.Join(directory, "deploy-production-release.sh"))
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
	}, environment...)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("release accepted live runtime drift:\n%s", output)
	}
	if !strings.Contains(
		string(output),
		"acuity-portal-api runtime contract drift: maximumInstances expected 3, got 20",
	) {
		t.Fatalf("runtime drift error = %q", output)
	}
	commands := capturedGcloudCommands(t, gcloudCapture)
	for _, service := range []string{"acuity-web", "acuity-realtime", "acuity-provider-ingress", "acuity-portal-api"} {
		promotion := commandIndex(commands, "run\tservices\tupdate-traffic\t"+service)
		rollback := commandIndex(commands[promotion+1:], "run\tservices\tupdate-traffic\t"+service)
		if promotion < 0 || rollback < 0 || !strings.Contains(commands[promotion+1+rollback], "--to-revisions\t"+service+"-old=100") {
			t.Errorf("runtime drift did not restore %s after promotion:\n%s", service, strings.Join(commands, "\n"))
		}
	}
	if !strings.Contains(commands[len(commands)-1], "--to-revisions\tacuity-worker-old=100") {
		t.Errorf("runtime drift did not restore the worker allocation:\n%s", strings.Join(commands, "\n"))
	}
}

func TestProductionReleaseRejectsInsufficientDatabaseCapacityBeforeCloudMutation(t *testing.T) {
	directory := releaseDeployDirectory(t)
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	command := exec.Command(
		"bash",
		filepath.Join(directory, "deploy-production-release.sh"),
	)
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
	}, environmentWithValue(releaseEnvironment(), "USABLE_DATABASE_CONNECTIONS", "35")...)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("release unexpectedly accepted insufficient database capacity:\n%s", output)
	}
	if !strings.Contains(string(output), "production requires at least 36 usable database connections; measured 35") {
		t.Fatalf("release returned the wrong capacity error:\n%s", output)
	}
	captured, err := os.ReadFile(gcloudCapture)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read captured gcloud commands: %v", err)
	}
	if len(captured) != 0 {
		t.Fatalf("release mutated cloud state before rejecting capacity:\n%s", captured)
	}
}

func TestProductionReleaseLoadsWorkerCapacityFromRuntimeContract(t *testing.T) {
	directory := releaseDeployDirectory(t)
	temporaryDirectory := t.TempDir()
	for _, name := range []string{
		"deploy-production-release.sh",
		"production-runtime-contract.json",
		"verify-production-runtime.py",
	} {
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(temporaryDirectory, name), raw, mode); err != nil {
			t.Fatalf("copy %s: %v", name, err)
		}
	}
	contractPath := filepath.Join(temporaryDirectory, "production-runtime-contract.json")
	raw, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	var contract map[string]any
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	contract["requiredDatabaseConnections"] = float64(30)
	runtimes, ok := contract["runtimes"].([]any)
	if !ok {
		t.Fatal("runtime contract omits runtimes")
	}
	for _, value := range runtimes {
		runtime, ok := value.(map[string]any)
		if ok && runtime["name"] == "worker" {
			runtime["poolMaximum"] = float64(3)
		}
	}
	raw, err = json.MarshalIndent(contract, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contractPath, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	command := exec.Command("bash", filepath.Join(temporaryDirectory, "deploy-production-release.sh"))
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
		"GCLOUD_WORKER_POOL=3",
	}, environmentWithValue(releaseEnvironment(), "USABLE_DATABASE_CONNECTIONS", "30")...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("release with varied worker contract: %v\n%s", err, output)
	}
	assertCapturedCommand(
		t,
		capturedGcloudCommands(t, gcloudCapture),
		"run\tworker-pools\tdeploy\tacuity-worker",
		"--update-env-vars\tDATABASE_POOL_MAX=3,DATABASE_ACQUIRE_TIMEOUT_MS=1500,HUMAN_CALLING_RING_WINDOW_SECONDS=20",
	)
}

func TestProductionReleaseRejectsMutableImageTagBeforeCloudMutation(t *testing.T) {
	directory := releaseDeployDirectory(t)
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	environment := replaceEnvironment(
		releaseEnvironment(),
		"IMAGE_TAG",
		"latest",
	)
	command := exec.Command(
		"bash",
		filepath.Join(directory, "deploy-production-release.sh"),
	)
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
	}, environment...)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("release accepted a mutable image tag")
	}
	if !strings.Contains(string(output), "IMAGE_TAG must be a full 40-character Git commit SHA") {
		t.Fatalf("mutable-tag error = %q", output)
	}
	if _, err := os.Stat(gcloudCapture); !os.IsNotExist(err) {
		t.Fatal("mutable image tag reached gcloud")
	}
}

func TestProductionReleaseRejectsMissingPlaybackSigningKeyBeforeCloudMutation(t *testing.T) {
	directory := releaseDeployDirectory(t)
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	command := exec.Command(
		"bash",
		filepath.Join(directory, "deploy-production-release.sh"),
	)
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
		"GCLOUD_MISSING_PLAYBACK_SIGNING_KEY=true",
	}, releaseEnvironment()...)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("release accepted a portal service without its playback signing key")
	}
	if !strings.Contains(
		string(output),
		"acuity-portal-api must configure HUMAN_CALLING_PLAYBACK_SIGNING_KEY",
	) {
		t.Fatalf("missing-secret error = %q", output)
	}
	commands := capturedGcloudCommands(t, gcloudCapture)
	if commandIndex(commands, "run\tjobs\tupdate\tacuity-migrate") >= 0 {
		t.Fatal("missing playback signing key reached migration")
	}
}

func TestProductionReleaseRestoresStagedServiceTrafficWhenDeployFails(t *testing.T) {
	directory := releaseDeployDirectory(t)
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	command := exec.Command(
		"bash",
		filepath.Join(directory, "deploy-production-release.sh"),
	)
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
		"GCLOUD_FAIL_DEPLOY_SERVICE=acuity-portal-api",
	}, releaseEnvironment()...)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("release ignored a failed portal deployment")
	}
	commands := capturedGcloudCommands(t, gcloudCapture)
	assertCapturedCommand(t, commands, "run\tservices\tupdate-traffic\tacuity-portal-api",
		"--to-revisions\tacuity-portal-api-old=100",
	)
	if commandIndex(commands, "run\tdeploy\tacuity-provider-ingress") >= 0 {
		t.Fatalf("release continued after portal failure:\n%s\n%s", strings.Join(commands, "\n"), output)
	}
}

func TestProductionReleaseRestoresTrafficWhenWorkerIsNotReady(t *testing.T) {
	gcloudCapture, output, err := runRelease(t, "GCLOUD_WORKER_READY=False")
	if err == nil {
		t.Fatalf("release accepted a worker that did not become ready:\n%s", output)
	}
	if !strings.Contains(string(output), "did not become ready") {
		t.Fatalf("worker readiness error = %q", output)
	}
	commands := capturedGcloudCommands(t, gcloudCapture)
	for _, service := range []string{"acuity-realtime", "acuity-provider-ingress", "acuity-portal-api"} {
		assertCapturedCommand(t, commands, "run\tservices\tupdate-traffic\t"+service,
			"--to-revisions\t"+service+"-old=100",
		)
	}
	assertCapturedCommand(t, commands, "run\tworker-pools\tupdate-instance-split\tacuity-worker",
		"--to-revisions\tacuity-worker-old=100",
	)
	if commandIndex(commands, "run\tdeploy\tacuity-web") >= 0 {
		t.Fatalf("release deployed web after the worker failed:\n%s", strings.Join(commands, "\n"))
	}
}

func TestProductionReleaseNeverExitsPastItsRollback(t *testing.T) {
	script, err := os.ReadFile(filepath.Join(releaseDeployDirectory(t), "deploy-production-release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	_, armed, found := strings.Cut(string(script), "trap rollback ERR\n")
	armed, _, disarmed := strings.Cut(armed, "trap - ERR\n")
	if !found || !disarmed {
		t.Fatal("release script does not arm and then disarm its rollback")
	}
	exitCommand := regexp.MustCompile(`(^|[\s;&|(])exit(\s|;|$)`)
	for _, line := range strings.Split(armed, "\n") {
		if exitCommand.MatchString(line) {
			t.Errorf("exit skips rollback while it is armed: %s", line)
		}
	}
}

func TestProductionReleaseRepairsStaleDesiredTrafficBeforeMigration(t *testing.T) {
	directory := releaseDeployDirectory(t)
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	command := exec.Command(
		"bash",
		filepath.Join(directory, "deploy-production-release.sh"),
	)
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
		"GCLOUD_STALE_TRAFFIC_SERVICE=acuity-portal-api",
	}, releaseEnvironment()...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("repair stale desired traffic: %v\n%s", err, output)
	}
	commands := capturedGcloudCommands(t, gcloudCapture)
	repair := commandIndex(commands, "run\tservices\tupdate-traffic\tacuity-portal-api")
	migration := commandIndex(commands, "run\tjobs\tupdate\tacuity-migrate")
	if repair < 0 || repair >= migration {
		t.Fatalf("stale desired traffic was not repaired before migration:\n%s", strings.Join(commands, "\n"))
	}
	assertCapturedCommand(t, commands, "run\tservices\tupdate-traffic\tacuity-portal-api",
		"--to-revisions\tacuity-portal-api-old=100",
	)
}

func TestProductionDeployRequiresExactReleaseVerification(t *testing.T) {
	root := filepath.Dir(releaseDeployDirectory(t))
	ciWorkflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	ciContent := string(ciWorkflow)
	for _, required := range []string{
		"uses: ./.github/workflows/verify.yml",
	} {
		if !strings.Contains(ciContent, required) {
			t.Errorf("CI workflow omits %q", required)
		}
	}
	for _, forbidden := range []string{
		"release-please:",
		"deploy:",
	} {
		if strings.Contains(ciContent, forbidden) {
			t.Errorf("superseding CI workflow still owns %q", forbidden)
		}
	}

	verificationWorkflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "verify.yml"))
	if err != nil {
		t.Fatalf("read reusable verification workflow: %v", err)
	}
	verificationContent := string(verificationWorkflow)
	for _, required := range []string{
		"workflow_call:",
		"release_sha:",
		"ref: ${{ inputs.release_sha }}",
		"backend:",
		"run: go test ./backend/... ./deploy -count=1",
		"web:",
		"contracts:",
		"run: bash ./scripts/test-release-container.sh",
		"browser:",
		"run: pnpm playwright install --with-deps chromium\n        timeout-minutes: 10",
	} {
		if !strings.Contains(verificationContent, required) {
			t.Errorf("reusable verification workflow omits %q", required)
		}
	}

	for _, forbidden := range []string{"github.event_name", "backend-exact:"} {
		if strings.Contains(verificationContent, forbidden) {
			t.Errorf("verification must not select a partial suite by event: %q", forbidden)
		}
	}

	releaseWorkflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	releaseContent := string(releaseWorkflow)
	for _, required := range []string{
		"workflow_run:",
		"workflows: [\"CI\"]",
		"types: [completed]",
		"github.event.workflow_run.conclusion == 'success'",
		"github.event.workflow_run.event == 'push'",
		"github.event.workflow_run.head_branch == 'main'",
		"release-please:",
		"googleapis/release-please-action@",
		"release_created: ${{ steps.release.outputs.release_created }}",
		"release_sha: ${{ steps.release.outputs.sha }}",
		"verify-release:",
		"uses: ./.github/workflows/verify.yml",
		"release_sha: ${{ needs.release-please.outputs.release_sha }}",
		"if: needs.release-please.outputs.release_created == 'true' && needs.release-please.outputs.release_sha != github.event.workflow_run.head_sha",
		"deploy:",
		"needs: [release-please, verify-release]",
		"needs.verify-release.result == 'success' || (needs.verify-release.result == 'skipped' && needs.release-please.outputs.release_sha == github.event.workflow_run.head_sha)",
		"ref: ${{ needs.release-please.outputs.release_sha }}",
		"id-token: write",
		"google-github-actions/auth",
		"gcloud builds submit",
		"cloudbuild.release.yaml",
		"RELEASE_SHA: ${{ needs.release-please.outputs.release_sha }}",
		"_IMAGE_TAG=${RELEASE_SHA}",
		"USABLE_DATABASE_CONNECTIONS: ${{ vars.USABLE_DATABASE_CONNECTIONS }}",
		"_USABLE_DATABASE_CONNECTIONS=${USABLE_DATABASE_CONNECTIONS}",
		"_MIDDLEWARE_BASE_URL=${MIDDLEWARE_BASE_URL}",
		"_MIDDLEWARE_API_SECRET_SECRET=${MIDDLEWARE_API_SECRET_SECRET}",
		"url: https://acuity-web-cbuqwpsdsq-ue.a.run.app",
	} {
		if !strings.Contains(releaseContent, required) {
			t.Errorf("non-superseding release workflow omits %q", required)
		}
	}
	if strings.Contains(releaseContent, "\nconcurrency:") {
		t.Error("release workflow must not use GitHub concurrency, which supersedes pending runs")
	}
}

func commandIndex(commands []string, prefix string) int {
	for index, command := range commands {
		if strings.HasPrefix(command, prefix) {
			return index
		}
	}
	return -1
}

func runRelease(t *testing.T, extra ...string) (string, []byte, error) {
	t.Helper()
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	command := exec.Command("bash", filepath.Join(releaseDeployDirectory(t), "deploy-production-release.sh"))
	command.Env = append(append([]string{"PATH=" + path, "GCLOUD_CAPTURE=" + gcloudCapture, "CURL_CAPTURE=" + curlCapture}, releaseEnvironment()...), extra...)
	output, err := command.CombinedOutput()
	return gcloudCapture, output, err
}

func installReleaseFakes(t *testing.T) (string, string, string) {
	t.Helper()
	directory := t.TempDir()
	gcloudPath := filepath.Join(directory, "gcloud")
	curlPath := filepath.Join(directory, "curl")
	gcloudCapture := filepath.Join(directory, "gcloud.tsv")
	curlCapture := filepath.Join(directory, "curl.tsv")
	gcloud := `#!/bin/sh
set -eu
separator=
for argument do
  printf '%s%s' "$separator" "$argument" >>"$GCLOUD_CAPTURE"
  separator='	'
done
printf '\n' >>"$GCLOUD_CAPTURE"

if [ -n "${GCLOUD_FAIL_DEPLOY_SERVICE:-}" ] &&
  [ "$1" = run ] &&
  [ "$2" = deploy ] &&
  [ "$3" = "$GCLOUD_FAIL_DEPLOY_SERVICE" ]; then
  exit 1
fi

case "$*" in
  "artifacts docker images describe "*"backend:"*)
    printf '%s\n' "us-central1-docker.pkg.dev/acuity-test/acuity-product/backend@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    ;;
  "artifacts docker images describe "*"web:"*)
    printf '%s\n' "us-central1-docker.pkg.dev/acuity-test/acuity-product/web@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
    ;;
  "run services describe "*"status.latestReadyRevisionName"*)
    printf '%s-old\n' "$4"
    ;;
  "run services describe "*"spec.traffic[].revisionName"*)
    if [ "${GCLOUD_STALE_TRAFFIC_SERVICE:-}" = "$4" ]; then
      printf '%s\n' "$4-failed"
    else
      printf '%s-old\n' "$4"
    fi
    ;;
  "run services describe "*"spec.traffic[].percent"*)
    printf '%s\n' "100"
    ;;
  "run services describe acuity-portal-api "*"spec.template.spec.containers[0].env[].name"*)
    if [ "${GCLOUD_MISSING_PLAYBACK_SIGNING_KEY:-}" != true ]; then
      printf '%s\n' "DATABASE_URL;HUMAN_CALLING_PLAYBACK_SIGNING_KEY"
    fi
    ;;
  "run services describe acuity-web "*"spec.template.spec.containers[0].env[].name"*)
    printf '%s\n' "GOOGLE_CLIENT_ID;GOOGLE_CLIENT_SECRET"
    ;;
  "run services describe acuity-portal-api "*"status.url"*)
    printf '%s\n' "https://portal.example"
    ;;
  "run services describe acuity-provider-ingress "*"status.url"*)
    printf '%s\n' "https://ingress.example"
    ;;
  "run services describe acuity-realtime "*"status.url"*)
    printf '%s\n' "https://realtime.example"
    ;;
  "run services describe acuity-web "*"status.url"*)
    printf '%s\n' "https://portal-web.example"
    ;;
  "run worker-pools describe "*"status.instanceSplits.revisionName"*)
    if grep -q "acuity-worker-release-1234=100" "$GCLOUD_CAPTURE"; then
      printf '%s\n' "acuity-worker-release-1234"
    else
      printf '%s\n' "acuity-worker-old"
    fi
    ;;
  "run revisions describe "*"spec.containers[0].image"*)
    case "$4" in
      acuity-web-*)
        printf '%s\n' "us-central1-docker.pkg.dev/acuity-test/acuity-product/web@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
        ;;
      *)
        printf '%s\n' "us-central1-docker.pkg.dev/acuity-test/acuity-product/backend@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
        ;;
    esac
    ;;
  "run revisions describe "*"status.conditions"*)
    printf '%s\n' "True"
    ;;
  "run worker-pools revisions describe "*"spec.containers[0].image"*)
    printf '%s\n' "us-central1-docker.pkg.dev/acuity-test/acuity-product/backend@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    ;;
  "run worker-pools describe "*"status.conditions"*)
    printf '%s\n' "${GCLOUD_WORKER_READY:-True}"
    ;;
  "run services describe "*"--format json"*)
    service="$4"
    case "$service" in
      acuity-web)
        concurrency=40; minimum=1; maximum=2; pool_name=AUTH_DB_POOL_MAX; pool=1 ;;
      acuity-portal-api)
        concurrency=8; minimum=1; maximum=3; pool_name=DATABASE_POOL_MAX; pool=4 ;;
      acuity-provider-ingress)
        concurrency=20; minimum=1; maximum=2; pool_name=DATABASE_POOL_MAX; pool=1 ;;
      acuity-realtime)
        concurrency=50; minimum=1; maximum=2; pool_name=DATABASE_POOL_MAX; pool=1 ;;
    esac
    if [ "${GCLOUD_RUNTIME_DRIFT_SERVICE:-}" = "$service" ]; then
      maximum=20
    fi
    printf '{"metadata":{"name":"%s","annotations":{"run.googleapis.com/minScale":"%s","run.googleapis.com/maxScale":"%s"}},"spec":{"template":{"metadata":{"annotations":{"autoscaling.knative.dev/maxScale":"20"}},"spec":{"containerConcurrency":%s,"containers":[{"env":[{"name":"%s","value":"%s"}]}]}}}}\n' \
      "$service" "$minimum" "$maximum" "$concurrency" "$pool_name" "$pool"
    ;;
  "run worker-pools describe acuity-worker "*"--format json"*)
    printf '{"metadata":{"name":"acuity-worker"},"spec":{"template":{"scaling":{"manualInstanceCount":"1"},"spec":{"containerConcurrency":0,"containers":[{"env":[{"name":"DATABASE_POOL_MAX","value":"%s"}]}]}}}}\n' "${GCLOUD_WORKER_POOL:-2}"
    ;;
esac
`
	curl := `#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$CURL_CAPTURE"
`
	if err := os.WriteFile(gcloudPath, []byte(gcloud), 0o755); err != nil {
		t.Fatalf("write fake gcloud: %v", err)
	}
	if err := os.WriteFile(curlPath, []byte(curl), 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
	return directory + ":" + os.Getenv("PATH"), gcloudCapture, curlCapture
}

func releaseEnvironment() []string {
	return []string{
		"PROJECT_ID=acuity-test",
		"REGION=us-central1",
		"REPOSITORY=acuity-product",
		"BACKEND_IMAGE=backend",
		"WEB_IMAGE=web",
		"IMAGE_TAG=0123456789abcdef0123456789abcdef01234567",
		"DEPLOYMENT_ID=release-1234",
		"USABLE_DATABASE_CONNECTIONS=36",
	}
}

func environmentWithValue(environment []string, name string, value string) []string {
	prefix := name + "="
	result := append([]string(nil), environment...)
	for index, assignment := range result {
		if strings.HasPrefix(assignment, prefix) {
			result[index] = prefix + value
			return result
		}
	}
	return append(result, prefix+value)
}

func releaseDeployDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate release deploy directory")
	}
	return filepath.Dir(filename)
}
