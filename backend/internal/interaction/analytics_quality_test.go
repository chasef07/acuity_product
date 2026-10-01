package interaction

import (
	"encoding/json"
	"testing"
	"time"
)

func TestReadEvaluationSeparatesUnevaluatedFlagsAndSentiment(t *testing.T) {
	for _, tc := range []struct {
		name      string
		raw       string
		evaluated bool
		checks    []string
		sentiment *float64
	}{
		{"missing", ``, false, nil, nil},
		{"skipped", `{"evaluatorVersion":"typesafe-scorecard-v4","status":"skipped"}`, false, nil, nil},
		{"unknown version", `{"evaluatorVersion":"future","status":"complete"}`, false, nil, nil},
		{"flags and sentiment", `{"evaluatorVersion":"typesafe-scorecard-v4","status":"incomplete","results":{"request_understood":{"answers":{"request_understood":{"type":"noul","noul":0.4}}},"office_rules_grounded":{"answers":{"office_rules_grounded":{"type":"noul","noul":0.41}}},"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":3.6}}}}}`, true, []string{"request_understood"}, qualityFloat(3.6)},
		{"failed judges are not flags", `{"evaluatorVersion":"typesafe-scorecard-v3","status":"incomplete","results":{"request_understood":{"answers":{"request_understood":{"type":"noul","noul":0.1}}},"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":0}}}},"errors":{"request_understood":{},"expressed_sentiment":{}}}`, true, nil, nil},
		{"out of range sentiment", `{"evaluatorVersion":"typesafe-scorecard-v4","status":"complete","results":{"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":5}}}}}`, true, nil, nil},
		{"legacy trace", `{"evaluatorVersion":"typesafe-trace-v4","status":"complete","results":{"outcome":{"answers":{"claims_supported":{"type":"boolean","probability":0.1}}}}}`, true, []string{"claims_supported"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := readEvaluation(json.RawMessage(tc.raw))
			checks := []string{}
			for _, flag := range got.Flags {
				checks = append(checks, flag.Check)
			}
			if got.Evaluated != tc.evaluated || len(checks) != len(tc.checks) || (len(checks) > 0 && checks[0] != tc.checks[0]) {
				t.Fatalf("got evaluated=%v checks=%v", got.Evaluated, checks)
			}
			if (got.Sentiment == nil) != (tc.sentiment == nil) || got.Sentiment != nil && *got.Sentiment != *tc.sentiment {
				t.Fatalf("sentiment=%v want %v", got.Sentiment, tc.sentiment)
			}
		})
	}
}

func TestCallTokenUsageCountsOnlyRecordedTokens(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want *tokenUsage
	}{
		{"missing", ``, nil},
		{"empty", `[]`, nil},
		{"session duration only", `[{"model":"gpt-live-1","session_duration":60}]`, nil},
		{"typeless llm entries sum", `[{"model":"gpt-6-luna","input_tokens":100,"input_cached_tokens":40,"output_tokens":10},{"type":"llm_usage","inputTokens":50,"outputTokens":5}]`, &tokenUsage{150, 40, 15}},
		{"stt ignored", `[{"type":"stt_usage","input_tokens":999},{"input_tokens":10}]`, &tokenUsage{10, 0, 0}},
		{"invalid entry ignored", `[{"input_tokens":-1},{"input_tokens":"x"}]`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := callTokenUsage(json.RawMessage(tc.raw))
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestContextFootprintRequiresCompleteReport(t *testing.T) {
	complete := contextFootprint(json.RawMessage(`{"speakerPromptTokens":700,"thinkerPromptTokens":3200,"toolSchemaTokens":1800,"toolCount":17}`))
	if complete == nil || *complete != (ContextFootprint{700, 3200, 1800, 17}) {
		t.Fatalf("complete=%+v", complete)
	}
	for _, raw := range []string{``, `null`, `{"speakerPromptTokens":700,"thinkerPromptTokens":3200,"toolSchemaTokens":1800}`, `{"speakerPromptTokens":-1,"thinkerPromptTokens":1,"toolSchemaTokens":1,"toolCount":1}`, `{"speakerPromptTokens":1.5,"thinkerPromptTokens":1,"toolSchemaTokens":1,"toolCount":1}`} {
		if got := contextFootprint(json.RawMessage(raw)); got != nil {
			t.Fatalf("%s became %+v", raw, got)
		}
	}
}

func TestQualityTrendsKeepEmptyDatesUnknown(t *testing.T) {
	from := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	days := analyticsDays(from, from.Add(48*time.Hour))
	q := newQualityAccumulator(days)
	footprint := func(tools int) *ContextFootprint { return &ContextFootprint{700, 3000, 1000 * tools, tools} }
	q.add(qualitySample{startedAt: from.Add(24 * time.Hour), footprint: footprint(2), usage: &tokenUsage{1000, 500, 10}})
	q.add(qualitySample{startedAt: from.Add(25 * time.Hour), footprint: footprint(4), usage: &tokenUsage{3000, 500, 30}})
	q.add(qualitySample{startedAt: from.Add(26 * time.Hour), footprint: footprint(3)})
	result := q.finish()
	if len(result.Daily) != 3 || result.Daily[0].P50InputTokens != nil || result.Daily[0].Footprint != nil || result.Daily[0].MeanSentiment != nil {
		t.Fatalf("empty dates must stay unknown: %+v", result.Daily[0])
	}
	day := result.Daily[1]
	if day.UnevaluatedCalls != 3 || day.TokenCalls != 2 || *day.P50InputTokens != 2000 || *day.P90InputTokens != 3000 ||
		*day.Footprint != *footprint(3) || *result.LatestFootprint != *footprint(3) {
		t.Fatalf("day=%+v latest=%+v", day, result.LatestFootprint)
	}
}

func qualityFloat(value float64) *float64 { return &value }
