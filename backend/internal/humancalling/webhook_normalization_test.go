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

func TestRecordingSavedNormalizationKeepsDurableTelnyxIdentity(t *testing.T) {
	raw := []byte(`{
		"data": {
			"record_type": "event",
			"event_type": "call.recording.saved",
			"id": "recording-event",
			"occurred_at": "2026-07-27T12:00:00Z",
			"payload": {
				"call_control_id": "staff-control",
				"call_leg_id": "staff-leg",
				"call_session_id": "provider-session",
				"recording_id": "recording-id",
				"recording_urls": {
					"wav": "gs://synthetic-recordings/provider-prefix/call.wav"
				}
			}
		}
	}`)
	fact, known, err := normalizeTelnyxFact(raw)
	if err != nil || !known {
		t.Fatalf("normalize recording saved: known=%t err=%v", known, err)
	}
	if fact.Type != FactRecordingSaved ||
		fact.RecordingID != "recording-id" ||
		!fact.OccurredAt.Equal(time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("recording fact = %#v", fact)
	}
}

func TestRecordingSavedNormalizationDoesNotDependOnCallbackURL(t *testing.T) {
	raw := []byte(`{
		"data": {
			"record_type": "event",
			"event_type": "call.recording.saved",
			"id": "recording-event",
			"occurred_at": "2026-07-27T12:00:00Z",
			"payload": {
				"call_control_id": "staff-control",
				"call_leg_id": "staff-leg",
				"call_session_id": "provider-session",
				"recording_id": "recording-id",
				"recording_urls": {
					"wav": "https://telnyx.example/temporary.wav"
				}
			}
		}
	}`)
	fact, known, err := normalizeTelnyxFact(raw)
	if err != nil || !known {
		t.Fatalf("normalize recording callback: known=%t err=%v", known, err)
	}
	if fact.RecordingID != "recording-id" {
		t.Fatalf("recording identity = %#v", fact)
	}
}

func TestRecordingSavedNormalizationAcceptsCanonicalTelnyxCallback(t *testing.T) {
	raw := []byte(`{
		"data": {
			"record_type": "event",
			"event_type": "call.recording.saved",
			"id": "recording-event",
			"occurred_at": "2026-07-27T12:00:00Z",
			"payload": {
				"call_leg_id": "caller-leg",
				"call_session_id": "provider-session",
				"client_state": "",
				"recording_started_at": "2026-07-27T11:59:30Z",
				"recording_ended_at": "2026-07-27T12:00:00Z"
			}
		}
	}`)
	fact, known, err := normalizeTelnyxFact(raw)
	if err != nil || !known {
		t.Fatalf("normalize canonical recording callback: known=%t err=%v", known, err)
	}
	if fact.CallControlID != "" || fact.RecordingID != "" ||
		fact.CallLegID != "caller-leg" || fact.CallSessionID != "provider-session" {
		t.Fatalf("canonical recording fact = %#v", fact)
	}
}

func TestRecordingErrorNormalizationAcceptsCanonicalTelnyxCallback(t *testing.T) {
	raw := []byte(`{
		"data": {
			"record_type": "event",
			"event_type": "call.recording.error",
			"id": "recording-error-event",
			"occurred_at": "2026-07-27T12:00:00Z",
			"payload": {
				"call_leg_id": "caller-leg",
				"call_session_id": "provider-session",
				"client_state": ""
			}
		}
	}`)
	fact, known, err := normalizeTelnyxFact(raw)
	if err != nil || !known || fact.Type != FactRecordingError ||
		fact.CallControlID != "" || fact.CallLegID != "caller-leg" {
		t.Fatalf("normalize canonical recording error: fact=%#v known=%t err=%v", fact, known, err)
	}
}

func TestSpeakEventNormalizationRecognizesVoicemailLifecycle(t *testing.T) {
	for _, eventType := range []FactType{FactSpeakStarted, FactSpeakEnded} {
		t.Run(string(eventType), func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{
				"data": {
					"record_type": "event",
					"event_type": %q,
					"id": "speak-event",
					"occurred_at": "2026-07-31T12:00:00Z",
					"payload": {
						"call_control_id": "caller-control",
						"call_leg_id": "caller-leg",
						"call_session_id": "provider-session",
						"status": %q
					}
				}
			}`, eventType, map[bool]string{true: "completed"}[eventType == FactSpeakEnded]))
			fact, known, err := normalizeTelnyxFact(raw)
			if err != nil || !known || fact.Type != eventType {
				t.Fatalf("normalize %s: fact=%#v known=%t err=%v", eventType, fact, known, err)
			}
		})
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
