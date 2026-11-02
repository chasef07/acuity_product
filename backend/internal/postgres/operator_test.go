package postgres

import (
	"testing"
	"time"
)

func TestOperatorPoolConfigPinsSafetyEnvelope(t *testing.T) {
	t.Setenv("TEST_IMPORT_DATABASE_URL", "postgres://operator@db.example.com:6543/acuity?sslmode=require")
	t.Setenv("TEST_DATABASE_HOST", "127.0.0.1")

	config, err := operatorPoolConfig("TEST_IMPORT_DATABASE_URL", "TEST_DATABASE_HOST")
	if err != nil {
		t.Fatal(err)
	}
	if config.ConnConfig.Host != "127.0.0.1" || config.ConnConfig.Port != 5432 || config.ConnConfig.TLSConfig != nil || config.ConnConfig.Fallbacks != nil {
		t.Fatalf("host override = %s:%d tls=%v fallbacks=%d", config.ConnConfig.Host, config.ConnConfig.Port, config.ConnConfig.TLSConfig != nil, len(config.ConnConfig.Fallbacks))
	}
	if config.MaxConns != 1 || config.MinConns != 0 || config.ConnConfig.ConnectTimeout != 5*time.Second {
		t.Fatalf("pool limits = max %d min %d connect %s", config.MaxConns, config.MinConns, config.ConnConfig.ConnectTimeout)
	}
	if config.ConnConfig.RuntimeParams["statement_timeout"] != "10000" || config.ConnConfig.RuntimeParams["lock_timeout"] != "2000" {
		t.Fatalf("runtime params = %v", config.ConnConfig.RuntimeParams)
	}
}

func TestOperatorPoolConfigRejectsNonLoopbackHostOverride(t *testing.T) {
	t.Setenv("TEST_IMPORT_DATABASE_URL", "postgres://operator@db.example.com/acuity")
	t.Setenv("TEST_DATABASE_HOST", "10.0.0.5")

	_, err := operatorPoolConfig("TEST_IMPORT_DATABASE_URL", "TEST_DATABASE_HOST")
	if err == nil || err.Error() != "TEST_DATABASE_HOST override only supports the loopback Cloud SQL proxy" {
		t.Fatalf("err = %v", err)
	}
}
