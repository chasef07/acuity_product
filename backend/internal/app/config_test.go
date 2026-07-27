package app

import (
	"testing"
	"time"
)

func TestLoadConfigKeepsRuntimeRolesAndDatabasePoolsExplicit(t *testing.T) {
	base := map[string]string{
		"ACUITY_RUNTIME_ROLE":                      "portal-api",
		"DATABASE_URL":                             "postgres://database.example/acuity",
		"DATABASE_POOL_MAX":                        "4",
		"DATABASE_ACQUIRE_TIMEOUT_MS":              "1500",
		"HTTP_PORT":                                "8080",
		"BROWSER_ORIGIN":                           "https://portal.example",
		"BETTER_AUTH_JWKS_URL":                     "https://portal.example/api/auth/jwks",
		"BETTER_AUTH_ISSUER":                       "https://portal.example",
		"PORTAL_API_AUDIENCE":                      "https://api.example",
		"HUMAN_CALLING_SIP_DOMAIN":                 "synthetic.sip.telnyx.com",
		"HUMAN_CALLING_HANDOFF_TOKEN_KEY":          "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		"HUMAN_CALLING_OFFER_SECONDS":              "20",
		"HUMAN_CALLING_CONNECTION_TIMEOUT_SECONDS": "15",
		"HUMAN_CALLING_LEASE_SECONDS":              "30",
		"HUMAN_CALLING_READINESS_GRACE_SECONDS":    "15",
		"HANDOFF_SERVICE_TOKEN":                    "synthetic-service-token",
		"HANDOFF_SERVICE_SUBJECT":                  "abita-synthetic",
		"HANDOFF_SERVICE_PRACTICE_ID":              "00000000-0000-0000-0000-000000000001",
		"TELNYX_API_KEY":                           "KEY_synthetic",
		"TELNYX_CALL_CONTROL_ID":                   "call-control-synthetic",
		"TELNYX_CREDENTIAL_CONNECTION_ID":          "credential-connection-synthetic",
		"TELNYX_FROM_NUMBER":                       "+15555550100",
		"TELNYX_RINGBACK_URL":                      "https://assets.example/ringback.wav",
		"TELNYX_RECORDING_BUCKET":                  "synthetic-recordings",
		"TELNYX_WEBHOOK_PUBLIC_KEY":                "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		"REALTIME_HEARTBEAT_SECONDS":               "15",
		"REALTIME_STREAM_SECONDS":                  "300",
		"REALTIME_REVALIDATE_SECONDS":              "30",
		"REALTIME_RECONNECT_MIN_MS":                "250",
		"REALTIME_RECONNECT_MAX_SECONDS":           "5",
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
	if config.HumanCalling.OfferDuration != 20*time.Second ||
		config.HumanCalling.ConnectionTimeout != 15*time.Second ||
		config.HumanCalling.LeaseDuration != 30*time.Second ||
		config.HumanCalling.ReadinessGrace != 15*time.Second {
		t.Fatalf("calling timings = %#v", config.HumanCalling)
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
		if role == RoleProviderIngress || role == RoleRealtime || role == RoleMigrate {
			delete(values, "TELNYX_API_KEY")
			delete(values, "TELNYX_CALL_CONTROL_ID")
			delete(values, "TELNYX_CREDENTIAL_CONNECTION_ID")
			delete(values, "TELNYX_FROM_NUMBER")
			delete(values, "TELNYX_RINGBACK_URL")
			delete(values, "TELNYX_RECORDING_BUCKET")
		}
		if role == RoleProviderIngress || role == RoleWorker || role == RoleRealtime || role == RoleMigrate {
			delete(values, "HUMAN_CALLING_SIP_DOMAIN")
			delete(values, "HUMAN_CALLING_HANDOFF_TOKEN_KEY")
			delete(values, "HANDOFF_SERVICE_TOKEN")
			delete(values, "HANDOFF_SERVICE_SUBJECT")
			delete(values, "HANDOFF_SERVICE_PRACTICE_ID")
		}
		if role != RoleProviderIngress {
			delete(values, "TELNYX_WEBHOOK_PUBLIC_KEY")
		}
		if _, err := LoadConfig(func(name string) string { return values[name] }); err != nil {
			t.Fatalf("load %s config: %v", role, err)
		}
	}
}

func TestLoadConfigRejectsMalformedHumanCallingKeys(t *testing.T) {
	base := map[string]string{
		"ACUITY_RUNTIME_ROLE":             "portal-api",
		"DATABASE_URL":                    "postgres://database.example/acuity",
		"DATABASE_POOL_MAX":               "4",
		"DATABASE_ACQUIRE_TIMEOUT_MS":     "1500",
		"HTTP_PORT":                       "8080",
		"BROWSER_ORIGIN":                  "https://portal.example",
		"BETTER_AUTH_JWKS_URL":            "https://portal.example/api/auth/jwks",
		"BETTER_AUTH_ISSUER":              "https://portal.example",
		"PORTAL_API_AUDIENCE":             "https://api.example",
		"HUMAN_CALLING_SIP_DOMAIN":        "synthetic.sip.telnyx.com",
		"HUMAN_CALLING_HANDOFF_TOKEN_KEY": "too-short",
		"HANDOFF_SERVICE_TOKEN":           "synthetic-service-token",
		"HANDOFF_SERVICE_SUBJECT":         "abita-synthetic",
		"HANDOFF_SERVICE_PRACTICE_ID":     "00000000-0000-0000-0000-000000000001",
		"TELNYX_API_KEY":                  "KEY_synthetic",
		"TELNYX_CALL_CONTROL_ID":          "call-control-synthetic",
		"TELNYX_CREDENTIAL_CONNECTION_ID": "credential-connection-synthetic",
		"TELNYX_FROM_NUMBER":              "+15555550100",
		"TELNYX_RINGBACK_URL":             "https://assets.example/ringback.wav",
		"TELNYX_RECORDING_BUCKET":         "synthetic-recordings",
	}
	if _, err := LoadConfig(func(name string) string { return base[name] }); err == nil {
		t.Fatal("expected malformed handoff token key to fail closed")
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
