package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/authn"
)

type failingAuthenticator struct{ err error }

func (adapter failingAuthenticator) Authenticate(context.Context, string) (access.Identity, error) {
	return access.Identity{}, adapter.err
}

func TestSigningKeyOutageIsRetryableWhileInvalidCredentialsAreUnauthenticated(t *testing.T) {
	for name, scenario := range map[string]struct {
		err       error
		status    int
		retryable bool
	}{
		"keys unavailable":   {authn.ErrKeysUnavailable, http.StatusServiceUnavailable, true},
		"invalid credential": {authn.ErrInvalidCredential, http.StatusUnauthorized, false},
		"wrapped outage":     {errors.Join(errors.New("fetch"), authn.ErrKeysUnavailable), http.StatusServiceUnavailable, true},
	} {
		server := &Server{role: "portal-api", config: Config{RequestTimeout: time.Second}, authenticator: failingAuthenticator{scenario.err}}
		request := httptest.NewRequest(http.MethodGet, "/v1/access", nil)
		request.Header.Set("Authorization", "Bearer synthetic-token")
		response := httptest.NewRecorder()
		server.withRequestMetadata(api.Handler(server)).ServeHTTP(response, request)
		var envelope api.ErrorEnvelope
		if response.Code != scenario.status || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Error.Retryable != scenario.retryable {
			t.Errorf("%s status = %d, body = %s", name, response.Code, response.Body.String())
		}
	}
}

type staticServiceAuthenticator access.ServiceIdentity

func (identity staticServiceAuthenticator) AuthenticateService(context.Context, string) (access.ServiceIdentity, error) {
	return access.ServiceIdentity(identity), nil
}

func TestKnowledgeSearchDeniesServicesWithTheSharedAccessCode(t *testing.T) {
	server := &Server{role: "portal-api", config: Config{RequestTimeout: time.Second}, serviceAuth: staticServiceAuthenticator{Subject: "synthetic-service"}}
	request := httptest.NewRequest(http.MethodPost, "/v1/agent/knowledge/search", strings.NewReader(`{"query":"hours"}`))
	request.Header.Set("Authorization", "Bearer synthetic-service-token")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Office-Key", "synthetic-office")
	response := httptest.NewRecorder()
	server.withRequestMetadata(api.Handler(server)).ServeHTTP(response, request)
	var envelope api.ErrorEnvelope
	if response.Code != http.StatusForbidden || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Error.Code != "ACCESS_DENIED" {
		t.Fatalf("service without knowledge capability status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestLocationKeyConflictIsAConflict(t *testing.T) {
	server := &Server{}
	response := httptest.NewRecorder()
	server.writeAccessError(response, httptest.NewRequest(http.MethodPost, "/", nil), access.ErrLocationConflict)
	var envelope api.ErrorEnvelope
	if response.Code != http.StatusConflict || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Error.Code != "LOCATION_CONFLICT" || envelope.Error.Retryable {
		t.Fatalf("Location conflict status = %d, body = %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	server.writeAccessError(response, httptest.NewRequest(http.MethodPost, "/", nil), access.ErrInvalidInput)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid Location status = %d", response.Code)
	}
}
