package observability

import (
	"context"
	"time"
)

type DiagnosticRoute string

const (
	RouteTaskQuery        DiagnosticRoute = "/v1/tasks/query"
	RouteCallingReadiness DiagnosticRoute = "/v1/calling/readiness"
	RouteEvents           DiagnosticRoute = "/v1/events"
)

func RouteAvailability(route DiagnosticRoute, outcome AvailabilityOutcome, stage FailureStage, duration time.Duration) Event {
	return event("acuity_backend_route_availability",
		"route", bounded(string(route), string(RouteTaskQuery), string(RouteCallingReadiness), string(RouteEvents)),
		"outcome", bounded(string(outcome), "available", "unavailable"),
		"failure_stage", bounded(string(stage), "none", "authentication", "authorization", "dependency", "handler"),
		"seconds", positive(duration).Seconds())
}

type streamReadyContextKey struct{}

func WithStreamReadyObserver(ctx context.Context, ready func()) context.Context {
	return context.WithValue(ctx, streamReadyContextKey{}, ready)
}

func StreamReady(ctx context.Context) {
	if ready, ok := ctx.Value(streamReadyContextKey{}).(func()); ok {
		ready()
	}
}
