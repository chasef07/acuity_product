package httpapi

import (
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
)

func analyticsDiagnosticsResponse(value interaction.AnalyticsDiagnostics) *api.OperatorAIAnalyticsDiagnostics {
	result := &api.OperatorAIAnalyticsDiagnostics{Tools: []api.OperatorAIToolDiagnostics{}}
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

func analyticsDistributionsResponse(value interaction.AnalyticsDistributions) *api.OperatorAIAnalyticsDistributions {
	counts := analyticsDistributionDayResponse("", value.AnalyticsDistributionCounts)
	result := &api.OperatorAIAnalyticsDistributions{
		TotalCalls: counts.TotalCalls,
		Daily:      make([]api.OperatorAIAnalyticsDistributionDay, 0, len(value.Daily)),
		Intents:    counts.Intents, Outcomes: counts.Outcomes, Failures: counts.Failures,
		UnlabeledIntentCalls: counts.UnlabeledIntentCalls, UnlabeledOutcomeCalls: counts.UnlabeledOutcomeCalls,
		EvaluatedCalls: counts.EvaluatedCalls, FailedCalls: counts.FailedCalls, UnevaluatedCalls: counts.UnevaluatedCalls,
	}
	for _, day := range value.Daily {
		response := analyticsDistributionDayResponse(day.Date, day.AnalyticsDistributionCounts)
		response.Paths = analyticsPathsResponse(day.Paths)
		result.Daily = append(result.Daily, response)
	}
	result.Paths = analyticsPathsResponse(value.Paths)
	return result
}

func analyticsPathsResponse(paths []interaction.AnalyticsPathCount) []api.OperatorAIPathCount {
	result := make([]api.OperatorAIPathCount, 0, len(paths))
	for _, path := range paths {
		row := api.OperatorAIPathCount{Evaluated: path.Evaluated, Failures: path.Failures, Calls: path.Calls}
		if path.Intent != nil {
			intent := api.CallIntent(*path.Intent)
			row.Intent = &intent
		}
		if path.Outcome != nil {
			outcome := api.CallOutcome(*path.Outcome)
			row.Outcome = &outcome
		}
		result = append(result, row)
	}
	return result
}

func analyticsDistributionDayResponse(date string, value interaction.AnalyticsDistributionCounts) api.OperatorAIAnalyticsDistributionDay {
	result := api.OperatorAIAnalyticsDistributionDay{
		TotalCalls:            value.TotalCalls,
		Date:                  date,
		Intents:               make([]api.OperatorAIIntentCount, 0, len(value.Intents)),
		Outcomes:              make([]api.OperatorAIOutcomeCount, 0, len(value.Outcomes)),
		Failures:              make([]api.OperatorAIFailureCount, 0, len(value.Failures)),
		UnlabeledIntentCalls:  value.UnlabeledIntentCalls,
		UnlabeledOutcomeCalls: value.UnlabeledOutcomeCalls,
		EvaluatedCalls:        value.EvaluatedCalls,
		FailedCalls:           value.FailedCalls,
		UnevaluatedCalls:      value.UnevaluatedCalls,
	}
	for _, row := range value.Intents {
		result.Intents = append(result.Intents, api.OperatorAIIntentCount{Intent: api.CallIntent(row.Intent), Calls: row.Calls})
	}
	for _, row := range value.Outcomes {
		result.Outcomes = append(result.Outcomes, api.OperatorAIOutcomeCount{Outcome: api.CallOutcome(row.Outcome), Calls: row.Calls})
	}
	for _, row := range value.Failures {
		result.Failures = append(result.Failures, api.OperatorAIFailureCount{Key: row.Key, Label: row.Label, Calls: row.Calls})
	}
	return result
}
