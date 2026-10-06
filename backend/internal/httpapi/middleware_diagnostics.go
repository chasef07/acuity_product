package httpapi

import (
	"fmt"

	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/google/uuid"
)

func operatorMiddlewareRequests(requests []interaction.MiddlewareRequestDiagnostic) (*[]api.MiddlewareRequestDiagnostic, error) {
	if len(requests) == 0 {
		return nil, nil
	}
	result := make([]api.MiddlewareRequestDiagnostic, 0, len(requests))
	for _, request := range requests {
		providers := make([]api.ProviderErrorDiagnostic, 0, len(request.ProviderErrors))
		for _, provider := range request.ProviderErrors {
			providers = append(providers, api.ProviderErrorDiagnostic{Operation: provider.Operation, Category: provider.Category, DurationMs: provider.DurationMs, HttpStatus: provider.HTTPStatus, Code: stringPointer(provider.Code)})
		}
		requestID, err := uuid.Parse(request.RequestID)
		if err != nil {
			return nil, fmt.Errorf("middleware request diagnostic id: %w", err)
		}
		item := api.MiddlewareRequestDiagnostic{
			RequestId: requestID, Operation: request.Operation, Attempt: request.Attempt, DurationMs: request.DurationMs, Result: request.Result,
			HttpStatus: request.HTTPStatus, ResponseStatus: stringPointer(request.ResponseStatus), Outcome: stringPointer(request.Outcome), Category: stringPointer(request.Category),
			FailureReason: stringPointer(request.FailureReason), Retryable: request.Retryable, ProviderErrorCount: request.ProviderErrorCount,
		}
		if request.AppointmentsStatus != "" {
			value := api.MiddlewareRequestDiagnosticAppointmentsStatus(request.AppointmentsStatus)
			item.AppointmentsStatus = &value
		}
		if request.FailureDetail != "" {
			value := api.MiddlewareRequestDiagnosticFailureDetail(request.FailureDetail)
			item.FailureDetail = &value
		}
		if len(providers) > 0 {
			item.ProviderErrors = &providers
		}
		result = append(result, item)
	}
	return &result, nil
}
