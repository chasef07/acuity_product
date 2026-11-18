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

func diagnosticTestServer(output *bytes.Buffer, role string) *Server {
	return &Server{role: role, observer: observability.NewLogger(observability.RuntimeRole(role), "diagnostic-test", slog.New(slog.NewJSONHandler(output, nil)))}
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
	if entry["outcome"] != "unavailable" || entry["failure_stage"] != "handler" {
		t.Fatalf("metric = %#v", entry)
	}
}

func TestDiagnosticRoutesCoverBothTaskReads(t *testing.T) {
	for _, path := range []string{"/v1/tasks/query", "/v1/tasks/patients/query"} {
		var output bytes.Buffer
		server := diagnosticTestServer(&output, "portal-api")
		server.withRequestMetadata(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", path, nil))
		var entry map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
			t.Fatalf("%s recorded no single metric: %v in %q", path, err, output.String())
		}
		if entry["metric"] != "acuity_backend_route_availability" || entry["route"] != path || entry["outcome"] != "available" {
			t.Fatalf("%s metric = %#v", path, entry)
		}
	}
}

type failedFlushWriter struct{ *httptest.ResponseRecorder }

func (*failedFlushWriter) FlushError() error { return errors.New("synthetic flush failure") }
