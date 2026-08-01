package humancalling

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

func TestRecordingSavedNormalizationRequiresAnExactGCSObject(t *testing.T) {
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
		fact.RecordingBucket != "synthetic-recordings" ||
		fact.RecordingObjectKey != "provider-prefix/call.wav" ||
		!fact.OccurredAt.Equal(time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("recording fact = %#v", fact)
	}
}

func TestRecordingSavedNormalizationDoesNotTreatTelnyxStorageAsGCSReady(t *testing.T) {
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
		t.Fatalf("normalize non-GCS recording: known=%t err=%v", known, err)
	}
	if fact.RecordingBucket != "" || fact.RecordingObjectKey != "" {
		t.Fatalf("non-GCS object became ready: %#v", fact)
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
						"call_session_id": "provider-session"
					}
				}
			}`, eventType))
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
