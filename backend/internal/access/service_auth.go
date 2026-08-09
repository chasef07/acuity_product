package access

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type ServiceCredential struct {
	Token    string
	Identity ServiceIdentity
}

type serviceCredential struct {
	digest   [sha256.Size]byte
	identity ServiceIdentity
}

type ServiceAuthenticator struct {
	credentials []serviceCredential
}

func NewServiceAuthenticator(credentials ...ServiceCredential) (*ServiceAuthenticator, error) {
	if len(credentials) == 0 {
		return nil, fmt.Errorf("at least one service credential is required")
	}
	validated := make([]serviceCredential, 0, len(credentials))
	seen := make(map[[sha256.Size]byte]struct{}, len(credentials))
	for _, credential := range credentials {
		identity := credential.Identity
		if strings.TrimSpace(credential.Token) == "" ||
			strings.TrimSpace(identity.Subject) == "" {
			return nil, fmt.Errorf("service credential and subject are required")
		}
		if _, err := uuid.Parse(identity.PracticeID); err != nil {
			return nil, fmt.Errorf("service Practice ID must be a UUID")
		}
		if identity.LocationScope != LocationScopeAll ||
			len(identity.Capabilities) == 0 {
			return nil, fmt.Errorf("service scope and capabilities are required")
		}
		digest := sha256.Sum256([]byte(credential.Token))
		if _, duplicate := seen[digest]; duplicate {
			return nil, fmt.Errorf("service credentials must be unique")
		}
		seen[digest] = struct{}{}
		validated = append(validated, serviceCredential{digest: digest, identity: identity})
	}
	return &ServiceAuthenticator{credentials: validated}, nil
}

func (authenticator *ServiceAuthenticator) AuthenticateService(
	_ context.Context,
	token string,
) (ServiceIdentity, error) {
	digest := sha256.Sum256([]byte(token))
	match := -1
	for index, credential := range authenticator.credentials {
		if subtle.ConstantTimeCompare(credential.digest[:], digest[:]) == 1 {
			match = index
		}
	}
	if match < 0 {
		return ServiceIdentity{}, ErrDenied
	}
	return authenticator.credentials[match].identity, nil
}
