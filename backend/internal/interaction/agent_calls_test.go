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
		{"claim does not override receipts", Interaction{AppointmentOutcome: OutcomeCancellation}, `[{"outcome":"booked","status":"success"}]`, []AppointmentAction{}},
		{"blocked receipt", Interaction{AppointmentOutcome: OutcomeBooking}, `[{"outcome":"booked","status":"blocked"}]`, []AppointmentAction{}},
		{"confirmed booking", Interaction{AppointmentOutcome: OutcomeBooking}, ``, []AppointmentAction{AppointmentBooked}},
		{"confirmed reschedule", Interaction{AppointmentOutcome: OutcomeReschedule}, ``, []AppointmentAction{AppointmentRescheduled}},
		{"partial reschedule", Interaction{AppointmentOutcome: OutcomePartial, BookingResult: json.RawMessage(`{"status":"booked"}`)}, ``, []AppointmentAction{AppointmentBooked}},
		{"partial receipt with confirmed replacement", Interaction{AppointmentOutcome: OutcomePartial}, `[{"outcome":"rescheduled","status":"partial","evidence":{"bookingResult":{"status":"booked","appointmentId":"synthetic-new"},"cancellationResult":{"status":"unconfirmed"}}}]`, []AppointmentAction{AppointmentBooked}},
		{"partial receipt with confirmed cancellation", Interaction{}, `[{"outcome":"rescheduled","status":"partial","evidence":{"bookingResult":{"status":"error"},"cancellationResult":{"status":"cancelled"}}}]`, []AppointmentAction{AppointmentCancelled}},
		{"partial receipt without confirmed leg", Interaction{AppointmentOutcome: OutcomeBooking}, `[{"outcome":"rescheduled","status":"partial","evidence":{"bookingResult":{"status":"partial"},"cancellationResult":{"status":"unconfirmed"}}}]`, []AppointmentAction{}},
		{"partial receipt replay excluded", Interaction{AppointmentOutcome: OutcomeBooking}, `[{"outcome":"rescheduled","status":"partial","evidence":{"replayed":true,"bookingResult":{"status":"booked"}}}]`, []AppointmentAction{}},
		{"non-appointment partial receipt excluded", Interaction{}, `[{"outcome":"staff_task_created","status":"partial","evidence":{"bookingResult":{"status":"booked"}}}]`, []AppointmentAction{}},
		{"multiple actions deduplicated", Interaction{}, `[{"outcome":"booked","status":"success","evidence":{"appointmentId":"synthetic-new"}},{"outcome":"booked","status":"success","evidence":{"appointmentId":"synthetic-new"}},{"outcome":"cancelled","status":"success","evidence":{"cancelledAppointmentId":"synthetic-old"}}]`, []AppointmentAction{AppointmentBooked, AppointmentCancelled}},
		{"replay excluded", Interaction{AppointmentOutcome: OutcomeBooking}, `[{"outcome":"booked","status":"success","evidence":{"replayed":true}}]`, []AppointmentAction{}},
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
