package humancalling

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
