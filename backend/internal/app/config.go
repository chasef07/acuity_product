package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Role string

const (
	RolePortalAPI       Role = "portal-api"
	RoleProviderIngress Role = "provider-ingress"
	RoleRealtime        Role = "realtime"
	RoleWorker          Role = "worker"
	RoleMigrate         Role = "migrate"
)

type Config struct {
	Role               Role
	DatabaseURL        string
	PoolMax            int32
	AcquireTimeout     time.Duration
	HTTPPort           int
	BrowserOrigin      string
	JWKSURL            string
	AuthIssuer         string
	APIAudience        string
	ProvisioningInput  string
	ProvisioningOutput string
	Realtime           RealtimeConfig
}

type RealtimeConfig struct {
	Heartbeat    time.Duration
	Lifetime     time.Duration
	Revalidate   time.Duration
	ReconnectMin time.Duration
	ReconnectMax time.Duration
}

func LoadConfig(getenv func(string) string) (Config, error) {
	role := Role(strings.TrimSpace(getenv("ACUITY_RUNTIME_ROLE")))
	switch role {
	case RolePortalAPI, RoleProviderIngress, RoleRealtime, RoleWorker, RoleMigrate:
	default:
		return Config{}, fmt.Errorf("ACUITY_RUNTIME_ROLE must name a supported runtime mode")
	}

	databaseURL, err := required(getenv, "DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	poolMax, err := positiveInt(getenv, "DATABASE_POOL_MAX")
	if err != nil {
		return Config{}, err
	}
	acquireMilliseconds, err := positiveInt(getenv, "DATABASE_ACQUIRE_TIMEOUT_MS")
	if err != nil {
		return Config{}, err
	}
	if poolMax > 64 {
		return Config{}, fmt.Errorf("DATABASE_POOL_MAX must be 64 or less")
	}
	config := Config{
		Role:               role,
		DatabaseURL:        databaseURL,
		PoolMax:            int32(poolMax),
		AcquireTimeout:     time.Duration(acquireMilliseconds) * time.Millisecond,
		ProvisioningInput:  strings.TrimSpace(getenv("PROVISIONING_INPUT")),
		ProvisioningOutput: strings.TrimSpace(getenv("PROVISIONING_OUTPUT")),
	}

	if role == RolePortalAPI || role == RoleProviderIngress || role == RoleRealtime {
		config.HTTPPort, err = positiveInt(getenv, "HTTP_PORT")
		if err != nil {
			return Config{}, err
		}
	}
	if role == RolePortalAPI || role == RoleRealtime {
		if config.BrowserOrigin, err = required(getenv, "BROWSER_ORIGIN"); err != nil {
			return Config{}, err
		}
		if config.JWKSURL, err = required(getenv, "BETTER_AUTH_JWKS_URL"); err != nil {
			return Config{}, err
		}
		if config.AuthIssuer, err = required(getenv, "BETTER_AUTH_ISSUER"); err != nil {
			return Config{}, err
		}
		if config.APIAudience, err = required(getenv, "PORTAL_API_AUDIENCE"); err != nil {
			return Config{}, err
		}
	}
	if role == RoleRealtime {
		if config.Realtime, err = loadRealtimeConfig(getenv); err != nil {
			return Config{}, err
		}
	}
	if role == RoleMigrate && (config.ProvisioningInput == "") != (config.ProvisioningOutput == "") {
		return Config{}, fmt.Errorf("PROVISIONING_INPUT and PROVISIONING_OUTPUT must be set together")
	}
	return config, nil
}

func loadRealtimeConfig(getenv func(string) string) (RealtimeConfig, error) {
	heartbeat, err := positiveInt(getenv, "REALTIME_HEARTBEAT_SECONDS")
	if err != nil {
		return RealtimeConfig{}, err
	}
	lifetime, err := positiveInt(getenv, "REALTIME_STREAM_SECONDS")
	if err != nil {
		return RealtimeConfig{}, err
	}
	revalidate, err := positiveInt(getenv, "REALTIME_REVALIDATE_SECONDS")
	if err != nil {
		return RealtimeConfig{}, err
	}
	reconnectMin, err := positiveInt(getenv, "REALTIME_RECONNECT_MIN_MS")
	if err != nil {
		return RealtimeConfig{}, err
	}
	reconnectMax, err := positiveInt(getenv, "REALTIME_RECONNECT_MAX_SECONDS")
	if err != nil {
		return RealtimeConfig{}, err
	}
	result := RealtimeConfig{
		Heartbeat:    time.Duration(heartbeat) * time.Second,
		Lifetime:     time.Duration(lifetime) * time.Second,
		Revalidate:   time.Duration(revalidate) * time.Second,
		ReconnectMin: time.Duration(reconnectMin) * time.Millisecond,
		ReconnectMax: time.Duration(reconnectMax) * time.Second,
	}
	if result.Lifetime <= result.Heartbeat || result.ReconnectMax < result.ReconnectMin {
		return RealtimeConfig{}, fmt.Errorf("realtime intervals are inconsistent")
	}
	return result, nil
}

func required(getenv func(string) string, name string) (string, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func positiveInt(getenv func(string) string, name string) (int, error) {
	raw, err := required(getenv, name)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}
