package interaction

import (
	"encoding/json"
	"regexp"
	"slices"

	"github.com/google/uuid"
)

type ProviderErrorDiagnostic struct {
	Operation  string `json:"operation"`
	Category   string `json:"category"`
	DurationMs int    `json:"durationMs"`
	HTTPStatus *int   `json:"httpStatus,omitempty"`
	Code       string `json:"code,omitempty"`
}

type MiddlewareRequestDiagnostic struct {
	RequestID          string                    `json:"requestId"`
	Operation          string                    `json:"operation"`
	Attempt            int                       `json:"attempt"`
	DurationMs         int                       `json:"durationMs"`
	Result             string                    `json:"result"`
	HTTPStatus         *int                      `json:"httpStatus,omitempty"`
	AppointmentsStatus string                    `json:"appointmentsStatus,omitempty"`
	ResponseStatus     string                    `json:"responseStatus,omitempty"`
	Outcome            string                    `json:"outcome,omitempty"`
	Category           string                    `json:"category,omitempty"`
	FailureReason      string                    `json:"failureReason,omitempty"`
	Retryable          *bool                     `json:"retryable,omitempty"`
	ProviderErrorCount *int                      `json:"providerErrorCount,omitempty"`
	ProviderErrors     []ProviderErrorDiagnostic `json:"providerErrors,omitempty"`
}

var diagnosticCode = regexp.MustCompile(`^-?[0-9]{1,6}$`)

func diagnosticLabel(value string, allowed ...string) string {
	if slices.Contains(allowed, value) {
		return value
	}
	return ""
}

func diagnosticCategory(value string) string {
	return diagnosticLabel(value, "none", "canceled", "timeout", "network", "conflict", "rejected", "authentication", "unavailable", "upstream_status", "invalid_response", "internal", "upstream_error")
}

func diagnosticHTTPStatus(value *int) *int {
	if value != nil && *value >= 100 && *value <= 599 {
		return value
	}
	return nil
}

// Project only known diagnostic fields. Agent closeout remains raw evidence;
// arbitrary messages, identifiers and unknown labels never enter this response.
func middlewareRequestDiagnostics(value any) []MiddlewareRequestDiagnostic {
	var result []MiddlewareRequestDiagnostic
	for _, raw := range arrayValue(value) {
		if len(result) == 32 {
			break
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var item MiddlewareRequestDiagnostic
		if json.Unmarshal(encoded, &item) != nil {
			continue
		}
		id, err := uuid.Parse(item.RequestID)
		item.Operation = diagnosticLabel(item.Operation, "resolvePatient", "getAvailability", "createPatient", "bookAppointment", "cancelAppointment", "updateInsurance")
		item.Result = diagnosticLabel(item.Result, "response", "http_error", "network_error", "timeout", "cancelled", "invalid_response", "not_configured", "unsupported_office")
		if err != nil || id.String() != item.RequestID || id.Version() != 4 || item.Operation == "" || item.Result == "" || item.Attempt < 1 || item.Attempt > 1000 || item.DurationMs < 0 || item.DurationMs > 86400000 {
			continue
		}
		item.AppointmentsStatus = diagnosticLabel(item.AppointmentsStatus, "found", "none", "error")
		item.HTTPStatus = diagnosticHTTPStatus(item.HTTPStatus)
		item.Category = diagnosticCategory(item.Category)
		item.ResponseStatus = diagnosticLabel(item.ResponseStatus, "error", "failed", "failure", "success", "verified", "multiple_matches", "not_found", "no_match", "no_appointments", "created", "partial", "updated", "booked", "cancelled", "found", "none", "incomplete")
		item.Outcome = diagnosticLabel(item.Outcome, "success", "invalid_request", "authentication_rejected", "provider_failure", "internal_failure", "not_found", "client_error", "server_error", "rejected", "reconciled_failure", "indeterminate_write", "validation_failed", "unavailable", "failed", "reconciled_success", "validation", "invalid_booking_token", "invalid_cancellation_token", "invalid_reschedule_token", "booking_token_required", "appointment_type_unresolved", "patient_context_mismatch", "slot_unavailable", "provider_conflict", "provider_rejected", "ownership_mismatch", "write_failed", "availability_search_incomplete", "availability_found", "no_availability", "no_eligible_providers")
		item.FailureReason = diagnosticLabel(item.FailureReason, "middleware_error", "network_error", "invalid_response", "request_rejected", "unsupported_office", "cancelled", "invalid_cancellation_token")
		if item.FailureReason == "" {
			item.Retryable = nil
		}
		providers := []ProviderErrorDiagnostic{}
		for _, provider := range item.ProviderErrors {
			if len(providers) == 8 {
				break
			}
			provider.Operation = diagnosticLabel(provider.Operation, "lookuppatient", "addpatient", "addinsurance", "enddateinsurance", "getdemographic", "getschedulersetup", "get_appointments", "get_block_holds", "get_appointments_by_month", "book_appointment", "cancel_appointment")
			provider.Category = diagnosticCategory(provider.Category)
			if provider.Operation == "" || provider.Category == "" || provider.DurationMs < 0 || provider.DurationMs > 86400000 {
				continue
			}
			provider.HTTPStatus = diagnosticHTTPStatus(provider.HTTPStatus)
			if !diagnosticCode.MatchString(provider.Code) {
				provider.Code = ""
			}
			providers = append(providers, provider)
		}
		item.ProviderErrors = providers
		if item.ProviderErrorCount != nil && (*item.ProviderErrorCount < len(providers) || *item.ProviderErrorCount > 1000000) {
			item.ProviderErrorCount = nil
		}
		result = append(result, item)
	}
	return result
}
