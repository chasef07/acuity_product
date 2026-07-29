package app

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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
	Service            ServiceConfig
	HumanCalling       HumanCallingConfig
}

type ServiceConfig struct {
	Token      string
	Subject    string
	PracticeID string
}

type HumanCallingConfig struct {
	HandoffSIPDomain       string
	StaffSIPDomain         string
	HandoffTokenKey        []byte
	WebhookPublicKey       []byte
	TelnyxAPIKey           string
	TelnyxAPIBaseURL       string
	CallControlID          string
	CredentialConnectionID string
	FromNumber             string
	RingbackURL            string
	RecordingBucket        string
	OfferDuration          time.Duration
	ConnectionTimeout      time.Duration
	LeaseDuration          time.Duration
	ReadinessGrace         time.Duration
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
	if role == RolePortalAPI {
		if config.HumanCalling, err = loadHandoffConfig(getenv); err != nil {
			return Config{}, err
		}
		if config.Service, err = loadServiceConfig(getenv); err != nil {
			return Config{}, err
		}
	}
	if role == RolePortalAPI || role == RoleWorker {
		if err := loadTelnyxCommandConfig(getenv, &config.HumanCalling); err != nil {
			return Config{}, err
		}
	}
	if role == RoleProviderIngress {
		if config.HumanCalling.WebhookPublicKey, err = requiredBase64Key(
			getenv,
			"TELNYX_WEBHOOK_PUBLIC_KEY",
			32,
		); err != nil {
			return Config{}, err
		}
	}
	if role == RoleMigrate && (config.ProvisioningInput == "") != (config.ProvisioningOutput == "") {
		return Config{}, fmt.Errorf("PROVISIONING_INPUT and PROVISIONING_OUTPUT must be set together")
	}
	return config, nil
}

func loadHandoffConfig(getenv func(string) string) (HumanCallingConfig, error) {
	var result HumanCallingConfig
	var err error
	if result.HandoffSIPDomain, err = required(getenv, "HUMAN_CALLING_SIP_DOMAIN"); err != nil {
		return HumanCallingConfig{}, err
	}
	if result.StaffSIPDomain, err = required(
		getenv,
		"HUMAN_CALLING_STAFF_SIP_DOMAIN",
	); err != nil {
		return HumanCallingConfig{}, err
	}
	if result.HandoffTokenKey, err = requiredBase64Key(
		getenv,
		"HUMAN_CALLING_HANDOFF_TOKEN_KEY",
		32,
	); err != nil {
		return HumanCallingConfig{}, err
	}
	return result, nil
}

func loadServiceConfig(getenv func(string) string) (ServiceConfig, error) {
	var result ServiceConfig
	var err error
	if result.Token, err = required(getenv, "HANDOFF_SERVICE_TOKEN"); err != nil {
		return ServiceConfig{}, err
	}
	if result.Subject, err = required(getenv, "HANDOFF_SERVICE_SUBJECT"); err != nil {
		return ServiceConfig{}, err
	}
	if result.PracticeID, err = required(
		getenv,
		"HANDOFF_SERVICE_PRACTICE_ID",
	); err != nil {
		return ServiceConfig{}, err
	}
	if _, err := uuid.Parse(result.PracticeID); err != nil {
		return ServiceConfig{}, fmt.Errorf("HANDOFF_SERVICE_PRACTICE_ID must be a UUID")
	}
	return result, nil
}

func loadTelnyxCommandConfig(
	getenv func(string) string,
	result *HumanCallingConfig,
) error {
	values := []struct {
		name   string
		target *string
	}{
		{"TELNYX_API_KEY", &result.TelnyxAPIKey},
		{"TELNYX_CALL_CONTROL_ID", &result.CallControlID},
		{"TELNYX_CREDENTIAL_CONNECTION_ID", &result.CredentialConnectionID},
		{"TELNYX_FROM_NUMBER", &result.FromNumber},
		{"TELNYX_RINGBACK_URL", &result.RingbackURL},
		{"TELNYX_RECORDING_BUCKET", &result.RecordingBucket},
	}
	for _, value := range values {
		loaded, err := required(getenv, value.name)
		if err != nil {
			return err
		}
		*value.target = loaded
	}
	result.TelnyxAPIBaseURL = strings.TrimSpace(getenv("TELNYX_API_BASE_URL"))
	timings := []struct {
		name   string
		target *time.Duration
	}{
		{"HUMAN_CALLING_OFFER_SECONDS", &result.OfferDuration},
		{"HUMAN_CALLING_CONNECTION_TIMEOUT_SECONDS", &result.ConnectionTimeout},
		{"HUMAN_CALLING_LEASE_SECONDS", &result.LeaseDuration},
		{"HUMAN_CALLING_READINESS_GRACE_SECONDS", &result.ReadinessGrace},
	}
	for _, timing := range timings {
		seconds, err := positiveInt(getenv, timing.name)
		if err != nil {
			return err
		}
		*timing.target = time.Duration(seconds) * time.Second
	}
	if result.ReadinessGrace >= result.LeaseDuration {
		return fmt.Errorf("calling readiness grace must be shorter than the lease")
	}
	return nil
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

func requiredBase64Key(
	getenv func(string) string,
	name string,
	length int,
) ([]byte, error) {
	raw, err := required(getenv, name)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(decoded) != length {
		return nil, fmt.Errorf("%s must be base64 encoding of exactly %d bytes", name, length)
	}
	return decoded, nil
}
