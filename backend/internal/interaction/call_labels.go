package interaction

type CallIntent string

const (
	IntentNewPatientBooking      CallIntent = "new_patient_booking"
	IntentExistingPatientBooking CallIntent = "existing_patient_booking"
)

type CallOutcome string
