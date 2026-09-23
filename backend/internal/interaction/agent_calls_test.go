package interaction

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAgentCallRequiresAppointmentOutcomeEvidence(t *testing.T) {
	cases := []struct {
		name     string
		stored   Interaction
		receipts string
		want     []AppointmentAction
	}{
		{"attempt alone", Interaction{AppointmentAction: AppointmentBooked}, `[]`, []AppointmentAction{}},
		{"claim without evidence", Interaction{}, `[{"outcome":"booked","status":"success"}]`, []AppointmentAction{}},
		{"empty evidence", Interaction{}, `[{"outcome":"booked","status":"success","evidence":{}}]`, []AppointmentAction{}},
		{"claim falls back to stored outcome", Interaction{AppointmentOutcome: OutcomeCancellation}, `[{"outcome":"booked","status":"success"}]`, []AppointmentAction{AppointmentCancelled}},
		{"blocked receipt", Interaction{}, `[{"outcome":"booked","status":"blocked"}]`, []AppointmentAction{}},
		{"confirmed booking", Interaction{AppointmentOutcome: OutcomeBooking}, `[]`, []AppointmentAction{AppointmentBooked}},
		{"confirmed reschedule", Interaction{AppointmentOutcome: OutcomeReschedule}, `[]`, []AppointmentAction{AppointmentRescheduled}},
		{"partial reschedule", Interaction{AppointmentOutcome: OutcomePartial, BookingResult: json.RawMessage(`{"status":"booked"}`)}, `[]`, []AppointmentAction{AppointmentBooked}},
		{"multiple actions deduplicated", Interaction{}, `[{"outcome":"booked","status":"success","evidence":{"appointmentId":"synthetic-new"}},{"outcome":"booked","status":"success","evidence":{"appointmentId":"synthetic-new"}},{"outcome":"cancelled","status":"success","evidence":{"cancelledAppointmentId":"synthetic-old"}}]`, []AppointmentAction{AppointmentBooked, AppointmentCancelled}},
		{"replay excluded", Interaction{}, `[{"outcome":"booked","status":"success","evidence":{"replayed":true}}]`, []AppointmentAction{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := agentCall(c.stored, json.RawMessage(c.receipts), false)
			if !reflect.DeepEqual(got.AppointmentActions, c.want) {
				t.Fatalf("actions = %v, want %v", got.AppointmentActions, c.want)
			}
			if got.DurationSeconds != nil {
				t.Fatal("missing end time must not produce a fabricated duration")
			}
		})
	}
}
