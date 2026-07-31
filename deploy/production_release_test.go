package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
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
		"--update-env-vars\tDATABASE_POOL_MAX=1,DATABASE_ACQUIRE_TIMEOUT_MS=5000",
	)

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
		"--concurrency\t20",
		"--min\t1",
		"--max\t3",
		"--update-env-vars\tDATABASE_POOL_MAX=1,DATABASE_ACQUIRE_TIMEOUT_MS=1500",
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
		"--update-env-vars\tDATABASE_POOL_MAX=1,DATABASE_ACQUIRE_TIMEOUT_MS=1500",
	)

	assertCapturedCommand(t, commands, "run\tworker-pools\tdeploy\tacuity-worker",
		"--image\tus-central1-docker.pkg.dev/acuity-test/acuity-product/backend@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"--no-promote",
		"--revision-suffix\trelease-1234",
		"--cpu\t1",
		"--memory\t512Mi",
		"--instances\t1",
		"--update-env-vars\tDATABASE_POOL_MAX=1,DATABASE_ACQUIRE_TIMEOUT_MS=1500",
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
		"--min\t0",
		"--max\t2",
		"--update-env-vars\tAUTH_DB_POOL_MAX=1,AUTH_DB_ACQUIRE_TIMEOUT_MS=1500",
	)
	assertCapturedCommand(t, commands, "run\tservices\tupdate-traffic\tacuity-web",
		"--to-revisions\tacuity-web-release-1234=100",
	)

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

func TestMainPushDeployWaitsForAllCIJobs(t *testing.T) {
	root := filepath.Dir(releaseDeployDirectory(t))
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	content := string(workflow)
	for _, required := range []string{
		"deploy:",
		"needs: [backend, web, contracts, browser]",
		"go test -p 1 ./backend/... ./deploy -count=1",
		"github.event_name == 'push'",
		"github.ref == 'refs/heads/main'",
		"id-token: write",
		"google-github-actions/auth",
		"gcloud builds submit",
		"cloudbuild.release.yaml",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("main deployment workflow omits %q", required)
		}
	}
}

func TestCloudBuildReleaseBuildsBothImagesBeforeDeploy(t *testing.T) {
	root := filepath.Dir(releaseDeployDirectory(t))
	config, err := os.ReadFile(filepath.Join(root, "cloudbuild.release.yaml"))
	if err != nil {
		t.Fatalf("read Cloud Build release config: %v", err)
	}
	content := string(config)
	for _, required := range []string{
		"id: build-backend",
		"id: build-web",
		"NEXT_PUBLIC_PORTAL_API_URL=${_PORTAL_API_URL}",
		"NEXT_PUBLIC_REALTIME_URL=${_REALTIME_URL}",
		"id: push-backend",
		"id: push-web",
		"id: deploy",
		"gcr.io/google.com/cloudsdktool/google-cloud-cli:578.0.0-slim",
		"deploy/deploy-production-release.sh",
		"IMAGE_TAG=${_IMAGE_TAG}",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("Cloud Build release config omits %q", required)
		}
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
    printf '%s\n' "acuity-worker-old"
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
    printf '%s\n' "True"
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
	}
}

func releaseDeployDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate release deploy directory")
	}
	return filepath.Dir(filename)
}
