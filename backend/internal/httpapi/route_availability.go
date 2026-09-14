package httpapi

import (
	"net/http"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/observability"
)

func diagnosticRoute(role, method, path string) observability.DiagnosticRoute {
	if role == "realtime" && method == http.MethodGet && path == string(observability.RouteEvents) {
		return observability.RouteEvents
	}
	if role != "portal-api" {
		return ""
	}
	switch {
	case method == http.MethodPost && path == string(observability.RouteMessageThreads):
		return observability.RouteMessageThreads
	case method == http.MethodPost && path == string(observability.RouteAIOutcomes):
		return observability.RouteAIOutcomes
	case method == http.MethodPut && path == string(observability.RouteCallingReadiness):
		return observability.RouteCallingReadiness
	default:
		return ""
	}
}

func (server *Server) serveDiagnosticRoute(next http.Handler, w http.ResponseWriter, r *http.Request, route observability.DiagnosticRoute) {
	response := &statusResponseWriter{ResponseWriter: w}
	started := time.Now()
	recorded, completed := false, false
	record := func(outcome observability.AvailabilityOutcome, stage observability.FailureStage) {
		if recorded {
			return
		}
		recorded = true
		observability.Record(server.observer, observability.RouteAvailability(route, outcome, stage, time.Since(started)))
	}
	defer func() {
		outcome, stage := availabilityResult("", response.statusCode())
		if !completed || (route == observability.RouteEvents && outcome == observability.AvailabilityAvailable) {
			outcome, stage = observability.AvailabilityUnavailable, observability.FailureHandler
		}
		record(outcome, stage)
	}()
	if route == observability.RouteEvents {
		r = r.WithContext(observability.WithStreamReadyObserver(r.Context(), func() {
			record(observability.AvailabilityAvailable, observability.FailureNone)
		}))
		next.ServeHTTP(&streamResponseWriter{response}, r)
	} else {
		next.ServeHTTP(response, r)
	}
	completed = true
}

// Preserve error-aware flushing through the status wrapper.
type streamResponseWriter struct{ *statusResponseWriter }

func (writer *streamResponseWriter) FlushError() error {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(writer.ResponseWriter).Flush()
}
func (writer *streamResponseWriter) Flush() { _ = writer.FlushError() }
