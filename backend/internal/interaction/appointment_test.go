package interaction

import (
	"encoding/json"
	"testing"
)

func TestProjectAppointmentDetailsPrefersProvenDomainOutcomeEvidence(t *testing.T) {
	interaction := Interaction{
		AppointmentAction:  AppointmentRescheduled,
		AppointmentOutcome: OutcomePartial,
		OldAppointmentID:   "old-63",
		NewAppointmentID:   "new-63",
		CloseoutPayload: json.RawMessage(`{
			"domainOutcomes":[
				{"callId":"failed","outcome":"rescheduled","status":"failed","evidence":{"appointment":{"patientName":"Failed receipt"}}},
				{"callId":"original","outcome":"rescheduled","status":"partial","evidence":{"appointment":{"patientName":"Jane Doe","appointmentDate":"2026-08-30"},"cancelledAppointment":{"appointmentDate":"2026-08-20"}}},
				{"callId":"replay","outcome":"rescheduled","status":"success","evidence":{"replayed":true,"appointment":{"patientName":"Replay duplicate"}}},
				{"callId":"staff","outcome":"staff_task_created","status":"success","evidence":{"appointment":{"patientName":"Wrong domain"}}}
			],
			"appointmentActions":[{"action":"rescheduled","appointment":{"patientName":"Legacy duplicate"}}]
		}`),
	}

	details := ProjectAppointmentDetails(interaction)
	if details.Appointment.PatientName != "Jane Doe" ||
		details.Appointment.AppointmentDate != "2026-08-30" ||
		details.Appointment.AppointmentID != "new-63" ||
		details.PreviousAppointment == nil ||
		details.PreviousAppointment.AppointmentDate != "2026-08-20" ||
		details.PreviousAppointment.AppointmentID != "old-63" {
		t.Fatalf("domain outcome appointment details = %#v", details)
	}
}

func TestProjectAppointmentDetailsRetainsHistoricalAppointmentActionsFallback(t *testing.T) {
	interaction := Interaction{
		AppointmentOutcome: OutcomeBooking,
		NewAppointmentID:   "legacy-new",
		CloseoutPayload: json.RawMessage(`{
			"appointmentActions":[{"action":"booked","appointment":{"patientName":"Historical Patient","appointmentDate":"2026-08-31"}}]
		}`),
	}

	details := ProjectAppointmentDetails(interaction)
	if details.Appointment.PatientName != "Historical Patient" ||
		details.Appointment.AppointmentDate != "2026-08-31" ||
		details.Appointment.AppointmentID != "legacy-new" {
		t.Fatalf("historical appointment details = %#v", details)
	}
}

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
