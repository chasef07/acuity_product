package observability_test

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/observability"
)

func TestDiagnosticRouteMetricBoundsLabels(t *testing.T) {
	var output bytes.Buffer
	observer := observability.NewLogger(observability.RuntimeRealtime, "test", slog.New(slog.NewJSONHandler(&output, nil)))
	observer.Observe(observability.RouteAvailability("/v1/events?patient=synthetic", "raw-outcome", "raw-error", -time.Second))
	metric := findMetric(t, entries(t, output.String()), "acuity_backend_route_availability")
	if metric["route"] != "other" || metric["outcome"] != "other" || metric["failure_stage"] != "other" || metric["seconds"] != float64(0) {
		t.Fatalf("unbounded diagnostic metric: %#v", metric)
	}
}
