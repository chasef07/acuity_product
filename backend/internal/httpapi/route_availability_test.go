package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/observability"
)

func TestDiagnosticRouteCoverage(t *testing.T) {
	for _, test := range []struct {
		role, method, path string
		status             int
		stage              string
	}{
		{"portal-api", "POST", "/v1/message-threads/query", 503, "dependency"},
		{"portal-api", "POST", "/v1/ai/interactions/outcomes/query", 503, "dependency"},
		{"portal-api", "PUT", "/v1/calling/readiness", 503, "dependency"},
		{"realtime", "GET", "/v1/events", 503, "dependency"},
		{"portal-api", "POST", "/v1/message-threads/query", 401, "authentication"},
		{"portal-api", "PUT", "/v1/calling/readiness", 403, "authorization"},
		{"portal-api", "POST", "/v1/ai/interactions/outcomes/query", 200, "none"},
	} {
		t.Run(test.method+test.path+test.stage, func(t *testing.T) {
			var output bytes.Buffer
			server := diagnosticTestServer(&output, test.role)
			server.withRequestMetadata(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(test.status) })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(test.method, test.path+"?phone=synthetic-secret", nil))
			var entry map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
				t.Fatal(err)
			}
			if entry["metric"] != "acuity_backend_route_availability" || entry["route"] != test.path || entry["failure_stage"] != test.stage {
				t.Fatalf("metric = %#v", entry)
			}
			if bytes.Contains(output.Bytes(), []byte("synthetic-secret")) {
				t.Fatal("request data leaked")
			}
		})
	}
	for _, test := range []struct{ role, method, path string }{
		{"portal-api", "GET", "/v1/calling/readiness"}, {"portal-api", "GET", "/v1/events"},
		{"realtime", "POST", "/v1/message-threads/query"}, {"portal-api", "POST", "/v1/message-threads/arbitrary"},
	} {
		if diagnosticRoute(test.role, test.method, test.path) != "" {
			t.Fatalf("unexpected route: %#v", test)
		}
	}
}

func TestDiagnosticSSERecordsFirstReadyBeforeStreamEnds(t *testing.T) {
	var output bytes.Buffer
	server := diagnosticTestServer(&output, "realtime")
	handler := server.withRequestMetadata(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
		observability.StreamReady(r.Context())
		first := output.String()
		if first == "" {
			t.Fatal("first-ready metric waits for stream to end")
		}
		observability.StreamReady(r.Context())
		if output.String() != first {
			t.Fatal("duplicate ready counted twice")
		}
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/events", nil))
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["outcome"] != "available" || entry["failure_stage"] != "none" {
		t.Fatalf("metric = %#v", entry)
	}
}

func TestDiagnosticSSEHTTP200WithoutReadyIsUnavailable(t *testing.T) {
	var output bytes.Buffer
	server := diagnosticTestServer(&output, "realtime")
	server.withRequestMetadata(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/events", nil))
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["outcome"] != "unavailable" || entry["failure_stage"] != "handler" {
		t.Fatalf("metric = %#v", entry)
	}
}

func diagnosticTestServer(output *bytes.Buffer, role string) *Server {
	return &Server{role: role, observer: observability.NewLogger(observability.RuntimeRole(role), "diagnostic-test", slog.New(slog.NewJSONHandler(output, nil)))}
}

func TestDiagnosticRoutePanicIsUnavailableAndPropagates(t *testing.T) {
	var output bytes.Buffer
	server := diagnosticTestServer(&output, "portal-api")
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		server.withRequestMetadata(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("synthetic") })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/v1/calling/readiness", nil))
	}()
	if recovered == nil {
		t.Fatal("panic did not propagate")
	}
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["outcome"] != "unavailable" || entry["failure_stage"] != "handler" {
		t.Fatalf("metric = %#v", entry)
	}
}

func TestDiagnosticSSEFlushFailureRemainsUnavailable(t *testing.T) {
	var output bytes.Buffer
	server := diagnosticTestServer(&output, "realtime")
	response := &failedFlushWriter{ResponseRecorder: httptest.NewRecorder()}
	server.withRequestMetadata(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if err := http.NewResponseController(w).Flush(); err == nil {
			t.Fatal("flush failure was hidden")
		}
	})).ServeHTTP(response, httptest.NewRequest("GET", "/v1/events", nil))
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["outcome"] != "unavailable" {
		t.Fatalf("metric = %#v", entry)
	}
}

type failedFlushWriter struct{ *httptest.ResponseRecorder }

func (*failedFlushWriter) FlushError() error { return errors.New("synthetic flush failure") }
