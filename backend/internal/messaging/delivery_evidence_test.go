package messaging

import "testing"

func TestDeliveryEvidenceMapsEveryTelnyxRecipientStatus(t *testing.T) {
	for status, want := range map[string]DeliveryState{
		"accepted":             DeliverySent,
		"queued":               DeliverySent,
		"sending":              DeliverySent,
		"sent":                 DeliverySent,
		"delivery_unconfirmed": DeliverySent,
		"delivered":            DeliveryDelivered,
		"failed":               DeliveryFailed,
		"sending_failed":       DeliveryFailed,
		"delivery_failed":      DeliveryFailed,
		"undelivered":          DeliveryFailed,
		"expired":              DeliveryFailed,
		"cancelled":            DeliveryFailed,
		" Expired ":            DeliveryFailed,
	} {
		got, known := deliveryEvidence("message.finalized", status)
		if !known || got != want {
			t.Errorf("finalized %q = %q, %t; want %q", status, got, known, want)
		}
	}
	if got, known := deliveryEvidence("message.finalized", "webhook_delivered"); known {
		t.Errorf("unrecognized finalized status projected as %q", got)
	}
	if got, known := deliveryEvidence("message.sent", ""); !known || got != DeliverySent {
		t.Errorf("message.sent without status = %q, %t", got, known)
	}
}

func TestAdvanceDeliveryOnlyFlagsOpposingFinalEvidence(t *testing.T) {
	for _, test := range []struct {
		current       DeliveryState
		evidence      DeliveryState
		next          DeliveryState
		changed       bool
		contradictory bool
	}{
		{DeliverySending, DeliverySent, DeliverySent, true, false},
		{DeliveryUnknown, DeliveryFailed, DeliveryFailed, true, false},
		{DeliverySent, DeliveryDelivered, DeliveryDelivered, true, false},
		{DeliverySent, DeliveryFailed, DeliveryFailed, true, false},
		{DeliveryDelivered, DeliverySent, DeliveryDelivered, false, false},
		{DeliveryFailed, DeliverySent, DeliveryFailed, false, false},
		{DeliveryFailed, DeliveryUnknown, DeliveryFailed, false, false},
		{DeliveryDelivered, DeliveryDelivered, DeliveryDelivered, false, false},
		{DeliveryDelivered, DeliveryFailed, DeliveryDelivered, false, true},
		{DeliveryFailed, DeliveryDelivered, DeliveryFailed, false, true},
	} {
		next, changed, contradictory := advanceDelivery(test.current, test.evidence)
		if next != test.next || changed != test.changed || contradictory != test.contradictory {
			t.Errorf(
				"advance %s with %s = %s, %t, %t; want %s, %t, %t",
				test.current, test.evidence, next, changed, contradictory,
				test.next, test.changed, test.contradictory,
			)
		}
	}
}
