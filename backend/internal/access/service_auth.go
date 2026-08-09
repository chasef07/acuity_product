package access

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type ServiceAuthenticator struct {
	token    []byte
	identity ServiceIdentity
}

func NewServiceAuthenticator(
	token string,
	identity ServiceIdentity,
) (*ServiceAuthenticator, error) {
	if strings.TrimSpace(token) == "" ||
		strings.TrimSpace(identity.Subject) == "" {
		return nil, fmt.Errorf("service credential and subject are required")
	}
	if _, err := uuid.Parse(identity.PracticeID); err != nil {
		return nil, fmt.Errorf("service Practice ID must be a UUID")
	}
	practiceIDs := map[string]struct{}{identity.PracticeID: {}}
	for _, practiceID := range identity.AdditionalHandoffPracticeIDs {
		if _, err := uuid.Parse(practiceID); err != nil {
			return nil, fmt.Errorf("additional service Practice ID must be a UUID")
		}
		if _, duplicate := practiceIDs[practiceID]; duplicate {
			return nil, fmt.Errorf("service Practice IDs must be unique")
		}
		practiceIDs[practiceID] = struct{}{}
	}
	if identity.LocationScope != LocationScopeAll ||
		len(identity.Capabilities) == 0 {
		return nil, fmt.Errorf("service scope and capabilities are required")
	}
	return &ServiceAuthenticator{
		token:    []byte(token),
		identity: identity,
	}, nil
}

func (authenticator *ServiceAuthenticator) AuthenticateService(
	_ context.Context,
	token string,
) (ServiceIdentity, error) {
	if subtle.ConstantTimeCompare(authenticator.token, []byte(token)) != 1 {
		return ServiceIdentity{}, ErrDenied
	}
	return authenticator.identity, nil
}
