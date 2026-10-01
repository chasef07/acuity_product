package httpapi

import (
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/google/uuid"
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

func analyticsQualityResponse(value interaction.AnalyticsQuality) api.OperatorAIAnalyticsQuality {
	result := api.OperatorAIAnalyticsQuality{
		Daily:            make([]api.OperatorAIQualityDay, 0, len(value.Daily)),
		EvaluatedCalls:   value.EvaluatedCalls,
		UnevaluatedCalls: value.UnevaluatedCalls,
		FlaggedCalls:     value.FlaggedCalls,
		CheckFlags:       checkFlagsResponse(value.CheckFlags),
		StaffFlags:       staffFlagsResponse(value.StaffFlags),
		SentimentCalls:   value.SentimentCalls,
		MeanSentiment:    value.MeanSentiment,
		TokenCalls:       value.TokenCalls,
		P50InputTokens:   value.P50InputTokens,
		P50CachedTokens:  value.P50CachedTokens,
		P50OutputTokens:  value.P50OutputTokens,
		FootprintCalls:   value.FootprintCalls,
		LatestFootprint:  footprintResponse(value.LatestFootprint),
	}
	for _, day := range value.Daily {
		result.Daily = append(result.Daily, api.OperatorAIQualityDay{
			Date:             day.Date,
			EvaluatedCalls:   day.EvaluatedCalls,
			UnevaluatedCalls: day.UnevaluatedCalls,
			FlaggedCalls:     day.FlaggedCalls,
			CheckFlags:       checkFlagsResponse(day.CheckFlags),
			StaffFlags:       staffFlagsResponse(day.StaffFlags),
			SentimentCalls:   day.SentimentCalls,
			SentimentCounts:  day.SentimentCounts,
			MeanSentiment:    day.MeanSentiment,
			TokenCalls:       day.TokenCalls,
			P50InputTokens:   day.P50InputTokens,
			P90InputTokens:   day.P90InputTokens,
			P50CachedTokens:  day.P50CachedTokens,
			P50OutputTokens:  day.P50OutputTokens,
			FootprintCalls:   day.FootprintCalls,
			Footprint:        footprintResponse(day.Footprint),
		})
	}
	return result
}

func checkFlagsResponse(values []interaction.CheckFlagCount) []api.OperatorAICheckFlagCount {
	result := make([]api.OperatorAICheckFlagCount, 0, len(values))
	for _, value := range values {
		result = append(result, api.OperatorAICheckFlagCount{Check: value.Check, Calls: value.Calls, ScoredCalls: value.ScoredCalls})
	}
	return result
}

func staffFlagsResponse(value interaction.StaffFlagCounts) api.OperatorAIStaffFlagCounts {
	return api.OperatorAIStaffFlagCounts{Pending: value.Pending, Confirmed: value.Confirmed, NotAnIssue: value.NotAnIssue}
}

func footprintResponse(value *interaction.ContextFootprint) *api.OperatorAIContextFootprint {
	if value == nil {
		return nil
	}
	return &api.OperatorAIContextFootprint{
		SpeakerPromptTokens: value.SpeakerPromptTokens,
		ThinkerPromptTokens: value.ThinkerPromptTokens,
		ToolSchemaTokens:    value.ToolSchemaTokens,
		ToolCount:           value.ToolCount,
	}
}

func analyticsVersionsResponse(value interaction.AnalyticsVersions) (api.OperatorAIAnalyticsVersions, error) {
	location := func(id string) (*uuid.UUID, error) {
		if id == "" {
			return nil, nil
		}
		parsed, err := uuid.Parse(id)
		return &parsed, err
	}
	result := api.OperatorAIAnalyticsVersions{
		InEffect: make([]api.OperatorAIVersionInEffect, 0, len(value.InEffect)),
		Changes:  make([]api.OperatorAIVersionChange, 0, len(value.Changes)),
	}
	for _, version := range value.InEffect {
		locationID, err := location(version.LocationID)
		if err != nil {
			return api.OperatorAIAnalyticsVersions{}, err
		}
		result.InEffect = append(result.InEffect, api.OperatorAIVersionInEffect{Dimension: api.OperatorAIVersionDimension(version.Dimension), Version: version.Version, LocationId: locationID})
	}
	for _, change := range value.Changes {
		locationID, err := location(change.LocationID)
		if err != nil {
			return api.OperatorAIAnalyticsVersions{}, err
		}
		result.Changes = append(result.Changes, api.OperatorAIVersionChange{Dimension: api.OperatorAIVersionDimension(change.Dimension), Version: change.Version, PreviousVersion: change.PreviousVersion, FirstSeenAt: change.FirstSeenAt, Date: change.Date, LocationId: locationID})
	}
	return result, nil
}
