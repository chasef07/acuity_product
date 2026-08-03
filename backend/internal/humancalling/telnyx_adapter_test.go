package humancalling_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
)

func TestTelnyxAdapterRefreshesVoicemailRecordingAndStreamsRange(t *testing.T) {
	audio := []byte("synthetic-mp3")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/v2/recordings/provider-recording-1":
			if request.Header.Get("Authorization") != "Bearer synthetic-key" {
				http.Error(writer, "unauthorized", http.StatusUnauthorized)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(
				writer,
				`{"data":{"download_urls":{"mp3":%q}}}`,
				server.URL+"/recording.mp3",
			)
		case "/recording.mp3":
			if request.Header.Get("Authorization") != "" {
				http.Error(writer, "credential leaked", http.StatusBadRequest)
				return
			}
			if request.Header.Get("Range") != "bytes=0-3" {
				http.Error(writer, "range missing", http.StatusBadRequest)
				return
			}
			writer.Header().Set("Accept-Ranges", "bytes")
			writer.Header().Set("Content-Range", "bytes 0-3/13")
			writer.Header().Set("Content-Length", "4")
			writer.Header().Set("Content-Type", "audio/mpeg")
			writer.WriteHeader(http.StatusPartialContent)
			_, _ = writer.Write(audio[:4])
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
	content, err := adapter.OpenVoicemailRecording(
		context.Background(),
		"provider-recording-1",
		"bytes=0-3",
	)
	if err != nil {
		t.Fatalf("open voicemail recording: %v", err)
	}
	defer content.Body.Close()
	body, err := io.ReadAll(content.Body)
	if err != nil {
		t.Fatalf("read voicemail recording: %v", err)
	}
	if content.StatusCode != http.StatusPartialContent ||
		content.ContentType != "audio/mpeg" ||
		content.ContentLength != "4" ||
		content.ContentRange != "bytes 0-3/13" ||
		string(body) != "synt" {
		t.Fatalf("voicemail recording = %#v body=%q", content, body)
	}
}

func TestTelnyxAdapterClassifiesVoicemailPlaybackFailures(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		expiredURL bool
		timeout    bool
		want       humancalling.VoicemailUnavailableReason
		wantRetry  string
	}{
		{name: "recording not found", status: http.StatusNotFound, want: humancalling.VoicemailRecordingNotFound},
		{name: "provider unauthorized", status: http.StatusUnauthorized, want: humancalling.VoicemailProviderAuth},
		{name: "provider forbidden", status: http.StatusForbidden, want: humancalling.VoicemailProviderAuth},
		{name: "provider rate limited", status: http.StatusTooManyRequests, want: humancalling.VoicemailProviderRateLimited, wantRetry: "7"},
		{name: "provider unavailable", status: http.StatusServiceUnavailable, want: humancalling.VoicemailProviderUnavailable},
		{name: "fresh download URL expired", status: http.StatusOK, expiredURL: true, want: humancalling.VoicemailRecordingURLExpired},
		{name: "provider timeout", timeout: true, want: humancalling.VoicemailProviderTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				if test.timeout {
					time.Sleep(100 * time.Millisecond)
					return
				}
				if request.URL.Path == "/recording.mp3" {
					writer.WriteHeader(http.StatusForbidden)
					return
				}
				if test.status == http.StatusTooManyRequests {
					writer.Header().Set("Retry-After", test.wantRetry)
				}
				if test.expiredURL {
					writer.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(
						writer,
						`{"data":{"download_urls":{"mp3":%q}}}`,
						server.URL+"/recording.mp3",
					)
					return
				}
				writer.WriteHeader(test.status)
			}))
			defer server.Close()
			client := server.Client()
			if test.timeout {
				client.Timeout = 10 * time.Millisecond
			}
			adapter, err := humancalling.NewTelnyxAdapter(humancalling.TelnyxConfig{
				APIKey:     "synthetic-key",
				BaseURL:    server.URL + "/v2",
				HTTPClient: client,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.OpenVoicemailRecording(
				context.Background(),
				"provider-recording-1",
				"",
			)
			var unavailable *humancalling.VoicemailUnavailableError
			if !errors.As(err, &unavailable) ||
				unavailable.Reason != test.want ||
				unavailable.RetryAfter != test.wantRetry {
				t.Fatalf("voicemail playback error = %#v, want reason %q retry %q", err, test.want, test.wantRetry)
			}
		})
	}
}

func TestTelnyxAdapterRejectsInsecureProductionRecordingURL(t *testing.T) {
	var requests int
	adapter, err := humancalling.NewTelnyxAdapter(humancalling.TelnyxConfig{
		APIKey:  "synthetic-key",
		BaseURL: "https://api.telnyx.test/v2",
		HTTPClient: &http.Client{Transport: httpRoundTripperFunc(func(
			request *http.Request,
		) (*http.Response, error) {
			requests++
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"data":{"download_urls":{"mp3":"http://recordings.telnyx.test/audio.mp3"}}}`,
				)),
				Request: request,
			}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.OpenVoicemailRecording(
		context.Background(),
		"provider-recording-1",
		"",
	)
	var unavailable *humancalling.VoicemailUnavailableError
	if !errors.As(err, &unavailable) ||
		unavailable.Reason != humancalling.VoicemailProviderInvalid ||
		requests != 1 {
		t.Fatalf("insecure recording URL result = err:%#v requests:%d", err, requests)
	}
}

type httpRoundTripperFunc func(*http.Request) (*http.Response, error)

func (transport httpRoundTripperFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return transport(request)
}

func TestTelnyxAdapterAcceptsRawMediaJWT(t *testing.T) {
	expiresAt := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	claims := base64.RawURLEncoding.EncodeToString([]byte(
		`{"exp":` + fmt.Sprint(expiresAt.Unix()) + `}`,
	))
	token := "eyJhbGciOiJIUzI1NiJ9." + claims + ".synthetic-signature"
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/v2/telephony_credentials/credential-1/token" {
			http.NotFound(writer, request)
			return
		}
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(token))
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
	result, err := adapter.Execute(context.Background(), humancalling.ProviderCommand{
		ID:       "create-jwt-1",
		Action:   humancalling.CommandCreateJWT,
		TargetID: "credential-1",
		Payload:  map[string]any{},
	})
	if err != nil {
		t.Fatalf("create media JWT: %v", err)
	}
	if result.JWT != token || !result.JWTExpiresAt.Equal(expiresAt) {
		t.Fatalf("media JWT result = %#v", result)
	}
}

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
		APIKey:     "synthetic-key",
		BaseURL:    server.URL + "/v2",
		HTTPClient: server.Client(),
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
			"connection_id":         "call-control-app",
			"from":                  "+15555550199",
			"link_to":               "caller-control",
			"bridge_intent":         true,
			"bridge_on_answer":      true,
			"prevent_double_bridge": true,
			"client_state":          "opaque-state",
			"timeout_secs":          float64(20),
			"custom_headers": []map[string]string{{
				"name":  "X-Acuity-Media-Token",
				"value": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			}},
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
		dialRequest["timeout_secs"] != float64(20) ||
		dialRequest["bridge_on_answer"] != true ||
		dialRequest["prevent_double_bridge"] != true ||
		fmt.Sprint(dialRequest["custom_headers"]) !=
			"[map[name:X-Acuity-Media-Token value:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA]]" {
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

func TestTelnyxAdapterNormalizesVoicemailAndOutboundDestination(t *testing.T) {
	requests := make(chan map[string]any, 3)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
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
				`{"data":{"call_control_id":"destination-control","call_leg_id":"destination-leg"}}`,
			))
			return
		}
		_, _ = writer.Write([]byte(`{"data":{"result":"ok"}}`))
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
	commands := []humancalling.ProviderCommand{
		{
			ID:       "voicemail-greeting",
			Action:   humancalling.CommandPlayVoicemailGreeting,
			TargetID: "caller-control",
			Payload: map[string]any{
				"greeting":     "Please leave a message after the beep.",
				"client_state": "opaque-voicemail",
			},
		},
		{
			ID:       "voicemail-recording",
			Action:   humancalling.CommandStartVoicemailRecording,
			TargetID: "caller-control",
			Payload: map[string]any{
				"format":           "mp3",
				"channels":         "single",
				"recording_track":  "inbound",
				"transcription":    false,
				"play_beep":        true,
				"max_length":       float64(120),
				"custom_file_name": "voicemail-synthetic",
				"client_state":     "opaque-voicemail",
			},
		},
		{
			ID:       "destination-dial",
			Action:   humancalling.CommandDialDestination,
			TargetID: "staff-control",
			Payload: map[string]any{
				"to":                          "+15555550100",
				"connection_id":               "call-control-app",
				"from":                        "+15555550199",
				"link_to":                     "staff-control",
				"bridge_intent":               true,
				"bridge_on_answer":            true,
				"answering_machine_detection": "disabled",
				"timeout_secs":                float64(30),
				"client_state":                "opaque-destination",
			},
		},
	}
	for _, command := range commands {
		if _, err := adapter.Execute(context.Background(), command); err != nil {
			t.Fatalf("execute %s: %v", command.Action, err)
		}
	}
	greeting := <-requests
	recording := <-requests
	destination := <-requests
	if greeting["_path"] != "/v2/calls/caller-control/actions/speak" ||
		greeting["payload"] != "Please leave a message after the beep." ||
		greeting["voice"] != "Polly.Matthew" ||
		greeting["language"] != "en-US" ||
		greeting["command_id"] != "voicemail-greeting" {
		t.Fatalf("voicemail greeting request = %#v", greeting)
	}
	if recording["_path"] != "/v2/calls/caller-control/actions/record_start" ||
		recording["format"] != "mp3" ||
		recording["channels"] != "single" ||
		recording["recording_track"] != "inbound" ||
		recording["max_length"] != float64(120) ||
		recording["transcription"] != false {
		t.Fatalf("voicemail recording request = %#v", recording)
	}
	if destination["_path"] != "/v2/calls" ||
		destination["to"] != "+15555550100" ||
		destination["from"] != "+15555550199" ||
		destination["link_to"] != "staff-control" ||
		destination["bridge_on_answer"] != true ||
		destination["answering_machine_detection"] != "disabled" ||
		destination["timeout_secs"] != float64(30) {
		t.Fatalf("outbound destination request = %#v", destination)
	}
}

func TestTelnyxAdapterRejectsIncompleteDurableCommand(t *testing.T) {
	requested := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		requested <- struct{}{}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	adapter, err := humancalling.NewTelnyxAdapter(humancalling.TelnyxConfig{
		APIKey:     "synthetic-key",
		BaseURL:    server.URL + "/v2",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new Telnyx adapter: %v", err)
	}
	if _, err := adapter.Execute(context.Background(), humancalling.ProviderCommand{
		ID:       "incomplete-dial",
		Action:   humancalling.CommandDialStaff,
		TargetID: "caller-control",
		Payload: map[string]any{
			"to":                    "sip:synthetic-user@sip.telnyx.com",
			"connection_id":         "call-control-app",
			"from":                  "+15555550199",
			"link_to":               "caller-control",
			"bridge_intent":         true,
			"bridge_on_answer":      true,
			"prevent_double_bridge": true,
			"client_state":          "opaque-state",
			"timeout_secs":          float64(20),
		},
	}); err != humancalling.ErrInvalidInput {
		t.Fatalf("incomplete Dial error = %v, want %v", err, humancalling.ErrInvalidInput)
	}
	select {
	case <-requested:
		t.Fatal("incomplete durable command reached Telnyx")
	default:
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
