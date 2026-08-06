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

func TestTelnyxAdapterRevalidatesEveryVoicemailRedirect(t *testing.T) {
	tests := []struct {
		name     string
		location string
	}{
		{name: "plaintext", location: "http://recordings.telnyx.test/redirected.mp3"},
		{name: "different host", location: "https://other.example/redirected.mp3"},
		{name: "private address", location: "https://127.0.0.1/internal.mp3"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var followed bool
			client := &http.Client{Transport: httpRoundTripperFunc(func(
				request *http.Request,
			) (*http.Response, error) {
				switch request.URL.String() {
				case "https://api.telnyx.test/v2/recordings/provider-recording-1":
					return jsonHTTPResponse(request, http.StatusOK,
						`{"data":{"download_urls":{"mp3":"https://recordings.telnyx.test/audio.mp3"}}}`), nil
				case "https://recordings.telnyx.test/audio.mp3":
					return redirectHTTPResponse(request, test.location), nil
				default:
					followed = true
					return audioHTTPResponse(request, http.StatusOK, "audio"), nil
				}
			})}
			adapter, err := humancalling.NewTelnyxAdapter(humancalling.TelnyxConfig{
				APIKey:     "synthetic-key",
				BaseURL:    "https://api.telnyx.test/v2",
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
				unavailable.Reason != humancalling.VoicemailProviderInvalid ||
				followed {
				t.Fatalf("redirect result = err:%#v followed:%t", err, followed)
			}
		})
	}
}

func TestTelnyxAdapterFollowsValidatedSameHostVoicemailRedirect(t *testing.T) {
	client := &http.Client{Transport: httpRoundTripperFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		switch request.URL.String() {
		case "https://api.telnyx.test/v2/recordings/provider-recording-1":
			return jsonHTTPResponse(request, http.StatusOK,
				`{"data":{"download_urls":{"mp3":"https://recordings.telnyx.test/audio.mp3"}}}`), nil
		case "https://recordings.telnyx.test/audio.mp3":
			return redirectHTTPResponse(
				request,
				"https://recordings.telnyx.test/redirected.mp3",
			), nil
		case "https://recordings.telnyx.test/redirected.mp3":
			return audioHTTPResponse(request, http.StatusOK, "audio"), nil
		default:
			return nil, fmt.Errorf("unexpected request: %s", request.URL)
		}
	})}
	adapter, err := humancalling.NewTelnyxAdapter(humancalling.TelnyxConfig{
		APIKey:     "synthetic-key",
		BaseURL:    "https://api.telnyx.test/v2",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := adapter.OpenVoicemailRecording(
		context.Background(),
		"provider-recording-1",
		"",
	)
	if err != nil {
		t.Fatalf("follow validated redirect: %v", err)
	}
	defer content.Body.Close()
	body, err := io.ReadAll(content.Body)
	if err != nil || string(body) != "audio" {
		t.Fatalf("redirected voicemail body = %q, err=%v", body, err)
	}
}

func TestTelnyxAdapterBoundsHeadersWithoutTimingOutVoicemailBody(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/v2/recordings/provider-recording-1":
			_, _ = fmt.Fprintf(
				writer,
				`{"data":{"download_urls":{"mp3":%q}}}`,
				server.URL+"/slow.mp3",
			)
		case "/slow.mp3":
			writer.Header().Set("Content-Type", "audio/mpeg")
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			time.Sleep(80 * time.Millisecond)
			_, _ = writer.Write([]byte("slow-audio"))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 20 * time.Millisecond
	adapter, err := humancalling.NewTelnyxAdapter(humancalling.TelnyxConfig{
		APIKey:     "synthetic-key",
		BaseURL:    server.URL + "/v2",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := adapter.OpenVoicemailRecording(
		context.Background(),
		"provider-recording-1",
		"",
	)
	if err != nil {
		t.Fatalf("open slow voicemail body: %v", err)
	}
	defer content.Body.Close()
	body, err := io.ReadAll(content.Body)
	if err != nil || string(body) != "slow-audio" {
		t.Fatalf("slow voicemail body = %q, err=%v", body, err)
	}
}

func TestTelnyxAdapterRejectsMalformedPartialVoicemailResponse(t *testing.T) {
	tests := []struct {
		name         string
		contentRange string
	}{
		{name: "missing"},
		{name: "malformed", contentRange: "items 0-3/13"},
		{name: "different range", contentRange: "bytes 4-7/13"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				switch request.URL.Path {
				case "/v2/recordings/provider-recording-1":
					_, _ = fmt.Fprintf(
						writer,
						`{"data":{"download_urls":{"mp3":%q}}}`,
						server.URL+"/partial.mp3",
					)
				case "/partial.mp3":
					if test.contentRange != "" {
						writer.Header().Set("Content-Range", test.contentRange)
					}
					writer.Header().Set("Content-Type", "audio/mpeg")
					writer.WriteHeader(http.StatusPartialContent)
					_, _ = writer.Write([]byte("synt"))
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
			_, err = adapter.OpenVoicemailRecording(
				context.Background(),
				"provider-recording-1",
				"bytes=0-3",
			)
			var unavailable *humancalling.VoicemailUnavailableError
			if !errors.As(err, &unavailable) ||
				unavailable.Reason != humancalling.VoicemailProviderInvalid {
				t.Fatalf("partial response error = %#v", err)
			}
		})
	}
}

func jsonHTTPResponse(
	request *http.Request,
	status int,
	body string,
) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func redirectHTTPResponse(request *http.Request, location string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": {location}},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    request,
	}
}

func audioHTTPResponse(
	request *http.Request,
	status int,
	body string,
) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"audio/mpeg"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
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

func TestTelnyxAdapterUsesExplicitBridgeAfterIndependentStaffDial(t *testing.T) {
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
			"to":               "sip:synthetic-user@sip.telnyx.com",
			"connection_id":    "call-control-app",
			"from":             "+15555550199",
			"link_to":          "caller-control",
			"bridge_intent":    true,
			"bridge_on_answer": false,
			"client_state":     "opaque-state",
			"timeout_secs":     float64(20),
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
		ID:       "command-bridge-1",
		Action:   humancalling.CommandBridge,
		TargetID: "staff-control",
		Payload: map[string]any{
			"call_control_id":       "caller-control",
			"prevent_double_bridge": true,
			"client_state":          "opaque-bridge-state",
		},
	}); err != nil {
		t.Fatalf("bridge selected Staff CallLeg: %v", err)
	}

	dialRequest := <-requests
	if dialRequest["_path"] != "/v2/calls" ||
		dialRequest["command_id"] != "command-dial-1" ||
		dialRequest["connection_id"] != "call-control-app" ||
		dialRequest["from"] != "+15555550199" ||
		dialRequest["timeout_secs"] != float64(20) ||
		dialRequest["bridge_on_answer"] != false ||
		dialRequest["prevent_double_bridge"] != nil ||
		fmt.Sprint(dialRequest["custom_headers"]) !=
			"[map[name:X-Acuity-Media-Token value:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA]]" {
		t.Fatalf("Telnyx Dial request = %#v", dialRequest)
	}
	bridgeRequest := <-requests
	if bridgeRequest["_path"] != "/v2/calls/staff-control/actions/bridge" ||
		bridgeRequest["command_id"] != "command-bridge-1" ||
		bridgeRequest["call_control_id"] != "caller-control" ||
		bridgeRequest["prevent_double_bridge"] != true {
		t.Fatalf("Telnyx Bridge request = %#v", bridgeRequest)
	}
}

func TestTelnyxAdapterSendsVoicemailAndOutboundDestination(t *testing.T) {
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
			Action:   humancalling.CommandSpeakVoicemail,
			TargetID: "caller-control",
			Payload: map[string]any{
				"payload":      "Please leave a message after the beep.",
				"voice":        "Polly.Matthew",
				"language":     "en-US",
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
			Action:   humancalling.CommandDialOutboundDestination,
			TargetID: "staff-control",
			Payload: map[string]any{
				"to":                          "+15555550100",
				"connection_id":               "call-control-app",
				"from":                        "+15555550199",
				"link_to":                     "staff-control",
				"bridge_intent":               true,
				"bridge_on_answer":            false,
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
		destination["bridge_on_answer"] != false ||
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
			"to":               "sip:synthetic-user@sip.telnyx.com",
			"connection_id":    "call-control-app",
			"from":             "+15555550199",
			"link_to":          "caller-control",
			"bridge_intent":    true,
			"bridge_on_answer": false,
			"client_state":     "opaque-state",
			"timeout_secs":     float64(20),
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
		Action:   humancalling.CommandHangupLeg,
		TargetID: "opaque-control",
		Payload:  map[string]any{},
	})
	if err == nil || !errors.Is(err, humancalling.ErrAmbiguousEffect) {
		t.Fatalf("uncertain provider error = %v", err)
	}
}

func TestTelnyxAdapterClassifiesCallControlFailures(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		code         string
		safeCode     string
		definitive   bool
		targetAbsent bool
	}{
		{name: "ended Call", status: 422, code: "90018", safeCode: "TELNYX_CALL_ENDED", definitive: true, targetAbsent: true},
		{name: "Call not answered", status: 422, code: "90034", safeCode: "TELNYX_CALL_NOT_ANSWERED"},
		{name: "user channel limit", status: 422, code: "90041", safeCode: "TELNYX_USER_CHANNEL_LIMIT", definitive: true},
		{name: "profile channel limit", status: 422, code: "90042", safeCode: "TELNYX_PROFILE_CHANNEL_LIMIT", definitive: true},
		{name: "connection channel limit", status: 422, code: "90043", safeCode: "TELNYX_CONNECTION_CHANNEL_LIMIT", definitive: true},
		{name: "authentication", status: 401, safeCode: "TELNYX_AUTH_REJECTED", definitive: true},
		{name: "malformed request", status: 400, safeCode: "TELNYX_INVALID_REQUEST", definitive: true},
		{name: "missing target", status: 404, safeCode: "TELNYX_TARGET_ABSENT", definitive: true, targetAbsent: true},
		{name: "rate limit", status: 429, safeCode: "TELNYX_RATE_LIMITED"},
		{name: "server failure", status: 503, safeCode: "TELNYX_EFFECT_UNCERTAIN"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				_, _ = fmt.Fprintf(writer, `{"errors":[{"code":%q,"detail":"must not escape"}]}`, test.code)
			}))
			defer server.Close()
			adapter, err := humancalling.NewTelnyxAdapter(humancalling.TelnyxConfig{
				APIKey: "synthetic-key", BaseURL: server.URL, HTTPClient: server.Client(),
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.Execute(context.Background(), humancalling.ProviderCommand{
				ID: "classify-command", Action: humancalling.CommandBridge,
				TargetID: "opaque-control", Payload: map[string]any{
					"call_control_id": "caller-control", "prevent_double_bridge": true,
					"client_state": "opaque-state",
				},
			})
			var providerErr *humancalling.ProviderError
			if !errors.As(err, &providerErr) || providerErr.SafeCode != test.safeCode ||
				errors.Is(err, humancalling.ErrDefinitiveProviderFailure) != test.definitive ||
				errors.Is(err, humancalling.ErrProviderTargetAbsent) != test.targetAbsent ||
				strings.Contains(err.Error(), "must not escape") {
				t.Fatalf("classified provider error = %#v, err = %v", providerErr, err)
			}
		})
	}
}

func TestTelnyxAdapterObservesExactActiveCallAndProviderEvents(t *testing.T) {
	since := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v2/connections/connection-1/active_calls":
			if request.URL.Query().Get("page[size]") != "250" {
				http.Error(writer, "missing active-call bound", http.StatusBadRequest)
				return
			}
			_, _ = writer.Write([]byte(`{"data":[
				{"call_control_id":"control-1","call_leg_id":"leg-1",
				 "call_session_id":"session-1","client_state":"opaque-state"},
				{"call_control_id":"other-control","call_leg_id":"other-leg",
				 "call_session_id":"other-session","client_state":"other-state"}
			]}`))
		case "/v2/call_events":
			if request.URL.Query().Get("filter[leg_id]") != "leg-1" ||
				request.URL.Query().Get("filter[type]") != "webhook" ||
				request.URL.Query().Get("filter[occurred_at][gte]") != since.Format(time.RFC3339Nano) {
				http.Error(writer, "wrong event filter", http.StatusBadRequest)
				return
			}
			_, _ = writer.Write([]byte(`{"data":[
				{"name":"call.answered","call_leg_id":"leg-1",
				 "call_session_id":"session-1","event_timestamp":"2026-08-05T12:00:01Z"},
				{"name":"call.bridged","call_leg_id":"leg-1",
				 "call_session_id":"session-1","event_timestamp":"2026-08-05T12:00:02Z"},
				{"name":"call.playback.ended","call_leg_id":"leg-1",
				 "call_session_id":"session-1","event_timestamp":"2026-08-05T12:00:03Z",
				 "metadata":{"raw":{"data":{"record_type":"event",
				 "event_type":"call.playback.ended","id":"playback-event",
				 "occurred_at":"2026-08-05T12:00:03Z","payload":{
				 "connection_id":"connection-1","call_control_id":"control-1",
				 "call_leg_id":"leg-1","call_session_id":"session-1",
				 "client_state":"eyJ2IjoyLCJjYWxsIjoiMTExMTExMTEtMTExMS00MTExLTgxMTEtMTExMTExMTExMTExIiwiY2FsbF9sZWciOiIyMjIyMjIyMi0yMjIyLTQyMjItODIyMi0yMjIyMjIyMjIyMjIiLCJyb2xlIjoiQ0FMTEVSIiwia2luZCI6InJpbmdfd2luZG93In0=",
				 "status":"completed"}}}}}
			]}`))
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
	observation, err := adapter.ObserveCall(
		context.Background(), "connection-1", "control-1", "leg-1",
		"opaque-state", since,
	)
	if err != nil || !observation.Active ||
		observation.CallControlID != "control-1" ||
		observation.CallLegID != "leg-1" ||
		observation.CallSessionID != "session-1" ||
		len(observation.Events) != 3 ||
		observation.Events[0].Type != humancalling.FactCallAnswered ||
		observation.Events[1].Type != humancalling.FactCallBridged ||
		observation.Events[2].Type != humancalling.FactPlaybackEnded ||
		observation.Events[2].PlaybackStatus != "completed" {
		t.Fatalf("Call observation = %#v, err = %v", observation, err)
	}
}
