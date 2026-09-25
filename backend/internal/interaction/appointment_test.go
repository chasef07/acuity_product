package interaction

import (
	"encoding/json"
	"testing"
)

func TestProjectAppointmentDetailsDoesNotPromoteFailedOrReplayReceipts(t *testing.T) {
	interaction := Interaction{
		AppointmentOutcome: OutcomeBooking,
		NewAppointmentID:   "persisted-new",
		CloseoutPayload: json.RawMessage(`{
			"domainOutcomes":[
				{"callId":"failed","outcome":"booked","status":"failed","evidence":{"appointment":{"patientName":"Failed receipt"}}},
				{"callId":"replay","outcome":"booked","status":"success","evidence":{"replayed":true,"appointment":{"patientName":"Replay duplicate"}}}
			],
			"appointmentActions":[{"action":"booked","appointment":{"patientName":"Legacy must not leak into native records"}}]
		}`),
	}

	details := ProjectAppointmentDetails(interaction)
	if details.Appointment.PatientName != "" || details.Appointment.AppointmentID != "persisted-new" {
		t.Fatalf("unproven receipt promoted appointment details = %#v", details)
	}
}
