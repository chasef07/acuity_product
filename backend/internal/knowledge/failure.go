package knowledge

import (
	"context"
	"errors"
	"fmt"
)

// ProviderFailure contains only classifications created by the adapter, never
// provider bodies, input text, or transport URLs.
type ProviderFailure struct {
	Code   string
	Status int
}

func (e *ProviderFailure) Error() string {
	return fmt.Sprintf("knowledge embedding %s (HTTP %d)", e.Code, e.Status)
}
func (e *ProviderFailure) Unwrap() error { return ErrUnavailable }

// FailureCode is the bounded operational diagnostic; the model still receives
// a temporary_failure outcome and must not attempt a stale-file fallback.
func FailureCode(err error) string {
	var provider *ProviderFailure
	if errors.As(err, &provider) {
		switch provider.Code {
		case "transport":
			return "provider_transport"
		case "response":
			return "provider_response_invalid"
		case "incomplete":
			return "provider_embedding_incomplete"
		case "http":
			switch provider.Status {
			case 401, 403:
				return "provider_access_denied"
			case 429:
				return "provider_rate_limited"
			case 400:
				return "provider_input_rejected"
			default:
				return "provider_http_failure"
			}
		}
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, ErrUnavailable) {
		return "corpus_or_provider_unavailable"
	}
	return "database_failure"
}
