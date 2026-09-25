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
