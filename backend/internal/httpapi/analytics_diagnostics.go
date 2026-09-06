package httpapi

import (
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
)

func analyticsDiagnosticsResponse(value interaction.AnalyticsDiagnostics) *api.OperatorAIAnalyticsDiagnostics {
	result := &api.OperatorAIAnalyticsDiagnostics{Stages: []api.OperatorAILatencyDistribution{}, Tools: []api.OperatorAIToolDiagnostics{}}
	for _, stage := range value.Stages {
		row := api.OperatorAILatencyDistribution{Stage: api.OperatorAILatencyDistributionStage(stage.Stage), SampleCount: stage.SampleCount, MeasuredCalls: stage.MeasuredCalls, P50Ms: stage.P50Ms, P95Ms: stage.P95Ms, P99Ms: stage.P99Ms, Buckets: []api.OperatorAILatencyBucket{}, Trend: []api.OperatorAILatencyTrend{}}
		for _, b := range stage.Buckets {
			row.Buckets = append(row.Buckets, api.OperatorAILatencyBucket{FromMs: b.FromMs, ToMs: b.ToMs, Count: b.Count, Examples: diagnosticExamplesResponse(b.Examples)})
		}
		for _, point := range stage.Trend {
			row.Trend = append(row.Trend, api.OperatorAILatencyTrend{Date: point.Date, SampleCount: point.SampleCount, P50Ms: point.P50Ms, P95Ms: point.P95Ms})
		}
		result.Stages = append(result.Stages, row)
	}
	for _, tool := range value.Tools {
		result.Tools = append(result.Tools, api.OperatorAIToolDiagnostics{Name: tool.Name, ExecutionCount: tool.ExecutionCount, ErrorCount: tool.ErrorCount, IncompleteCount: tool.IncompleteCount, SampleCount: tool.SampleCount, P50Ms: tool.P50Ms, P95Ms: tool.P95Ms, Examples: diagnosticExamplesResponse(tool.Examples), Errors: diagnosticExamplesResponse(tool.Errors)})
	}
	return result
}
func diagnosticExamplesResponse(values []interaction.DiagnosticExample) []api.OperatorAIDiagnosticExample {
	result := make([]api.OperatorAIDiagnosticExample, 0, len(values))
	for _, v := range values {
		result = append(result, api.OperatorAIDiagnosticExample{InteractionId: v.InteractionID, ItemId: stringPointer(v.ItemID), CallId: stringPointer(v.CallID), StartedAt: v.StartedAt, DurationMs: v.DurationMs, Status: stringPointer(v.Status)})
	}
	return result
}
