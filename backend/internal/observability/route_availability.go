package observability

import (
	"context"
	"time"
)

// DiagnosticRoute is separate from the two critical-read routes in the SLO.
type DiagnosticRoute string

const (
	RouteMessageThreads   DiagnosticRoute = "/v1/message-threads/query"
	RouteAIOutcomes       DiagnosticRoute = "/v1/ai/interactions/outcomes/query"
	RouteCallingReadiness DiagnosticRoute = "/v1/calling/readiness"
	RouteEvents           DiagnosticRoute = "/v1/events"
)

func RouteAvailability(route DiagnosticRoute, outcome AvailabilityOutcome, stage FailureStage, duration time.Duration) Event {
	return event("acuity_backend_route_availability",
		"route", bounded(string(route), string(RouteMessageThreads), string(RouteAIOutcomes), string(RouteCallingReadiness), string(RouteEvents)),
		"outcome", bounded(string(outcome), "available", "unavailable"),
		"failure_stage", bounded(string(stage), "none", "authentication", "authorization", "dependency", "handler"),
		"seconds", positive(duration).Seconds())
}

type streamReadyContextKey struct{}

// WithStreamReadyObserver connects request timing to the stream owner's first
// ready flush. HTTP 200 alone does not establish a usable event stream.
func WithStreamReadyObserver(ctx context.Context, ready func()) context.Context {
	return context.WithValue(ctx, streamReadyContextKey{}, ready)
}

// StreamReady records server-side first-ready delivery, not browser receipt.
func StreamReady(ctx context.Context) {
	if ready, ok := ctx.Value(streamReadyContextKey{}).(func()); ok {
		ready()
	}
}
