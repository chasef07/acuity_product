package deploy_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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
		"--remove-env-vars\tMIGRATE_VOICE_PRACTICE_KEY,MIGRATE_VOICE_LOCATION_KEY,MIGRATE_VOICE_NUMBER,PROVISIONING_INPUT,PROVISIONING_OUTPUT",
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
	for _, destructiveCutover := range []bool{false, true} {
		name := "ordinary rollout"
		if destructiveCutover {
			name = "destructive cutover"
		}
		t.Run(name, func(t *testing.T) {
			directory := releaseDeployDirectory(t)
			path, gcloudCapture, curlCapture := installReleaseFakes(t)
			environment := append(
				releaseEnvironment(),
				"GCLOUD_RUNTIME_DRIFT_SERVICE=acuity-portal-api",
			)
			if destructiveCutover {
				environment = append(
					environment,
					"CALLLEG_DESTRUCTIVE_CUTOVER=true",
					"CALLLEG_CUTOVER_EVIDENCE_VERIFIED=true",
					"CALLLEG_CUTOVER_WINDOW_CONFIRMED=true",
					"CALLLEG_CUTOVER_EVIDENCE_PATH="+filepath.Join(strings.Split(path, ":")[0], "cutover-evidence.json"),
				)
			}
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
		})
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
		"verify-production-runtime.mjs",
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

func TestBackendImageIncludesReviewedProductionProvisioning(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate production release test")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "Dockerfile.backend"))
	if err != nil {
		t.Fatalf("read backend Dockerfile: %v", err)
	}
	if !strings.Contains(
		string(raw),
		"COPY config/production-provisioning.json /etc/acuity/production-provisioning.json",
	) {
		t.Fatal("backend image omits the reviewed production provisioning input")
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

func TestProductionReleasePausesBeforeCallLegCutover(t *testing.T) {
	directory := releaseDeployDirectory(t)
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	environment := replaceEnvironment(
		releaseEnvironment(),
		"CALLLEG_SCHEMA_CUTOVER_COMPLETE",
		"false",
	)
	command := exec.Command("bash", filepath.Join(directory, "deploy-production-release.sh"))
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
	}, environment...)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("release ran before the CallLeg cutover")
	}
	if !strings.Contains(string(output), "Automatic release is paused") {
		t.Fatalf("cutover pause error = %q", output)
	}
	if _, err := os.Stat(gcloudCapture); !os.IsNotExist(err) {
		t.Fatal("paused release reached gcloud")
	}
}

func TestDestructiveCallLegCutoverStopsLegacyRuntimeBeforeMigration(t *testing.T) {
	directory := releaseDeployDirectory(t)
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	environment := append(
		releaseEnvironment(),
		"CALLLEG_DESTRUCTIVE_CUTOVER=true",
		"CALLLEG_CUTOVER_EVIDENCE_VERIFIED=true",
		"CALLLEG_CUTOVER_WINDOW_CONFIRMED=true",
		"CALLLEG_CUTOVER_EVIDENCE_PATH="+filepath.Join(strings.Split(path, ":")[0], "cutover-evidence.json"),
	)
	command := exec.Command("bash", filepath.Join(directory, "deploy-production-release.sh"))
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
	}, environment...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("destructive CallLeg cutover: %v\n%s", err, output)
	}
	commands := capturedGcloudCommands(t, gcloudCapture)
	migration := commandIndex(commands, "run\tjobs\texecute\tacuity-migrate")
	firstReplacement := commandIndex(commands, "run\tdeploy\tacuity-portal-api")
	workerReplacement := commandIndex(commands, "run\tworker-pools\tdeploy\tacuity-worker")
	workerZeroProof := commandIndex(commands, "run\tworker-pools\tupdate\tacuity-worker")
	if migration < 0 || firstReplacement <= migration || workerReplacement <= migration ||
		workerZeroProof < 0 || workerZeroProof >= migration {
		t.Fatalf("replacement runtime crossed the zero-runtime migration gap:\n%s", strings.Join(commands, "\n"))
	}
	for _, service := range []string{
		"acuity-portal-api", "acuity-provider-ingress", "acuity-realtime", "acuity-web",
	} {
		assertCapturedCommand(t, commands, "run\tservices\tupdate\t"+service,
			"--scaling\t0",
		)
	}
	assertCapturedCommand(t, commands, "run\tworker-pools\tupdate\tacuity-worker",
		"--instances\t0",
	)
	assertCapturedCommand(t, commands, "run\tdeploy\tacuity-portal-api",
		"--min\t1",
		"--update-env-vars\tDATABASE_POOL_MAX=4,DATABASE_ACQUIRE_TIMEOUT_MS=1500,HUMAN_CALLING_RING_WINDOW_SECONDS=20,HUMAN_CALLING_HANDOFF_ADMISSION=closed",
	)
	assertCapturedCommand(t, commands, "run\tdeploy\tacuity-provider-ingress",
		"--min\t1",
		"--update-env-vars\tDATABASE_POOL_MAX=1,DATABASE_ACQUIRE_TIMEOUT_MS=1500,HUMAN_CALLING_HANDOFF_ADMISSION=closed",
	)
	assertCapturedCommand(t, commands, "run\tworker-pools\tdeploy\tacuity-worker",
		"--instances\t0",
		"--update-env-vars\tDATABASE_POOL_MAX=2,DATABASE_ACQUIRE_TIMEOUT_MS=1500,HUMAN_CALLING_RING_WINDOW_SECONDS=20,HUMAN_CALLING_HANDOFF_ADMISSION=closed",
	)
	assertCapturedCommand(t, commands, "run\tworker-pools\tupdate\tacuity-worker",
		"--instances\t1",
	)
}

func TestDestructiveCutoverRestoresScalingWhenDisableFails(t *testing.T) {
	directory := releaseDeployDirectory(t)
	path, gcloudCapture, curlCapture := installReleaseFakes(t)
	environment := append(
		releaseEnvironment(),
		"CALLLEG_DESTRUCTIVE_CUTOVER=true",
		"CALLLEG_CUTOVER_EVIDENCE_VERIFIED=true",
		"CALLLEG_CUTOVER_WINDOW_CONFIRMED=true",
		"CALLLEG_CUTOVER_EVIDENCE_PATH="+filepath.Join(strings.Split(path, ":")[0], "cutover-evidence.json"),
		"GCLOUD_FAIL_DISABLE_SERVICE=acuity-provider-ingress",
	)
	command := exec.Command("bash", filepath.Join(directory, "deploy-production-release.sh"))
	command.Env = append([]string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + gcloudCapture,
		"CURL_CAPTURE=" + curlCapture,
	}, environment...)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("destructive cutover ignored disable failure:\n%s", output)
	}
	commands := capturedGcloudCommands(t, gcloudCapture)
	assertCapturedCommand(t, commands, "run\tservices\tupdate\tacuity-portal-api",
		"--scaling\t0",
	)
	portalZeroUpdates := 0
	for _, captured := range commands {
		if strings.HasPrefix(captured, "run\tservices\tupdate\tacuity-portal-api") &&
			strings.Contains(captured, "--scaling\t0") {
			portalZeroUpdates++
		}
	}
	if portalZeroUpdates != 2 {
		t.Fatalf("portal scaling was not restored after partial disable:\n%s",
			strings.Join(commands, "\n"))
	}
	if commandIndex(commands, "run\tjobs\tupdate\tacuity-migrate") >= 0 {
		t.Fatal("failed disable reached migration")
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
		"backend-shard:\n    if: github.event_name == 'pull_request'",
		"database: acuity_calling_test",
		"database: acuity_domain_test",
		"database: acuity_support_test",
		"run: bash ./scripts/run-backend-test-shard.sh ${{ matrix.shard }}",
		"if: matrix.shard == 'support'",
		"backend-exact:\n    if: github.event_name != 'pull_request'",
		"backend:\n    if: always()\n    needs: [backend-shard, backend-exact]",
		"SHARD_RESULT: ${{ needs.backend-shard.result }}",
		"EXACT_RESULT: ${{ needs.backend-exact.result }}",
		"web:",
		"contracts:",
		"browser:",
		"go test -p 1 ./backend/... ./deploy -count=1",
		"run: pnpm playwright install --with-deps chromium\n        timeout-minutes: 10",
	} {
		if !strings.Contains(verificationContent, required) {
			t.Errorf("reusable verification workflow omits %q", required)
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
		"deploy:",
		"if: needs.release-please.outputs.release_created == 'true'",
		"needs: [release-please, verify-release]",
		"ref: ${{ needs.release-please.outputs.release_sha }}",
		"id-token: write",
		"google-github-actions/auth",
		"gcloud builds submit",
		"cloudbuild.release.yaml",
		"RELEASE_SHA: ${{ needs.release-please.outputs.release_sha }}",
		"_IMAGE_TAG=${RELEASE_SHA}",
		"USABLE_DATABASE_CONNECTIONS: ${{ vars.USABLE_DATABASE_CONNECTIONS }}",
		"_USABLE_DATABASE_CONNECTIONS=${USABLE_DATABASE_CONNECTIONS}",
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

func TestPullRequestBackendShardsCoverEveryPackageExactlyOnce(t *testing.T) {
	root := filepath.Dir(releaseDeployDirectory(t))
	shardScript := filepath.Join(root, "scripts", "run-backend-test-shard.sh")

	expectedCommand := exec.Command("go", "list", "./backend/...", "./deploy")
	expectedCommand.Dir = root
	expectedOutput, err := expectedCommand.Output()
	if err != nil {
		t.Fatalf("list complete backend package set: %v", err)
	}
	expected := strings.Fields(string(expectedOutput))
	sort.Strings(expected)

	seen := make(map[string]string, len(expected))
	var actual []string
	for _, shard := range []string{"calling", "domain", "support"} {
		command := exec.Command("bash", shardScript, shard, "--list")
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("list %s backend shard: %v\n%s", shard, err, output)
		}
		for _, packagePath := range strings.Fields(string(output)) {
			if previousShard, duplicated := seen[packagePath]; duplicated {
				t.Fatalf("backend package %q appears in both %s and %s shards", packagePath, previousShard, shard)
			}
			seen[packagePath] = shard
			actual = append(actual, packagePath)
		}
	}
	sort.Strings(actual)

	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("backend shards do not cover the complete package set\nactual:\n%s\nexpected:\n%s", strings.Join(actual, "\n"), strings.Join(expected, "\n"))
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
		"USABLE_DATABASE_CONNECTIONS=${_USABLE_DATABASE_CONNECTIONS}",
		"_USABLE_DATABASE_CONNECTIONS: DO_NOT_DEPLOY",
		"_REGION: us-east1",
		"_PORTAL_API_URL: https://acuity-portal-api-cbuqwpsdsq-ue.a.run.app",
		"_REALTIME_URL: https://acuity-realtime-cbuqwpsdsq-ue.a.run.app",
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
	nodePath := filepath.Join(directory, "node")
	evidencePath := filepath.Join(directory, "cutover-evidence.json")
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
if [ -n "${GCLOUD_FAIL_DISABLE_SERVICE:-}" ] &&
  [ "$1" = run ] &&
  [ "$2" = services ] &&
  [ "$3" = update ] &&
  [ "$4" = "$GCLOUD_FAIL_DISABLE_SERVICE" ] &&
  printf '%s\n' "$*" | grep -q -- '--scaling 0'; then
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
	"run services describe "*"spec.scaling.scalingMode"*)
		printf '%s\n' "MANUAL"
		;;
	"run services describe "*"spec.scaling.manualInstanceCount"*)
    printf '%s\n' "0"
    ;;
	"run services describe "*"status.conditions[0].status"*)
		printf '%s\n' "True"
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
  "run revisions describe "*"HUMAN_CALLING_HANDOFF_ADMISSION"*)
    printf '%s\n' "closed"
    ;;
  "run worker-pools revisions describe "*"spec.containers[0].image"*)
    printf '%s\n' "us-central1-docker.pkg.dev/acuity-test/acuity-product/backend@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    ;;
  "run worker-pools revisions describe "*"HUMAN_CALLING_HANDOFF_ADMISSION"*)
    printf '%s\n' "closed"
    ;;
  "run worker-pools describe "*"status.conditions"*)
    printf '%s\n' "True"
    ;;
  "run worker-pools describe "*"spec.template.scaling.manualInstanceCount"*)
    printf '%s\n' "0"
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
    printf '{"metadata":{"name":"%s"},"spec":{"template":{"metadata":{"annotations":{"autoscaling.knative.dev/minScale":"%s","autoscaling.knative.dev/maxScale":"%s"}},"spec":{"containerConcurrency":%s,"containers":[{"env":[{"name":"%s","value":"%s"}]}]}}}}\n' \
      "$service" "$minimum" "$maximum" "$concurrency" "$pool_name" "$pool"
    ;;
  "run worker-pools describe acuity-worker "*"--format json"*)
    printf '{"metadata":{"name":"acuity-worker","annotations":{"run.googleapis.com/manualInstanceCount":"1"}},"spec":{"template":{"spec":{"containerConcurrency":0,"containers":[{"env":[{"name":"DATABASE_POOL_MAX","value":"%s"}]}]}}}}\n' "${GCLOUD_WORKER_POOL:-2}"
    ;;
esac
`
	curl := `#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$CURL_CAPTURE"
`
	realNode, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("locate node:", err)
	}
	node := "#!/bin/sh\nset -eu\ncase \"$1\" in\n  *verify-production-runtime.mjs) exec \"" + realNode + "\" \"$@\" ;;\nesac\n"
	if err := os.WriteFile(gcloudPath, []byte(gcloud), 0o755); err != nil {
		t.Fatalf("write fake gcloud: %v", err)
	}
	if err := os.WriteFile(curlPath, []byte(curl), 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
	if err := os.WriteFile(nodePath, []byte(node), 0o755); err != nil {
		t.Fatalf("write fake node: %v", err)
	}
	if err := os.WriteFile(evidencePath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write fake cutover evidence: %v", err)
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
		"CALLLEG_SCHEMA_CUTOVER_COMPLETE=true",
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
