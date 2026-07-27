package humancalling_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
)

func TestTelnyxAdapterUsesStableOpaqueCommandsAndNoTranscription(t *testing.T) {
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Header.Get("Authorization") != "Bearer synthetic-key" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "invalid", http.StatusBadRequest)
			return
		}
		body["_path"] = request.URL.Path
		requests <- body
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/v2/calls" {
			_, _ = writer.Write([]byte(
				`{"data":{"call_control_id":"staff-control","call_leg_id":"staff-leg"}}`,
			))
			return
		}
		_, _ = writer.Write([]byte(`{"data":{"result":"ok"}}`))
	}))
	defer server.Close()

	adapter, err := humancalling.NewTelnyxAdapter(humancalling.TelnyxConfig{
		APIKey:        "synthetic-key",
		BaseURL:       server.URL + "/v2",
		HTTPClient:    server.Client(),
		CallControlID: "call-control-app",
		FromNumber:    "+15555550199",
		RingbackURL:   "https://media.example.test/ringback.wav",
	})
	if err != nil {
		t.Fatalf("new Telnyx adapter: %v", err)
	}
	dial, err := adapter.Execute(context.Background(), humancalling.ProviderCommand{
		ID:       "command-dial-1",
		Action:   humancalling.CommandDialStaff,
		TargetID: "caller-control",
		Payload: map[string]any{
			"to":                    "sip:synthetic-user@sip.telnyx.com",
			"link_to":               "caller-control",
			"bridge_intent":         true,
			"bridge_on_answer":      true,
			"prevent_double_bridge": true,
			"client_state":          "opaque-state",
		},
	})
	if err != nil ||
		dial.CallControlID != "staff-control" ||
		dial.CallLegID != "staff-leg" {
		t.Fatalf("Dial result = %#v, err = %v", dial, err)
	}
	if _, err := adapter.Execute(context.Background(), humancalling.ProviderCommand{
		ID:       "command-recording-1",
		Action:   humancalling.CommandStartRecording,
		TargetID: "caller-control",
		Payload: map[string]any{
			"format":          "wav",
			"channels":        "dual",
			"recording_track": "both",
			"transcription":   false,
			"client_state":    "opaque-recording-state",
		},
	}); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	dialRequest := <-requests
	if dialRequest["_path"] != "/v2/calls" ||
		dialRequest["command_id"] != "command-dial-1" ||
		dialRequest["connection_id"] != "call-control-app" ||
		dialRequest["from"] != "+15555550199" ||
		dialRequest["bridge_on_answer"] != true ||
		dialRequest["prevent_double_bridge"] != true {
		t.Fatalf("Telnyx Dial request = %#v", dialRequest)
	}
	recordingRequest := <-requests
	if recordingRequest["_path"] != "/v2/calls/caller-control/actions/record_start" ||
		recordingRequest["command_id"] != "command-recording-1" ||
		recordingRequest["channels"] != "dual" ||
		recordingRequest["transcription"] != false {
		t.Fatalf("Telnyx recording request = %#v", recordingRequest)
	}
}

func TestTelnyxAdapterClassifiesUncertainTransportWithoutBlindRetry(t *testing.T) {
	adapter, err := humancalling.NewTelnyxAdapter(humancalling.TelnyxConfig{
		APIKey:     "synthetic-key",
		BaseURL:    "http://127.0.0.1:1/v2",
		HTTPClient: &http.Client{Timeout: 20 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Execute(context.Background(), humancalling.ProviderCommand{
		ID:       "uncertain-command",
		Action:   humancalling.CommandHangup,
		TargetID: "opaque-control",
		Payload:  map[string]any{},
	})
	if err == nil ||
		!strings.Contains(err.Error(), humancalling.ErrAmbiguousEffect.Error()) {
		t.Fatalf("uncertain provider error = %v", err)
	}
}

func TestTelnyxAdapterReadsExactCallLivenessForHangupReconciliation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v2/calls/alive-control":
			_, _ = writer.Write([]byte(`{"data":{"is_alive":true}}`))
		case "/v2/calls/ended-control":
			_, _ = writer.Write([]byte(`{"data":{"is_alive":false}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	adapter, err := humancalling.NewTelnyxAdapter(humancalling.TelnyxConfig{
		APIKey:     "synthetic-key",
		BaseURL:    server.URL + "/v2",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	alive, err := adapter.IsCallAlive(context.Background(), "alive-control")
	if err != nil || !alive {
		t.Fatalf("alive Call state = %t, err = %v", alive, err)
	}
	alive, err = adapter.IsCallAlive(context.Background(), "ended-control")
	if err != nil || alive {
		t.Fatalf("ended Call state = %t, err = %v", alive, err)
	}
	alive, err = adapter.IsCallAlive(context.Background(), "missing-control")
	if err != nil || alive {
		t.Fatalf("absent Call state = %t, err = %v", alive, err)
	}
}
