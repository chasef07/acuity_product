package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
)

func TestAnalyticsBudgetRejectsOverlapAndReleasesOnFinish(t *testing.T) {
	server := &Server{config: Config{RequestTimeout: 10 * time.Second}}
	request := httptest.NewRequest("POST", "/v1/analytics/bookings/query", nil)
	ctx, finish, ok := server.beginAnalytics(httptest.NewRecorder(), request)
	if !ok {
		t.Fatal("first analytics request rejected")
	}
	defer finish()
	deadline, present := ctx.Deadline()
	if !present || time.Until(deadline) > 2*time.Second {
		t.Fatal("analytics must have its own short deadline")
	}
	busy := httptest.NewRecorder()
	if _, _, ok := server.beginAnalytics(busy, request); ok {
		t.Fatal("overlapping analytics request acquired a permit")
	}
	if busy.Code != 429 || busy.Header().Get("Retry-After") != "1" {
		t.Fatalf("busy response: %d %v", busy.Code, busy.Header())
	}
	// Other HTTP work is unaffected while analytics holds its permit.
	live := httptest.NewRecorder()
	server.GetLiveness(live, httptest.NewRequest("GET", "/health/live", nil))
	if live.Code != 200 {
		t.Fatalf("analytics blocked liveness: %d", live.Code)
	}
	finish()
	if ctx.Err() == nil {
		t.Fatal("finishing analytics did not cancel work")
	}
	_, nextFinish, ok := server.beginAnalytics(httptest.NewRecorder(), request)
	if !ok {
		t.Fatal("finished request retained its permit")
	}
	nextFinish()
}

func TestLegacyOperatorAnalyticsUsesSharedAdmission(t *testing.T) {
	server := &Server{role: "portal-api", config: Config{RequestTimeout: 10 * time.Second}, authenticator: analyticsBudgetAuthenticator{}}
	_, finish, ok := server.beginAnalytics(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/analytics/bookings/query", nil))
	if !ok {
		t.Fatal("could not occupy shared analytics budget")
	}
	defer finish()
	request := httptest.NewRequest("POST", "/v1/operator/ai-analytics/query", strings.NewReader(`{"practiceId":"11111111-1111-1111-1111-111111111111","range":"24h"}`))
	request.Header.Set("Authorization", "Bearer synthetic")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	// There is intentionally no database/module: rejected work must never enter
	// the query path or acquire a connection.
	server.QueryOperatorAIAnalytics(response, request)
	if response.Code != 429 || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("legacy analytics bypassed shared budget: %d %s", response.Code, response.Body.String())
	}
}

type analyticsBudgetAuthenticator struct{}

func (analyticsBudgetAuthenticator) Authenticate(context.Context, string) (access.Identity, error) {
	return access.Identity{Subject: "synthetic-operator"}, nil
}
