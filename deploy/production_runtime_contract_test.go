package deploy_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type productionContract struct {
	WorkerPoolRevisionOverlap   int               `json:"workerPoolRevisionOverlap"`
	AutoscalerOvershootHeadroom int               `json:"autoscalerOvershootHeadroom"`
	Region                      string            `json:"region"`
	Database                    databaseCapacity  `json:"database"`
	Migration                   migrationCapacity `json:"migration"`
	OperatorHeadroom            int               `json:"operatorHeadroom"`
	RequiredDatabaseConnections int               `json:"requiredDatabaseConnections"`
	Runtimes                    []runtimeCapacity `json:"runtimes"`
}

type runtimeCapacity struct {
	Name                           string `json:"name"`
	Kind                           string `json:"kind"`
	BillingMode                    string `json:"billingMode"`
	VCPUs                          int    `json:"vCPUs"`
	MemoryMiB                      int    `json:"memoryMiB"`
	Concurrency                    int    `json:"concurrency"`
	MinimumInstances               int    `json:"minimumInstances"`
	MaximumInstances               int    `json:"maximumInstances"`
	PoolMaximum                    int    `json:"poolMaximum"`
	DedicatedConnections           int    `json:"dedicatedConnections"`
	AcquisitionTimeoutMilliseconds int    `json:"acquisitionTimeoutMilliseconds"`
	RequestTimeoutSeconds          int    `json:"requestTimeoutSeconds"`
	StreamMaximumSeconds           int    `json:"streamMaximumSeconds"`
	StreamJitterSeconds            int    `json:"streamJitterSeconds"`
}

type migrationCapacity struct {
	Tasks                          int    `json:"tasks"`
	BillingMode                    string `json:"billingMode"`
	VCPUs                          int    `json:"vCPUs"`
	MemoryMiB                      int    `json:"memoryMiB"`
	PoolMaximum                    int    `json:"poolMaximum"`
	AcquisitionTimeoutMilliseconds int    `json:"acquisitionTimeoutMilliseconds"`
	MaximumRetries                 int    `json:"maximumRetries"`
}

type databaseCapacity struct {
	Version                    string `json:"version"`
	Edition                    string `json:"edition"`
	AvailabilityType           string `json:"availabilityType"`
	VCPUs                      int    `json:"vCPUs"`
	MemoryMiB                  int    `json:"memoryMiB"`
	StorageGiB                 int    `json:"storageGiB"`
	StorageType                string `json:"storageType"`
	AutomatedBackups           bool   `json:"automatedBackups"`
	BackupLocation             string `json:"backupLocation"`
	BackupStartTimeUTC         string `json:"backupStartTimeUTC"`
	PointInTimeRecovery        bool   `json:"pointInTimeRecovery"`
	RetainedTransactionLogDays int    `json:"retainedTransactionLogDays"`
	RetainedBackups            int    `json:"retainedBackups"`
	DeletionProtection         bool   `json:"deletionProtection"`
	DataCache                  bool   `json:"dataCache"`
	StorageAutoIncrease        bool   `json:"storageAutoIncrease"`
}

func TestProductionRuntimeContractIsLeanAuditableAndKeepsCallingWarm(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate production runtime contract test")
	}
	raw, err := os.ReadFile(filepath.Join(
		filepath.Dir(filename),
		"production-runtime-contract.json",
	))
	if err != nil {
		t.Fatalf("read production runtime contract: %v", err)
	}
	var contract productionContract
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contract); err != nil {
		t.Fatalf("decode production runtime contract: %v", err)
	}

	if contract.Region != "us-east1" {
		t.Errorf("production region = %q, want us-east1", contract.Region)
	}
	wantDatabase := databaseCapacity{
		Version:                    "POSTGRES_16",
		Edition:                    "ENTERPRISE",
		AvailabilityType:           "ZONAL",
		VCPUs:                      2,
		MemoryMiB:                  8192,
		StorageGiB:                 50,
		StorageType:                "SSD",
		AutomatedBackups:           true,
		BackupLocation:             "us-east1",
		BackupStartTimeUTC:         "04:00",
		PointInTimeRecovery:        true,
		RetainedTransactionLogDays: 7,
		RetainedBackups:            7,
		DeletionProtection:         true,
		DataCache:                  false,
		StorageAutoIncrease:        true,
	}
	if contract.Database != wantDatabase {
		t.Errorf("database = %+v, want %+v", contract.Database, wantDatabase)
	}

	expected := map[string]runtimeCapacity{
		"web": {
			Name:                           "web",
			Kind:                           "service",
			BillingMode:                    "request-based",
			VCPUs:                          1,
			MemoryMiB:                      512,
			Concurrency:                    40,
			MinimumInstances:               1,
			MaximumInstances:               2,
			PoolMaximum:                    1,
			AcquisitionTimeoutMilliseconds: 1500,
		},
		"portal-api": {
			Name:                           "portal-api",
			Kind:                           "service",
			BillingMode:                    "request-based",
			VCPUs:                          1,
			MemoryMiB:                      512,
			Concurrency:                    20,
			MinimumInstances:               1,
			MaximumInstances:               3,
			PoolMaximum:                    2,
			AcquisitionTimeoutMilliseconds: 1500,
		},
		"provider-ingress": {
			Name:                           "provider-ingress",
			Kind:                           "service",
			BillingMode:                    "request-based",
			VCPUs:                          1,
			MemoryMiB:                      512,
			Concurrency:                    20,
			MinimumInstances:               1,
			MaximumInstances:               2,
			PoolMaximum:                    1,
			AcquisitionTimeoutMilliseconds: 1500,
		},
		"realtime": {
			Name:                           "realtime",
			Kind:                           "service",
			BillingMode:                    "request-based",
			VCPUs:                          1,
			MemoryMiB:                      512,
			Concurrency:                    50,
			MinimumInstances:               1,
			MaximumInstances:               2,
			PoolMaximum:                    1,
			DedicatedConnections:           1,
			AcquisitionTimeoutMilliseconds: 1500,
			RequestTimeoutSeconds:          300,
			StreamMaximumSeconds:           270,
			StreamJitterSeconds:            30,
		},
		"worker": {
			Name:                           "worker",
			Kind:                           "worker-pool",
			BillingMode:                    "instance-based",
			VCPUs:                          1,
			MemoryMiB:                      512,
			MinimumInstances:               1,
			MaximumInstances:               1,
			PoolMaximum:                    2,
			AcquisitionTimeoutMilliseconds: 1500,
		},
	}
	if len(contract.Runtimes) != len(expected) {
		t.Fatalf("runtime count = %d, want %d", len(contract.Runtimes), len(expected))
	}

	serviceConnections := 0
	oneExtraServiceInstanceConnections := 0
	workerPoolConnectionsPerRevision := 0
	for _, configured := range contract.Runtimes {
		want, ok := expected[configured.Name]
		if !ok {
			t.Fatalf("unexpected production runtime %q", configured.Name)
		}
		if configured != want {
			t.Errorf("runtime %s = %+v, want %+v", configured.Name, configured, want)
		}
		runtimeConnections := configured.MaximumInstances *
			(configured.PoolMaximum + configured.DedicatedConnections)
		if configured.Kind == "service" {
			serviceConnections += runtimeConnections
			oneExtraServiceInstanceConnections +=
				configured.PoolMaximum + configured.DedicatedConnections
		} else {
			workerPoolConnectionsPerRevision += runtimeConnections
		}
		delete(expected, configured.Name)
	}
	if len(expected) != 0 {
		t.Fatalf("missing production runtimes: %v", expected)
	}
	if serviceConnections != 14 {
		t.Errorf(
			"configured service connection demand = %d, want 14",
			serviceConnections,
		)
	}
	if workerPoolConnectionsPerRevision != 2 {
		t.Errorf(
			"worker-pool connection demand per revision = %d, want 2",
			workerPoolConnectionsPerRevision,
		)
	}
	if contract.WorkerPoolRevisionOverlap != 2 {
		t.Errorf(
			"worker-pool revision overlap = %d, want 2",
			contract.WorkerPoolRevisionOverlap,
		)
	}
	wantMigration := migrationCapacity{
		Tasks:                          1,
		BillingMode:                    "instance-based",
		VCPUs:                          1,
		MemoryMiB:                      512,
		PoolMaximum:                    1,
		AcquisitionTimeoutMilliseconds: 5000,
		MaximumRetries:                 0,
	}
	if contract.Migration != wantMigration {
		t.Errorf("migration = %+v, want %+v", contract.Migration, wantMigration)
	}
	if contract.OperatorHeadroom != 3 {
		t.Errorf("operator headroom = %d, want 3", contract.OperatorHeadroom)
	}
	if oneExtraServiceInstanceConnections != 6 {
		t.Errorf(
			"one-extra-instance service demand = %d, want 6",
			oneExtraServiceInstanceConnections,
		)
	}
	if contract.AutoscalerOvershootHeadroom !=
		oneExtraServiceInstanceConnections {
		t.Errorf(
			"autoscaler overshoot headroom = %d, calculated %d",
			contract.AutoscalerOvershootHeadroom,
			oneExtraServiceInstanceConnections,
		)
	}

	calculated := serviceConnections +
		workerPoolConnectionsPerRevision*contract.WorkerPoolRevisionOverlap +
		contract.Migration.Tasks*contract.Migration.PoolMaximum +
		oneExtraServiceInstanceConnections +
		contract.OperatorHeadroom
	if calculated != 28 {
		t.Errorf("calculated production connection reservation = %d, want 28", calculated)
	}
	if contract.RequiredDatabaseConnections != calculated {
		t.Errorf(
			"required database connections = %d, calculated %d",
			contract.RequiredDatabaseConnections,
			calculated,
		)
	}
}

func TestProductionRendererIncludesAuditableResourceAndRegionRows(t *testing.T) {
	directory := productionDeployDirectory(t)
	command := exec.Command(
		"node",
		filepath.Join(directory, "render-production-runtime-contract.mjs"),
		filepath.Join(directory, "production-runtime-contract.json"),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("render production contract: %v\n%s", err, output)
	}
	rows := strings.Split(strings.TrimSpace(string(output)), "\n")
	expected := []string{
		"capacity\tmeta\t0\t0\t28\t0\t0\t0\t0\t0\t0\t0\t0\t0\tmeta\tus-east1",
		"web\tservice\t40\t1\t2\t1\t0\t1500\t0\t0\t0\t0\t1\t512\trequest-based\tus-east1",
		"portal-api\tservice\t20\t1\t3\t2\t0\t1500\t0\t0\t0\t0\t1\t512\trequest-based\tus-east1",
		"provider-ingress\tservice\t20\t1\t2\t1\t0\t1500\t0\t0\t0\t0\t1\t512\trequest-based\tus-east1",
		"realtime\tservice\t50\t1\t2\t1\t1\t1500\t300\t270\t30\t0\t1\t512\trequest-based\tus-east1",
		"worker\tworker-pool\t0\t1\t1\t2\t0\t1500\t0\t0\t0\t0\t1\t512\tinstance-based\tus-east1",
		"migrate\tjob\t0\t0\t1\t1\t0\t5000\t0\t0\t0\t0\t1\t512\tinstance-based\tus-east1",
		"database\tdatabase\t0\t0\t1\t0\t0\t0\t0\t0\t0\t0\t2\t8192\tinstance-based\tus-east1\tPOSTGRES_16\tENTERPRISE\tZONAL\t50\tSSD\t04:00\t7\t7\t1\t1\t1\t0\t1\tus-east1",
	}
	if len(rows) != len(expected) {
		t.Fatalf("rendered row count = %d, want %d\n%s", len(rows), len(expected), output)
	}
	for index, want := range expected {
		if rows[index] != want {
			t.Errorf("rendered row %d = %q, want %q", index, rows[index], want)
		}
	}
}

func TestProductionCloudRunCommandsUseRenderedValues(t *testing.T) {
	directory := productionDeployDirectory(t)
	path, capture := installFakeGcloud(t)
	environment := append(
		[]string{"PATH=" + path, "GCLOUD_CAPTURE=" + capture},
		productionRuntimeEnvironment()...,
	)
	command := exec.Command(
		"sh",
		filepath.Join(directory, "cloud-run-commands.example.sh"),
	)
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("render Cloud Run commands: %v\n%s", err, output)
	}
	commands := capturedGcloudCommands(t, capture)
	if len(commands) != 6 {
		t.Fatalf("captured Cloud Run command count = %d, want 6\n%s", len(commands), strings.Join(commands, "\n"))
	}
	for _, retired := range []string{
		"TELNYX_RECORDING_BUCKET",
		"HUMAN_CALLING_RECORDING_HOSTS",
		"HUMAN_CALLING_RECORDING_CA_FILE",
	} {
		if strings.Contains(strings.Join(commands, "\n"), retired) {
			t.Fatalf("rendered runtime still requires %s", retired)
		}
	}
	assertCapturedCommand(t, commands, "run\tdeploy\tacuity-portal-api",
		"--region\tus-east1",
		"--cpu\t1",
		"--memory\t512Mi",
		"--cpu-throttling",
		"--concurrency\t20",
		"--min\t1",
		"--max\t3",
		"DATABASE_POOL_MAX=2",
		"BROWSER_ORIGIN=https://portal.example,https://legacy.example",
		"HUMAN_CALLING_PLAYBACK_SIGNING_KEY=playback-signing-key:latest",
		"MESSAGING_WEBHOOK_BASE_URL=https://ingress.example/v1/provider/telnyx/messaging-webhooks",
		"--add-volume\tname=messaging-attachments,type=cloud-storage,bucket=acuity-messaging",
		"--add-volume-mount\tvolume=messaging-attachments,mount-path=/mnt/acuity-messaging",
	)
	assertCapturedCommand(t, commands, "run\tdeploy\tacuity-provider-ingress",
		"--region\tus-east1",
		"--cpu\t1",
		"--memory\t512Mi",
		"--cpu-throttling",
		"--concurrency\t20",
		"--min\t1",
		"--max\t2",
		"DATABASE_POOL_MAX=1",
		"MESSAGING_MEDIA_SIGNING_KEY=messaging-media-signing-key:latest",
		"TELNYX_WEBHOOK_PUBLIC_KEY=test-public-key",
		"TELNYX_WEBHOOK_NEXT_PUBLIC_KEY=test-next-public-key",
		"MESSAGING_ATTACHMENT_DIRECTORY=/mnt/acuity-messaging",
		"--add-volume\tname=messaging-attachments,type=cloud-storage,bucket=acuity-messaging,readonly=true",
		"--add-volume-mount\tvolume=messaging-attachments,mount-path=/mnt/acuity-messaging",
	)
	assertCapturedCommand(t, commands, "run\tdeploy\tacuity-realtime",
		"--region\tus-east1",
		"--cpu\t1",
		"--memory\t512Mi",
		"--timeout\t300",
		"--cpu-throttling",
		"--concurrency\t50",
		"--min\t1",
		"--max\t2",
		"DATABASE_POOL_MAX=1",
		"BROWSER_ORIGIN=https://portal.example,https://legacy.example",
		"REALTIME_STREAM_SECONDS=270",
		"REALTIME_STREAM_JITTER_SECONDS=30",
	)
	assertCapturedCommand(t, commands, "run\tdeploy\tacuity-web",
		"--region\tus-east1",
		"--cpu\t1",
		"--memory\t512Mi",
		"--cpu-throttling",
		"--concurrency\t40",
		"--min\t1",
		"--max\t2",
		"AUTH_DB_POOL_MAX=1",
		"BETTER_AUTH_URL=https://portal.example",
		"BETTER_AUTH_TRUSTED_ORIGINS=https://portal.example,https://legacy.example",
	)
	assertCapturedCommand(t, commands, "run\tworker-pools\tdeploy\tacuity-worker",
		"--region\tus-east1",
		"--instances\t1",
		"--cpu\t1",
		"--memory\t512Mi",
		"DATABASE_POOL_MAX=2",
		"MESSAGING_MEDIA_SIGNING_KEY=messaging-media-signing-key:latest",
		"HUMAN_CALLING_HANDOFF_TOKEN_KEY=handoff-token-key:latest",
		"HUMAN_CALLING_PLAYBACK_SIGNING_KEY=playback-signing-key:latest",
		"HUMAN_CALLING_SIP_DOMAIN=caller.example",
		"HUMAN_CALLING_STAFF_SIP_DOMAIN=staff.example",
		"MESSAGING_MEDIA_PUBLIC_BASE_URL=https://ingress.example/v1/provider/messaging-media",
		"--add-volume\tname=messaging-attachments,type=cloud-storage,bucket=acuity-messaging",
		"--add-volume-mount\tvolume=messaging-attachments,mount-path=/mnt/acuity-messaging",
	)
	assertCapturedCommand(t, commands, "run\tjobs\tdeploy\tacuity-migrate",
		"--region\tus-east1",
		"--tasks\t1",
		"--max-retries\t0",
		"--cpu\t1",
		"--memory\t512Mi",
		"DATABASE_POOL_MAX=1",
	)
	for _, command := range commands {
		if strings.Contains(command, "HUMAN_CALLING_SAFE_VOICEMAIL_GREETING_URL") {
			t.Fatalf("obsolete voicemail greeting URL reached gcloud: %s", command)
		}
	}
}

func TestProductionCloudRunCommandsFailClosed(t *testing.T) {
	directory := productionDeployDirectory(t)
	for _, test := range []struct {
		name    string
		key     string
		value   string
		message string
	}{
		{
			name:    "region",
			key:     "GCP_REGION",
			value:   "us-central1",
			message: "production region must be us-east1",
		},
		{
			name:    "connections",
			key:     "USABLE_DATABASE_CONNECTIONS",
			value:   "27",
			message: "production requires at least 28 usable database connections",
		},
		{
			name:    "messaging attachment bucket",
			key:     "MESSAGING_ATTACHMENT_BUCKET_LOCATION",
			value:   "us-central1",
			message: "messaging attachment bucket must be in us-east1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, capture := installFakeGcloud(t)
			environment := replaceEnvironment(
				productionRuntimeEnvironment(),
				test.key,
				test.value,
			)
			command := exec.Command(
				"sh",
				filepath.Join(directory, "cloud-run-commands.example.sh"),
			)
			command.Env = append(
				[]string{"PATH=" + path, "GCLOUD_CAPTURE=" + capture},
				environment...,
			)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("Cloud Run commands accepted unsafe %s", test.name)
			}
			if !strings.Contains(string(output), test.message) {
				t.Fatalf(
					"Cloud Run %s error = %q, want %q",
					test.name,
					output,
					test.message,
				)
			}
			if _, err := os.Stat(capture); !os.IsNotExist(err) {
				t.Fatalf("Cloud Run %s reached gcloud", test.name)
			}
		})
	}
}

func TestProductionCloudSQLCommandUsesRenderedValues(t *testing.T) {
	directory := productionDeployDirectory(t)
	path, capture := installFakeGcloud(t)
	command := exec.Command(
		"sh",
		filepath.Join(directory, "cloud-sql-commands.example.sh"),
	)
	command.Env = []string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + capture,
		"GCP_PROJECT=acuity-test",
		"GCP_REGION=us-east1",
		"CLOUD_SQL_INSTANCE_NAME=acuity-production",
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("render Cloud SQL command: %v\n%s", err, output)
	}
	commands := capturedGcloudCommands(t, capture)
	if len(commands) != 1 {
		t.Fatalf("captured Cloud SQL command count = %d, want 1", len(commands))
	}
	assertCapturedCommand(t, commands, "sql\tinstances\tcreate\tacuity-production",
		"--region\tus-east1",
		"--database-version\tPOSTGRES_16",
		"--edition\tENTERPRISE",
		"--availability-type\tZONAL",
		"--cpu\t2",
		"--memory\t8192MiB",
		"--storage-size\t50",
		"--storage-type\tSSD",
		"--backup",
		"--backup-location\tus-east1",
		"--backup-start-time\t04:00",
		"--enable-point-in-time-recovery",
		"--retained-transaction-log-days\t7",
		"--retained-backups-count\t7",
		"--deletion-protection",
		"--no-enable-data-cache",
		"--storage-auto-increase",
	)
}

func TestProductionCloudSQLCommandFailsClosedOnRegion(t *testing.T) {
	directory := productionDeployDirectory(t)
	path, capture := installFakeGcloud(t)
	command := exec.Command(
		"sh",
		filepath.Join(directory, "cloud-sql-commands.example.sh"),
	)
	command.Env = []string{
		"PATH=" + path,
		"GCLOUD_CAPTURE=" + capture,
		"GCP_PROJECT=acuity-test",
		"GCP_REGION=us-central1",
		"CLOUD_SQL_INSTANCE_NAME=acuity-production",
	}
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("Cloud SQL command accepted a region outside the contract")
	}
	if !strings.Contains(
		string(output),
		"production region must be us-east1",
	) {
		t.Fatalf("Cloud SQL region error = %q", output)
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatal("Cloud SQL region mismatch reached gcloud")
	}
}

func installFakeGcloud(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	executable := filepath.Join(directory, "gcloud")
	capture := filepath.Join(directory, "gcloud.tsv")
	script := `#!/bin/sh
set -eu
separator=
for argument do
  printf '%s%s' "$separator" "$argument" >>"$GCLOUD_CAPTURE"
  separator='	'
done
printf '\n' >>"$GCLOUD_CAPTURE"
`
	if err := os.WriteFile(executable, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gcloud: %v", err)
	}
	return directory + ":" + os.Getenv("PATH"), capture
}

func capturedGcloudCommands(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read captured gcloud commands: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func assertCapturedCommand(
	t *testing.T,
	commands []string,
	prefix string,
	required ...string,
) {
	t.Helper()
	for _, command := range commands {
		if !containsCapturedFields(command, prefix) ||
			!strings.HasPrefix(command, prefix) {
			continue
		}
		matches := true
		for _, value := range required {
			found := containsCapturedFields(command, value)
			if strings.Contains(value, "=") &&
				!strings.Contains(value, "\t") {
				found = strings.Contains(command, value)
			}
			if !found {
				matches = false
				break
			}
		}
		if matches {
			return
		}
	}
	t.Errorf("captured commands omit %q with fields %q", prefix, required)
}

func containsCapturedFields(command string, required string) bool {
	commandFields := strings.Split(command, "\t")
	requiredFields := strings.Split(required, "\t")
	for start := 0; start+len(requiredFields) <= len(commandFields); start++ {
		matches := true
		for offset, value := range requiredFields {
			if commandFields[start+offset] != value {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func productionRuntimeEnvironment() []string {
	return []string{
		"GCP_PROJECT=acuity-test",
		"GCP_REGION=us-east1",
		"CLOUD_SQL_INSTANCE=acuity-test:us-east1:acuity-production",
		"BACKEND_IMAGE_DIGEST=backend@sha256:test",
		"WEB_IMAGE_DIGEST=web@sha256:test",
		"RUNTIME_SERVICE_ACCOUNT_PREFIX=acuity",
		"PORTAL_DATABASE_URL_SECRET=portal-database",
		"PROVIDER_DATABASE_URL_SECRET=provider-database",
		"REALTIME_DATABASE_URL_SECRET=realtime-database",
		"WORKER_DATABASE_URL_SECRET=worker-database",
		"MIGRATE_DATABASE_URL_SECRET=migrate-database",
		"WEB_AUTH_DATABASE_URL_SECRET=auth-database",
		"BETTER_AUTH_SECRET_SECRET=better-auth-secret",
		"GOOGLE_CLIENT_ID_SECRET=google-client-id",
		"GOOGLE_CLIENT_SECRET_SECRET=google-client-secret",
		"TELNYX_API_KEY_SECRET=telnyx-api-key",
		"MESSAGING_MEDIA_SIGNING_KEY_SECRET=messaging-media-signing-key",
		"HANDOFF_TOKEN_KEY_SECRET=handoff-token-key",
		"PLAYBACK_SIGNING_KEY_SECRET=playback-signing-key",
		"ACUITY_DEMO_SERVICE_TOKEN_SECRET=demo-service-token",
		"ABITA_EYE_GROUP_SERVICE_TOKEN_SECRET=production-service-token",
		"BROWSER_ORIGIN=https://portal.example",
		"BROWSER_ALLOWED_ORIGINS=https://portal.example,https://legacy.example",
		"NEXT_PUBLIC_PORTAL_API_URL=https://api.example",
		"NEXT_PUBLIC_REALTIME_URL=https://realtime.example",
		"BETTER_AUTH_JWKS_URL=https://portal.example/api/auth/jwks",
		"BETTER_AUTH_ISSUER=https://portal.example",
		"PORTAL_API_AUDIENCE=https://api.example",
		"PORTAL_API_INTERNAL_URL=https://api.example",
		"HUMAN_CALLING_SIP_DOMAIN=caller.example",
		"HUMAN_CALLING_STAFF_SIP_DOMAIN=staff.example",
		"ACUITY_DEMO_SERVICE_SUBJECT=abita-demo",
		"ACUITY_DEMO_SERVICE_PRACTICE_ID=00000000-0000-0000-0000-000000000001",
		"ABITA_EYE_GROUP_SERVICE_SUBJECT=abita-eye-group",
		"ABITA_EYE_GROUP_SERVICE_PRACTICE_ID=00000000-0000-0000-0000-000000000002",
		"TELNYX_CALL_CONTROL_ID=call-control",
		"TELNYX_CREDENTIAL_CONNECTION_ID=credential",
		"TELNYX_FROM_NUMBER=+15555550100",
		"TELNYX_RINGBACK_URL=https://portal.example/ringback.wav",
		"TELNYX_WEBHOOK_PUBLIC_KEY=test-public-key",
		"TELNYX_WEBHOOK_NEXT_PUBLIC_KEY=test-next-public-key",
		"MESSAGING_WEBHOOK_BASE_URL=https://ingress.example/v1/provider/telnyx/messaging-webhooks",
		"MESSAGING_MEDIA_PUBLIC_BASE_URL=https://ingress.example/v1/provider/messaging-media",
		"MESSAGING_ATTACHMENT_BUCKET=acuity-messaging",
		"MESSAGING_ATTACHMENT_BUCKET_LOCATION=us-east1",
		"MESSAGING_ATTACHMENT_DIRECTORY=/mnt/acuity-messaging",
		"ACUITY_DEPLOYMENT_PROFILE=production",
		"USABLE_DATABASE_CONNECTIONS=28",
	}
}

func replaceEnvironment(
	environment []string,
	key string,
	value string,
) []string {
	replaced := append([]string(nil), environment...)
	prefix := key + "="
	for index, item := range replaced {
		if strings.HasPrefix(item, prefix) {
			replaced[index] = prefix + value
			return replaced
		}
	}
	return append(replaced, prefix+value)
}

func productionDeployDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate production deploy directory")
	}
	return filepath.Dir(filename)
}
