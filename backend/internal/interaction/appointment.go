package interaction

import (
	"encoding/json"
	"strconv"
	"strings"
)

type AppointmentFacts struct {
	AppointmentDate     string
	AppointmentID       string
	AppointmentTime     string
	AppointmentTypeName string
	CareLane            string
	LocationName        string
	PatientName         string
	ProviderName        string
	StartDatetime       string
}

type AppointmentDetails struct {
	Appointment         AppointmentFacts
	PreviousAppointment *AppointmentFacts
}

func ProjectAppointmentDetails(value Interaction) AppointmentDetails {
	expectedOutcome := appointmentDomainOutcomeFor(value)
	action := latestAppointmentAction(value.CloseoutPayload, expectedOutcome)
	appointment := recordValue(action["appointment"])
	cancelledAppointment := recordValue(action["cancelledAppointment"])
	bookingResult := decodeRecord(value.BookingResult)
	cancellationResult := decodeRecord(value.CancellationResult)

	if expectedOutcome == "cancelled" {
		return AppointmentDetails{Appointment: appointmentFacts(
			[]map[string]any{
				cancelledAppointment,
				appointment,
				recordValue(cancellationResult["appointment"]),
				cancellationResult,
			},
			value.OldAppointmentID,
		)}
	}

	result := AppointmentDetails{Appointment: appointmentFacts(
		[]map[string]any{
			appointment,
			recordValue(bookingResult["appointment"]),
			bookingResult,
		},
		value.NewAppointmentID,
	)}
	if expectedOutcome != "rescheduled" {
		return result
	}
	previous := appointmentFacts(
		[]map[string]any{
			cancelledAppointment,
			recordValue(cancellationResult["appointment"]),
			cancellationResult,
		},
		value.OldAppointmentID,
	)
	result.PreviousAppointment = &previous
	return result
}

func latestAppointmentAction(
	closeoutPayload json.RawMessage,
	expected string,
) map[string]any {
	closeout := decodeRecord(closeoutPayload)
	if outcomes, present := closeout["domainOutcomes"]; present {
		return latestAppointmentOutcomeEvidence(arrayValue(outcomes), expected)
	}

	actions := arrayValue(closeout["appointmentActions"])
	for index := len(actions) - 1; index >= 0; index-- {
		action := recordValue(actions[index])
		if action == nil {
			continue
		}
		if expected == "" || strings.EqualFold(stringValue(action["action"]), expected) {
			return action
		}
	}
	return map[string]any{}
}

func appointmentDomainOutcomeFor(value Interaction) string {
	switch value.AppointmentAction {
	case AppointmentBooked:
		return "booked"
	case AppointmentCancelled:
		return "cancelled"
	case AppointmentRescheduled:
		return "rescheduled"
	}
	return map[AppointmentOutcome]string{
		OutcomeBooking:      "booked",
		OutcomeCancellation: "cancelled",
		OutcomeReschedule:   "rescheduled",
	}[value.AppointmentOutcome]
}

func latestAppointmentOutcomeEvidence(outcomes []any, expected string) map[string]any {
	for index := len(outcomes) - 1; index >= 0; index-- {
		receipt := recordValue(outcomes[index])
		status := strings.ToLower(stringValue(receipt["status"]))
		receiptOutcome := strings.ToLower(stringValue(receipt["outcome"]))
		if receipt == nil ||
			(status != "success" && status != "partial") ||
			(expected != "" && receiptOutcome != expected) ||
			(expected == "" && !appointmentDomainOutcome(receiptOutcome)) {
			continue
		}
		evidence := recordValue(receipt["evidence"])
		if evidence == nil || boolValue(evidence["replayed"]) {
			continue
		}
		return evidence
	}
	return map[string]any{}
}

func appointmentDomainOutcome(outcome string) bool {
	return outcome == "booked" || outcome == "cancelled" || outcome == "rescheduled"
}

func appointmentFacts(sources []map[string]any, appointmentID string) AppointmentFacts {
	return AppointmentFacts{
		AppointmentDate:     firstString(sources, "appointmentDate"),
		AppointmentID:       firstNonEmpty(appointmentID, firstString(sources, "appointmentId", "cancelledAppointmentId")),
		AppointmentTime:     firstString(sources, "appointmentTime"),
		AppointmentTypeName: firstString(sources, "appointmentTypeName"),
		CareLane:            firstString(sources, "careLane"),
		LocationName:        firstString(sources, "locationName"),
		PatientName:         firstString(sources, "patientName"),
		ProviderName:        firstString(sources, "providerName"),
		StartDatetime:       firstString(sources, "startDatetime"),
	}
}

func decodeRecord(value json.RawMessage) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	var result map[string]any
	if json.Unmarshal(value, &result) != nil || result == nil {
		return map[string]any{}
	}
	return result
}

func recordValue(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func firstString(records []map[string]any, keys ...string) string {
	for _, record := range records {
		for _, key := range keys {
			if value := stringValue(record[key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
