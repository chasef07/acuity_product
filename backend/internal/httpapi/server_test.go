package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestMetadataAllowsEachConfiguredBrowserOrigin(t *testing.T) {
	server := &Server{config: Config{AllowedOrigins: []string{
		"https://acuityhealth.io",
		"https://acuity-web.example.run.app",
	}}}
	handler := server.withRequestMetadata(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, test := range []struct {
		origin string
		want   string
	}{
		{origin: "https://acuityhealth.io", want: "https://acuityhealth.io"},
		{origin: "https://acuity-web.example.run.app", want: "https://acuity-web.example.run.app"},
		{origin: "https://untrusted.example", want: ""},
	} {
		request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		request.Header.Set("Origin", test.origin)
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		if got := response.Header().Get("Access-Control-Allow-Origin"); got != test.want {
			t.Errorf("origin %q allowed as %q, want %q", test.origin, got, test.want)
		}
	}
}
