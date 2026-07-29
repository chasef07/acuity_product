package deploy_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type productionContract struct {
	RevisionOverlap             int               `json:"revisionOverlap"`
	Migration                   migrationCapacity `json:"migration"`
	OperatorHeadroom            int               `json:"operatorHeadroom"`
	RequiredDatabaseConnections int               `json:"requiredDatabaseConnections"`
	Runtimes                    []runtimeCapacity `json:"runtimes"`
}

type runtimeCapacity struct {
	Name                           string `json:"name"`
	Kind                           string `json:"kind"`
	Concurrency                    int    `json:"concurrency"`
	MinimumInstances               int    `json:"minimumInstances"`
	MaximumInstances               int    `json:"maximumInstances"`
	PoolMaximum                    int    `json:"poolMaximum"`
	DedicatedConnections           int    `json:"dedicatedConnections"`
	AcquisitionTimeoutMilliseconds int    `json:"acquisitionTimeoutMilliseconds"`
}

type migrationCapacity struct {
	Tasks                          int `json:"tasks"`
	PoolMaximum                    int `json:"poolMaximum"`
	AcquisitionTimeoutMilliseconds int `json:"acquisitionTimeoutMilliseconds"`
	MaximumRetries                 int `json:"maximumRetries"`
}

func TestProductionRuntimeContractBoundsCapacityAndKeepsCallingWarm(t *testing.T) {
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

	expected := map[string]runtimeCapacity{
		"web": {
			Name:                           "web",
			Kind:                           "service",
			Concurrency:                    40,
			MinimumInstances:               1,
			MaximumInstances:               2,
			PoolMaximum:                    3,
			AcquisitionTimeoutMilliseconds: 1500,
		},
		"portal-api": {
			Name:                           "portal-api",
			Kind:                           "service",
			Concurrency:                    20,
			MinimumInstances:               2,
			MaximumInstances:               3,
			PoolMaximum:                    4,
			AcquisitionTimeoutMilliseconds: 1500,
		},
		"provider-ingress": {
			Name:                           "provider-ingress",
			Kind:                           "service",
			Concurrency:                    20,
			MinimumInstances:               2,
			MaximumInstances:               2,
			PoolMaximum:                    2,
			AcquisitionTimeoutMilliseconds: 1500,
		},
		"realtime": {
			Name:                           "realtime",
			Kind:                           "service",
			Concurrency:                    50,
			MinimumInstances:               2,
			MaximumInstances:               2,
			PoolMaximum:                    3,
			DedicatedConnections:           1,
			AcquisitionTimeoutMilliseconds: 1500,
		},
		"worker": {
			Name:                           "worker",
			Kind:                           "worker-pool",
			MinimumInstances:               2,
			MaximumInstances:               2,
			PoolMaximum:                    2,
			AcquisitionTimeoutMilliseconds: 1500,
		},
	}
	if len(contract.Runtimes) != len(expected) {
		t.Fatalf("runtime count = %d, want %d", len(contract.Runtimes), len(expected))
	}

	singleRevisionConnections := 0
	for _, configured := range contract.Runtimes {
		want, ok := expected[configured.Name]
		if !ok {
			t.Fatalf("unexpected production runtime %q", configured.Name)
		}
		if configured != want {
			t.Errorf("runtime %s = %+v, want %+v", configured.Name, configured, want)
		}
		singleRevisionConnections += configured.MaximumInstances *
			(configured.PoolMaximum + configured.DedicatedConnections)
		delete(expected, configured.Name)
	}
	if len(expected) != 0 {
		t.Fatalf("missing production runtimes: %v", expected)
	}
	if singleRevisionConnections != 34 {
		t.Errorf(
			"single-revision connection ceiling = %d, want 34",
			singleRevisionConnections,
		)
	}
	if contract.RevisionOverlap != 2 {
		t.Errorf("revision overlap = %d, want 2", contract.RevisionOverlap)
	}
	wantMigration := migrationCapacity{
		Tasks:                          1,
		PoolMaximum:                    2,
		AcquisitionTimeoutMilliseconds: 5000,
		MaximumRetries:                 0,
	}
	if contract.Migration != wantMigration {
		t.Errorf("migration = %+v, want %+v", contract.Migration, wantMigration)
	}
	if contract.OperatorHeadroom != 10 {
		t.Errorf("operator headroom = %d, want 10", contract.OperatorHeadroom)
	}

	calculated := singleRevisionConnections*contract.RevisionOverlap +
		contract.Migration.Tasks*contract.Migration.PoolMaximum +
		contract.OperatorHeadroom
	if calculated != 80 {
		t.Errorf("calculated production connection ceiling = %d, want 80", calculated)
	}
	if contract.RequiredDatabaseConnections != calculated {
		t.Errorf(
			"required database connections = %d, calculated %d",
			contract.RequiredDatabaseConnections,
			calculated,
		)
	}
}
