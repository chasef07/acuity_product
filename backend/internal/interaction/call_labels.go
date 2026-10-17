package interaction

type CallIntent string
type CallOutcome string

func ValidCallIntent(value string) bool {
	switch value {
	case "new_patient_booking", "existing_patient_booking", "reschedule_appointment",
		"cancel_appointment", "appointment_information", "office_information",
		"insurance_question", "billing_question", "prescription_or_refill",
		"records_or_forms", "glasses_or_contacts", "returning_call", "speak_to_staff", "other":
		return true
	default:
		return false
	}
}

func ValidCallOutcome(value string) bool {
	return value == "resolved" || value == "transferred" || value == "abandoned"
}
