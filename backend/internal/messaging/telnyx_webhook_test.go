package messaging

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"testing"
	"time"
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
