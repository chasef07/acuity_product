package access_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
)

func TestServiceAuthenticatorReturnsTheTenantBoundToEachCredential(t *testing.T) {
	demo := access.ServiceIdentity{
		Subject:       "abita-demo",
		PracticeID:    "00000000-0000-0000-0000-000000000001",
		LocationScope: access.LocationScopeAll,
		Capabilities: []access.ServiceCapability{
			access.ServiceCapabilityCreateTask,
			access.ServiceCapabilityHumanHandoff,
			access.ServiceCapabilityIngestAIInteraction,
		},
	}
	production := access.ServiceIdentity{
		Subject:       "abita-eye-group",
		PracticeID:    "00000000-0000-0000-0000-000000000002",
		LocationScope: access.LocationScopeAll,
		Capabilities: []access.ServiceCapability{
			access.ServiceCapabilityCreateTask,
			access.ServiceCapabilityHumanHandoff,
			access.ServiceCapabilityIngestAIInteraction,
		},
	}
	authenticator, err := access.NewServiceAuthenticator(
		access.ServiceCredential{Token: "demo-token", Identity: demo},
		access.ServiceCredential{Token: "production-token", Identity: production},
	)
	if err != nil {
		t.Fatalf("new service authenticator: %v", err)
	}

	for token, want := range map[string]access.ServiceIdentity{
		"demo-token":       demo,
		"production-token": production,
	} {
		got, err := authenticator.AuthenticateService(context.Background(), token)
		if err != nil {
			t.Fatalf("authenticate %s: %v", want.Subject, err)
		}
		if got.Subject != want.Subject || got.PracticeID != want.PracticeID {
			t.Fatalf("identity = %#v, want %#v", got, want)
		}
	}

	if _, err := authenticator.AuthenticateService(context.Background(), "wrong-token"); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("wrong credential error = %v, want denied", err)
	}
	if !demo.Allows(access.ServiceCapabilityCreateTask) ||
		!demo.Allows(access.ServiceCapabilityHumanHandoff) {
		t.Fatalf("demo capabilities = %#v", demo.Capabilities)
	}
	if !production.Allows(access.ServiceCapabilityCreateTask) ||
		!production.Allows(access.ServiceCapabilityHumanHandoff) ||
		!production.Allows(access.ServiceCapabilityIngestAIInteraction) {
		t.Fatalf("production capabilities = %#v", production.Capabilities)
	}
}

func TestServiceAuthenticatorRejectsInvalidCredentialSets(t *testing.T) {
	identity := access.ServiceIdentity{
		Subject:       "abita-demo",
		PracticeID:    "00000000-0000-0000-0000-000000000001",
		LocationScope: access.LocationScopeAll,
		Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
	}
	valid := access.ServiceCredential{Token: "valid-token", Identity: identity}
	for name, credentials := range map[string][2]access.ServiceCredential{
		"empty first token":  {{Identity: identity}, valid},
		"empty second token": {valid, {Identity: identity}},
		"duplicate token": {
			{Token: "same-token", Identity: identity},
			{Token: "same-token", Identity: identity},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := access.NewServiceAuthenticator(credentials[0], credentials[1]); err == nil {
				t.Fatal("expected invalid credential set to fail closed")
			}
		})
	}
}
