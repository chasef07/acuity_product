package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPreflightCachingDoesNotCacheDataOrBypassRequestAuthorization(t *testing.T) {
	server := &Server{config: Config{AllowedOrigins: []string{"https://acuityhealth.io"}}}
	requests := 0
	handler := server.withRequestMetadata(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	for _, scenario := range []struct {
		method, origin, maxAge string
		status                 int
	}{
		{http.MethodOptions, "https://acuityhealth.io", "600", http.StatusNoContent},
		{http.MethodOptions, "https://untrusted.example", "", http.StatusNoContent},
		{http.MethodGet, "https://acuityhealth.io", "", http.StatusUnauthorized},
	} {
		request := httptest.NewRequest(scenario.method, "/v1/access", nil)
		request.Header.Set("Origin", scenario.origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != scenario.status || response.Header().Get("Access-Control-Max-Age") != scenario.maxAge {
			t.Errorf("%s from %s: status=%d, preflight cache duration=%q", scenario.method, scenario.origin, response.Code, response.Header().Get("Access-Control-Max-Age"))
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Error("product response became cacheable")
		}
	}
	if requests != 1 {
		t.Fatalf("protected request handler calls=%d, want 1", requests)
	}
}
