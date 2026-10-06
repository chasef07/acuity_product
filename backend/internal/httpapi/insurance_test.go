package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/insurance"
)

type insuranceAuthenticator struct{}

func (insuranceAuthenticator) Authenticate(_ context.Context, token string) (access.Identity, error) {
	if token != "staff-token" {
		return access.Identity{}, errors.New("invalid credential")
	}
	return access.Identity{Subject: "staff", Email: "staff@abita.test", EmailVerified: true}, nil
}

type insuranceRoutes string

func (route insuranceRoutes) ReadLocationAbitaOfficeKey(context.Context, access.Identity, string, string) (string, error) {
	return string(route), nil
}

func insuranceHandler(t *testing.T, module *insurance.Module) http.Handler {
	t.Helper()
	server := &Server{role: "portal-api", config: Config{RequestTimeout: time.Second}, authenticator: insuranceAuthenticator{}, insurance: module}
	return server.withRequestMetadata(api.Handler(server))
}

func insuranceModule(t *testing.T, routes insuranceRoutes, status int, response string) *insurance.Module {
	t.Helper()
	middleware := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(middleware.Close)
	rules, err := insurance.NewMiddlewareClient(middleware.URL, "synthetic-middleware-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	module, err := insurance.New(routes, rules)
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func postInsurance(handler http.Handler, path, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

const plansBody = `{"practiceId":"00000000-0000-0000-0000-000000000010","locationId":"00000000-0000-0000-0000-000000000011","coverage":"medical"}`

func TestInsurancePlansAreUnavailableWithoutMiddlewareConfiguration(t *testing.T) {
	handler := insuranceHandler(t, nil)
	if response := postInsurance(handler, "/v1/insurance/plans/query", "", plansBody); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	if response := postInsurance(handler, "/v1/insurance/plans/query", "staff-token", plansBody); response.Code != http.StatusServiceUnavailable ||
		!strings.Contains(response.Body.String(), "Insurance rules aren't available right now.") {
		t.Fatalf("unconfigured status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestInsurancePlansReturnMiddlewareRulesOrUnavailable(t *testing.T) {
	plans := postInsurance(insuranceHandler(t, insuranceModule(t, "hollywood", http.StatusOK, `{"officeId":"hollywood","coverage":"medical","plans":[{"planId":"synthetic-hmo-medical","label":"Synthetic HMO","outcome":"accepted","requirements":["prior_authorization"]}]}`)), "/v1/insurance/plans/query", "staff-token", plansBody)
	var page api.InsurancePlansPage
	if plans.Code != http.StatusOK || json.Unmarshal(plans.Body.Bytes(), &page) != nil || page.LocationId.String() != "00000000-0000-0000-0000-000000000011" ||
		page.Coverage != api.Medical || len(page.Plans) != 1 || page.Plans[0].Requirements[0] != api.PriorAuthorization || page.Plans[0].Names == nil || page.Plans[0].AcceptedAt == nil {
		t.Fatalf("plans status = %d, body = %s", plans.Code, plans.Body.String())
	}

	failing := insuranceHandler(t, insuranceModule(t, "hollywood", http.StatusBadGateway, `{}`))
	var envelope api.ErrorEnvelope
	if response := postInsurance(failing, "/v1/insurance/plans/query", "staff-token", plansBody); response.Code != http.StatusServiceUnavailable ||
		json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Error.Message != "Insurance rules aren't available right now." || !envelope.Error.Retryable {
		t.Fatalf("middleware failure status = %d, body = %s", response.Code, response.Body.String())
	}

	rejected := insuranceHandler(t, insuranceModule(t, "dev", http.StatusBadRequest, `Unknown office`))
	if response := postInsurance(rejected, "/v1/insurance/plans/query", "staff-token", plansBody); response.Code != http.StatusServiceUnavailable ||
		json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Error.Message != "Insurance rules aren't available for this Location." || envelope.Error.Retryable {
		t.Fatalf("rejected office status = %d, body = %s", response.Code, response.Body.String())
	}

	if response := postInsurance(rejected, "/v1/insurance/plans/query", "staff-token", strings.Replace(plansBody, "medical", "dental", 1)); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown coverage status = %d", response.Code)
	}
}

func TestCancelledInsuranceRequestsAreNotLoggedAsOutages(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	handler := insuranceHandler(t, insuranceModule(t, "hollywood", http.StatusBadGateway, `{}`))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/insurance/plans/query", strings.NewReader(plansBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer staff-token")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if logs.Len() != 0 {
		t.Fatalf("cancelled request logged: %s", logs.String())
	}

	postInsurance(handler, "/v1/insurance/plans/query", "staff-token", plansBody)
	if !strings.Contains(logs.String(), "insurance_plans_failed") || strings.Contains(logs.String(), "synthetic-middleware-secret") {
		t.Fatalf("outage log = %s", logs.String())
	}
}

func TestLocationKnowledgeIsUnavailableWithoutTheKnowledgeModule(t *testing.T) {
	response := postInsurance(insuranceHandler(t, nil), "/v1/knowledge/query", "staff-token", `{"practiceId":"00000000-0000-0000-0000-000000000010","locationId":"00000000-0000-0000-0000-000000000011"}`)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "Knowledge isn't available right now.") {
		t.Fatalf("unconfigured knowledge status = %d, body = %s", response.Code, response.Body.String())
	}
}
