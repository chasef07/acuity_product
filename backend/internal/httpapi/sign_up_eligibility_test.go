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

func TestSignUpEligibilityLimitsEachClientWithinTheWindow(t *testing.T) {
	var limiter signUpEligibilityLimiter
	start := time.Date(2026, time.October, 6, 12, 0, 0, 0, time.UTC)
	for range signUpEligibilityLimit {
		if allowed, _ := limiter.allow("192.0.2.10", start); !allowed {
			t.Fatal("client limited before reaching the window budget")
		}
	}
	allowed, retryAfter := limiter.allow("192.0.2.10", start.Add(15*time.Second))
	if allowed || retryAfter != 45*time.Second {
		t.Fatalf("over-budget client allowed=%t retryAfter=%s", allowed, retryAfter)
	}
	if allowed, _ := limiter.allow("192.0.2.11", start.Add(15*time.Second)); !allowed {
		t.Fatal("another client shared the limited budget")
	}
	if allowed, _ := limiter.allow("192.0.2.10", start.Add(signUpEligibilityWindow)); !allowed {
		t.Fatal("client remained limited after the window elapsed")
	}
}

func TestSignUpEligibilityClientUsesTheProxyAppendedAddress(t *testing.T) {
	for name, scenario := range map[string]struct {
		forwarded []string
		want      string
	}{
		"direct":           {nil, "192.0.2.1"},
		"proxied":          {[]string{"198.51.100.7"}, "198.51.100.7"},
		"spoofed prefix":   {[]string{"203.0.113.9, 198.51.100.7"}, "198.51.100.7"},
		"repeated headers": {[]string{"203.0.113.9", "198.51.100.7"}, "198.51.100.7"},
	} {
		request := httptest.NewRequest(http.MethodPost, "/v1/access/sign-up-eligibility", nil)
		request.RemoteAddr = "192.0.2.1:41000"
		for _, value := range scenario.forwarded {
			request.Header.Add("X-Forwarded-For", value)
		}
		if got := signUpEligibilityClient(request); got != scenario.want {
			t.Errorf("%s client = %q, want %q", name, got, scenario.want)
		}
	}
}

func TestSignUpEligibilityRejectsOverBudgetClientsBeforeReadingAccess(t *testing.T) {
	server := &Server{role: "portal-api", config: Config{RequestTimeout: time.Second}}
	now := time.Now()
	for range signUpEligibilityLimit {
		server.signUpLimiter.allow("192.0.2.1", now)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/access/sign-up-eligibility", strings.NewReader(`{"email":"staff@example.test"}`))
	request.RemoteAddr = "192.0.2.1:41000"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.withRequestMetadata(api.Handler(server)).ServeHTTP(response, request)
	var envelope api.ErrorEnvelope
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" ||
		json.Unmarshal(response.Body.Bytes(), &envelope) != nil || !envelope.Error.Retryable {
		t.Fatalf("over-budget status = %d, retry-after = %q, body = %s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
	}
}

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
