package humancalling_test

import (
	"context"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
)

func TestStaticServiceAuthenticatorReturnsOnlyItsConfiguredScope(t *testing.T) {
	authenticator, err := humancalling.NewServiceAuthenticator(
		"synthetic-token",
		humancalling.ServiceIdentity{
			Subject:    "abita-synthetic",
			PracticeID: "00000000-0000-0000-0000-000000000001",
		},
	)
	if err != nil {
		t.Fatalf("new service authenticator: %v", err)
	}
	if _, err := authenticator.AuthenticateService(context.Background(), "wrong-token"); err == nil {
		t.Fatal("wrong token authenticated")
	}
	identity, err := authenticator.AuthenticateService(context.Background(), "synthetic-token")
	if err != nil {
		t.Fatalf("authenticate configured service: %v", err)
	}
	if identity.Subject != "abita-synthetic" ||
		identity.PracticeID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("identity = %#v", identity)
	}
}
