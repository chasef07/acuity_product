package messaging

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/team-telnyx/telnyx-go/v4"
)

func TestTelnyxWebhookUnwrapSupportsSigningKeyRotation(t *testing.T) {
	oldPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate old webhook key: %v", err)
	}
	nextPublicKey, nextPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate next webhook key: %v", err)
	}
	rawBody := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.received","id":"message-event-rotation","occurred_at":"%s","payload":{"id":"provider-message-rotation","from":{"phone_number":"+17275550199"},"to":[{"phone_number":"+17275550100","status":"webhook_delivered"}],"text":"START"}}}`,
		time.Now().UTC().Format(time.RFC3339),
	))
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		nextPrivateKey,
		append([]byte(timestamp+"|"), rawBody...),
	))

	envelope, ok := unwrapTelnyxWebhook(
		rawBody,
		timestamp,
		signature,
		[][]byte{oldPublicKey, nextPublicKey},
	)
	if !ok {
		t.Fatal("webhook signed by next rotation key was rejected")
	}
	if envelope.Data.ID != "message-event-rotation" ||
		envelope.Data.EventType != "message.received" ||
		envelope.Data.Payload.ID != "provider-message-rotation" ||
		envelope.Data.Payload.From != "+17275550199" ||
		len(envelope.Data.Payload.To) != 1 ||
		envelope.Data.Payload.To[0].Phone != "+17275550100" {
		t.Fatalf("normalized webhook = %#v", envelope)
	}
}

func TestNormalizeInboundPayloadAcceptsRecipientArrayOrString(t *testing.T) {
	for _, tc := range []struct {
		name   string
		to     string
		phone  string
		status string
	}{
		{"array", `[{"phone_number":"+17275550100","status":"webhook_delivered"}]`, "+17275550100", "webhook_delivered"},
		{"string", `"+17275550100"`, "+17275550100", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload telnyx.MessagingInboundMessagePayload
			raw := `{"id":"provider-message-1","from":{"phone_number":"+17275550199"},"to":` + tc.to + `,"text":"hi"}`
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				t.Fatalf("decode inbound payload: %v", err)
			}
			got := normalizeInboundPayload(payload)
			if len(got.To) != 1 || got.To[0].Phone != tc.phone || got.To[0].Status != tc.status {
				t.Fatalf("recipients = %+v, want one %s/%q", got.To, tc.phone, tc.status)
			}
		})
	}
}
