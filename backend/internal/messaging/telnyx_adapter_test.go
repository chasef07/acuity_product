package messaging_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/messaging"
)

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
		"provider failure without retry": {
			status:        http.StatusInternalServerError,
			responseBody:  `{"errors":[{"code":"unexpected_provider_failure"}]}`,
			expectedError: messaging.ErrAmbiguous,
		},
	} {
		t.Run(name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				requests++
				response.Header().Set("Content-Type", "application/json")
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
			if requests != 1 {
				t.Fatalf("request count = %d, want 1", requests)
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
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(
			`{"data":{"id":"provider-message-1","direction":"outbound","to":[{"phone_number":"+17275550199","status":"delivered"}]}}`,
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
	if path != "/messages/provider-message-1" ||
		result.MessageID != "provider-message-1" ||
		result.State != messaging.DeliveryDelivered {
		t.Fatalf("reconcile = %q, %#v", path, result)
	}
	for _, messageID := range []string{"provider/message-1", ".", "..", " provider-message-1", "provider-message-1 "} {
		if _, err := adapter.Reconcile(context.Background(), messageID); err != messaging.ErrInvalidInput {
			t.Fatalf("unsafe provider Message ID %q error = %v, want %v", messageID, err, messaging.ErrInvalidInput)
		}
	}
}
