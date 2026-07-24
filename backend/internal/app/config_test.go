package app

import (
	"testing"
	"time"
)

func TestLoadConfigKeepsRuntimeRolesAndDatabasePoolsExplicit(t *testing.T) {
	base := map[string]string{
		"ACUITY_RUNTIME_ROLE":            "portal-api",
		"DATABASE_URL":                   "postgres://database.example/acuity",
		"DATABASE_POOL_MAX":              "4",
		"DATABASE_ACQUIRE_TIMEOUT_MS":    "1500",
		"HTTP_PORT":                      "8080",
		"BROWSER_ORIGIN":                 "https://portal.example",
		"BETTER_AUTH_JWKS_URL":           "https://portal.example/api/auth/jwks",
		"BETTER_AUTH_ISSUER":             "https://portal.example",
		"PORTAL_API_AUDIENCE":            "https://api.example",
		"REALTIME_HEARTBEAT_SECONDS":     "15",
		"REALTIME_STREAM_SECONDS":        "300",
		"REALTIME_REVALIDATE_SECONDS":    "30",
		"REALTIME_RECONNECT_MIN_MS":      "250",
		"REALTIME_RECONNECT_MAX_SECONDS": "5",
	}

	config, err := LoadConfig(func(name string) string { return base[name] })
	if err != nil {
		t.Fatalf("load portal config: %v", err)
	}
	if config.Role != RolePortalAPI || config.PoolMax != 4 {
		t.Fatalf("config = %#v", config)
	}
	if config.AcquireTimeout != 1500*time.Millisecond {
		t.Fatalf("acquisition timeout = %s", config.AcquireTimeout)
	}

	for _, role := range []Role{
		RolePortalAPI,
		RoleProviderIngress,
		RoleRealtime,
		RoleWorker,
		RoleMigrate,
	} {
		values := clone(base)
		values["ACUITY_RUNTIME_ROLE"] = string(role)
		if role == RoleProviderIngress || role == RoleWorker || role == RoleMigrate {
			delete(values, "BETTER_AUTH_JWKS_URL")
			delete(values, "BETTER_AUTH_ISSUER")
			delete(values, "PORTAL_API_AUDIENCE")
		}
		if _, err := LoadConfig(func(name string) string { return values[name] }); err != nil {
			t.Fatalf("load %s config: %v", role, err)
		}
	}
}

func TestLoadConfigRejectsUnknownRolesAndUnboundedPools(t *testing.T) {
	for name, values := range map[string]map[string]string{
		"unknown role": {
			"ACUITY_RUNTIME_ROLE": "scheduler",
		},
		"zero pool": {
			"ACUITY_RUNTIME_ROLE":         "worker",
			"DATABASE_URL":                "postgres://database.example/acuity",
			"DATABASE_POOL_MAX":           "0",
			"DATABASE_ACQUIRE_TIMEOUT_MS": "1000",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(func(name string) string { return values[name] }); err == nil {
				t.Fatal("expected invalid configuration")
			}
		})
	}
}

func clone(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
