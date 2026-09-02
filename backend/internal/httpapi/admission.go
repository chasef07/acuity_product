package httpapi

import (
	"net/http"

	"github.com/chasef07/acuity_product/backend/internal/admission"
)

func (server *Server) withPortalAdmission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Pattern is populated by the generated ServeMux before this middleware.
		// Matching registered methods and routes prevents a caller from selecting
		// a class with a header, path prefix, or malformed resource identifier.
		class := portalRequestClass(r.Pattern)
		ctx := admission.WithClass(r.Context(), class)
		release, err := server.admission.Acquire(ctx)
		if err != nil {
			if ctx.Err() == nil {
				w.Header().Set("Retry-After", "1")
				server.writeError(w, r, http.StatusServiceUnavailable,
					"UNAVAILABLE", "The workspace is busy. Try again shortly.", true)
			}
			return
		}
		defer release()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func portalRequestClass(pattern string) admission.Class {
	switch pattern {
	case "GET /health/live", "GET /health/ready",
		"POST /v1/handoffs",
		"POST /v1/calling/softphone/lease",
		"PUT /v1/calling/readiness",
		"POST /v1/calling/media-token",
		"POST /v1/calling/outbound-calls",
		"POST /v1/calling/calls/{callId}/transfers",
		"POST /v1/calling/transfers/{transferId}/decline",
		"POST /v1/calling/transfers/{transferId}/cancel",
		"POST /v1/calling/calls/{callId}/media-ready",
		"POST /v1/calling/calls/{callId}/retry",
		"POST /v1/calling/calls/{callId}/hangup",
		"POST /v1/calling/calls/{callId}/disposition":
		return admission.CallingControl
	case "GET /v1/calling/state",
		"GET /v1/calling/calls/{callId}",
		"GET /v1/calling/calls/{callId}/transfer-candidates",
		"GET /v1/calling/tasks/{taskId}/eligibility":
		return admission.CallingSync
	default:
		return admission.Background
	}
}
