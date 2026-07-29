package messaging_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/messaging"
)

func TestTelnyxAdapterSendsTheCommittedMessageShape(t *testing.T) {
	var authorization string
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if request.Method != http.MethodPost || request.URL.Path != "/v2/messages" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		authorization = request.Header.Get("Authorization")
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"data":{"id":"provider-message-1"}}`))
	}))
	defer server.Close()

	adapter, err := messaging.NewTelnyxAdapter(messaging.TelnyxConfig{
		APIKey:         "KEY_synthetic",
		BaseURL:        server.URL + "/v2",
		WebhookBaseURL: "https://ingress.example/v1/provider/telnyx/messaging-webhooks",
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	result, err := adapter.Send(context.Background(), messaging.ProviderCommand{
		ID:                 "command-1",
		MessageID:          "message-1",
		Sender:             "+17275550100",
		Destination:        "+17275550199",
		Body:               "Your records are ready.",
		MediaURL:           "https://media.example/attachment-1",
		CallbackToken:      "callback-token-1",
		MessagingProfileID: "profile-1",
	})
	if err != nil {
		t.Fatalf("send Message: %v", err)
	}
	if result.MessageID != "provider-message-1" ||
		result.State != messaging.DeliverySent {
		t.Fatalf("result = %#v", result)
	}
	if authorization != "Bearer KEY_synthetic" {
		t.Fatalf("authorization = %q", authorization)
	}
	if payload["from"] != "+17275550100" ||
		payload["to"] != "+17275550199" ||
		payload["text"] != "Your records are ready." ||
		payload["messaging_profile_id"] != "profile-1" ||
		payload["type"] != "MMS" ||
		payload["webhook_url"] !=
			"https://ingress.example/v1/provider/telnyx/messaging-webhooks/callback-token-1" {
		t.Fatalf("payload = %#v", payload)
	}
	media, ok := payload["media_urls"].([]any)
	if !ok || len(media) != 1 || media[0] != "https://media.example/attachment-1" {
		t.Fatalf("media_urls = %#v", payload["media_urls"])
	}
}

func TestTelnyxAdapterClassifiesDefiniteRejectionAndAmbiguousResponses(t *testing.T) {
	for name, testCase := range map[string]struct {
		status        int
		responseBody  string
		expectedError error
	}{
		"provider rejection": {
			status:        http.StatusUnprocessableEntity,
			responseBody:  `{"errors":[{"code":"40001"}]}`,
			expectedError: messaging.ErrRejected,
		},
		"invalid accepted response": {
			status:        http.StatusOK,
			responseBody:  `{"data":{}}`,
			expectedError: messaging.ErrAmbiguous,
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				response.WriteHeader(testCase.status)
				_, _ = response.Write([]byte(testCase.responseBody))
			}))
			defer server.Close()
			adapter, err := messaging.NewTelnyxAdapter(messaging.TelnyxConfig{
				APIKey:         "KEY_synthetic",
				BaseURL:        server.URL,
				WebhookBaseURL: "https://ingress.example/hooks",
				HTTPClient:     server.Client(),
			})
			if err != nil {
				t.Fatalf("create adapter: %v", err)
			}
			_, err = adapter.Send(context.Background(), messaging.ProviderCommand{
				ID:                 "command-1",
				MessageID:          "message-1",
				Sender:             "+17275550100",
				Destination:        "+17275550199",
				Body:               "Hello",
				CallbackToken:      "callback-1",
				MessagingProfileID: "profile-1",
			})
			if !errors.Is(err, testCase.expectedError) {
				t.Fatalf("error = %v, want %v", err, testCase.expectedError)
			}
		})
	}
}

func TestTelnyxAdapterReconcilesOnlyByProviderMessageIdentity(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		path = request.URL.EscapedPath()
		_, _ = response.Write([]byte(
			`{"data":{"id":"provider/message 1","to":[{"status":"delivered"}]}}`,
		))
	}))
	defer server.Close()
	adapter, err := messaging.NewTelnyxAdapter(messaging.TelnyxConfig{
		APIKey:         "KEY_synthetic",
		BaseURL:        server.URL,
		WebhookBaseURL: "https://ingress.example/hooks",
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	result, err := adapter.Reconcile(context.Background(), "provider/message 1")
	if err != nil {
		t.Fatalf("reconcile Message: %v", err)
	}
	if path != "/messages/provider%2Fmessage%201" ||
		result.MessageID != "provider/message 1" ||
		result.State != messaging.DeliveryDelivered {
		t.Fatalf("reconcile = %q, %#v", path, result)
	}
}

func TestTelnyxAdapterKeepsAcceptedUnconfirmedDeliveryVisibleAsSent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = response.Write([]byte(
			`{"data":{"id":"provider-message-1","to":[{"status":"delivery_unconfirmed"}]}}`,
		))
	}))
	defer server.Close()
	adapter, err := messaging.NewTelnyxAdapter(messaging.TelnyxConfig{
		APIKey:         "KEY_synthetic",
		BaseURL:        server.URL,
		WebhookBaseURL: "https://ingress.example/hooks",
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	result, err := adapter.Reconcile(context.Background(), "provider-message-1")
	if err != nil {
		t.Fatalf("reconcile Message: %v", err)
	}
	if result.State != messaging.DeliverySent {
		t.Fatalf("delivery state = %q, want %q", result.State, messaging.DeliverySent)
	}
}
