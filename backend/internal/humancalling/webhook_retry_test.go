package humancalling

import "testing"

func TestWebhookRetryComparesOnlyEventData(t *testing.T) {
	data := `{"record_type":"event","event_type":"call.answered","id":"retry-event","occurred_at":"2026-08-12T12:00:00Z","payload":{"call_control_id":"control-1","call_leg_id":"leg-1"}}`
	first := []byte(`{"data":` + data + `,"meta":{"attempt":1,"delivered_to":"https://ingress.example/calls"}}`)
	for name, test := range map[string]struct {
		raw  string
		same bool
	}{
		"next attempt": {
			raw:  `{"meta":{"attempt":2,"delivered_to":"https://failover.example/calls"},"data":` + data + `}`,
			same: true,
		},
		"reordered data": {
			raw:  `{"data":{"payload":{"call_leg_id":"leg-1","call_control_id":"control-1"},"occurred_at":"2026-08-12T12:00:00Z","id":"retry-event","event_type":"call.answered","record_type":"event"},"meta":{"attempt":3}}`,
			same: true,
		},
		"changed payload": {
			raw:  `{"data":{"record_type":"event","event_type":"call.answered","id":"retry-event","occurred_at":"2026-08-12T12:00:00Z","payload":{"call_control_id":"control-2","call_leg_id":"leg-1"}},"meta":{"attempt":2}}`,
			same: false,
		},
		"missing data": {
			raw:  `{"meta":{"attempt":2}}`,
			same: false,
		},
		"invalid json": {
			raw:  `{"data":`,
			same: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := sameWebhookEventData(first, []byte(test.raw)); got != test.same {
				t.Fatalf("same webhook event data = %t, want %t", got, test.same)
			}
		})
	}
}
