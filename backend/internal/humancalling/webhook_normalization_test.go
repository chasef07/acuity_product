package humancalling

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func TestTelnyxSDKWebhookVerificationRotatesPublicKeys(t *testing.T) {
	oldPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	currentPublicKey, currentPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.answered","id":"rotation-event","occurred_at":%q,"payload":{"call_control_id":"control-1","call_leg_id":"leg-1","call_session_id":"session-1"}}}`,
		now.Format(time.RFC3339Nano),
	))
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		currentPrivateKey,
		append([]byte(timestamp+"|"), raw...),
	))
	event, err := unwrapTelnyxWebhook(
		raw,
		timestamp,
		signature,
		[][]byte{oldPublicKey, currentPublicKey},
	)
	if err != nil || event.Data.ID != "rotation-event" {
		t.Fatalf("verify rotated Telnyx key: event=%#v err=%v", event, err)
	}
	if _, err := unwrapTelnyxWebhook(
		raw,
		timestamp,
		signature,
		[][]byte{oldPublicKey},
	); err == nil {
		t.Fatal("webhook signed by the current key verified with only the old key")
	}
}

func TestKnownEventNormalizationRejectsIrreparablePayloads(t *testing.T) {
	invalidClientState := base64.StdEncoding.EncodeToString([]byte(
		`{"v":1,"call":"not-a-uuid","leg":"staff","attempt":"also-not-a-uuid"}`,
	))
	for name, payload := range map[string]string{
		"missing provider identities": `{}`,
		"invalid opaque identities": fmt.Sprintf(
			`{"call_control_id":"control","call_leg_id":"leg","call_session_id":"session","client_state":%q}`,
			invalidClientState,
		),
	} {
		t.Run(name, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{
				"data": {
					"record_type": "event",
					"event_type": "call.hangup",
					"id": "invalid-event",
					"occurred_at": "2026-07-27T12:00:00Z",
					"payload": %s
				}
			}`, payload))
			if _, known, err := normalizeTelnyxFact(raw); err == nil || known {
				t.Fatalf("normalize irreparable payload: known=%t err=%v", known, err)
			}
		})
	}
}
