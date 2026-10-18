package interaction

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReadEvaluationScoresOnlyUsableChecks(t *testing.T) {
	noul := func(check string, value float64) string {
		return fmt.Sprintf(`"%s":{"answers":{"%s":{"type":"noul","noul":%v}}}`, check, check, value)
	}
	scorecard := func(version, status, results, errors string) string {
		return fmt.Sprintf(`{"evaluatorVersion":"%s","status":"%s","results":{%s},"errors":{%s}}`, version, status, results, errors)
	}
	all := strings.Join([]string{noul("request_understood", 0.9), noul("appointment_datetime_correct", 0.9), noul("office_rules_grounded", 0.9), noul("results_reported_truthfully", 0.1), noul("conversation_responsive", 0.9)}, ",")
	notApplicable := `"request_understood":{"status":"not_applicable"},"appointment_datetime_correct":{"status":"not_applicable","reason":"no_appointment_action_result"}`
	for _, tc := range []struct {
		name      string
		raw       string
		evaluated bool
		scored    []string
		flags     []string
		sentiment *float64
	}{
		{"missing", ``, false, []string{}, []string{}, nil},
		{"skipped", `{"evaluatorVersion":"typesafe-scorecard-v4","status":"skipped"}`, false, []string{}, []string{}, nil},
		{"unknown version", `{"evaluatorVersion":"future","status":"complete"}`, false, []string{}, []string{}, nil},
		{"v1 scores every judge", scorecard("typesafe-scorecard-v1", "complete", all, ""), true, []string{"request_understood", "appointment_datetime_correct", "office_rules_grounded", "results_reported_truthfully", "conversation_responsive"}, []string{"results_reported_truthfully"}, nil},
		{"v5 ignores removed request and truthfulness judges", scorecard("typesafe-scorecard-v5", "complete", all, ""), true, []string{"appointment_datetime_correct", "office_rules_grounded", "conversation_responsive"}, []string{}, nil},
		{"inclusive flag threshold and sentiment", `{"evaluatorVersion":"typesafe-scorecard-v4","status":"incomplete","results":{"request_understood":{"answers":{"request_understood":{"type":"noul","noul":0.4}}},"office_rules_grounded":{"answers":{"office_rules_grounded":{"type":"noul","noul":0.41}}},"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":3.6}}}}}`, true, []string{"request_understood", "office_rules_grounded"}, []string{"request_understood"}, qualityFloat(3.6)},
		{"failed judges and sentiment are unevaluated", `{"evaluatorVersion":"typesafe-scorecard-v3","status":"incomplete","results":{"request_understood":{"answers":{"request_understood":{"type":"noul","noul":0.1}}},"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":0}}}},"errors":{"request_understood":{},"expressed_sentiment":{}}}`, false, []string{}, []string{}, nil},
		{"out of range sentiment", `{"evaluatorVersion":"typesafe-scorecard-v4","status":"complete","results":{"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":5}}}}}`, false, []string{}, []string{}, nil},
		{"trace flags unsupported claim", `{"evaluatorVersion":"typesafe-trace-v4","status":"complete","results":{"outcome":{"answers":{"claims_supported":{"type":"boolean","probability":0.1}}}}}`, true, []string{"claims_supported"}, []string{"claims_supported"}, nil},
		{"v4 ignores removed truthfulness judge", scorecard("typesafe-scorecard-v4", "complete", all, ""), true, []string{"request_understood", "appointment_datetime_correct", "office_rules_grounded", "conversation_responsive"}, []string{}, nil},
		{"v3 scores truthfulness", scorecard("typesafe-scorecard-v3", "complete", all, ""), true, []string{"request_understood", "appointment_datetime_correct", "office_rules_grounded", "results_reported_truthfully", "conversation_responsive"}, []string{"results_reported_truthfully"}, nil},
		{"complete but every check errored", scorecard("typesafe-scorecard-v4", "complete", noul("request_understood", 0.1)+","+noul("conversation_responsive", 0.9), `"request_understood":{},"conversation_responsive":{}`), false, []string{}, []string{}, nil},
		{"incomplete with every check not applicable", scorecard("typesafe-scorecard-v4", "incomplete", notApplicable, ""), false, []string{}, []string{}, nil},
		{"sentiment alone is unevaluated", scorecard("typesafe-scorecard-v4", "complete", `"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":2}}}`, ""), false, []string{}, []string{}, qualityFloat(2)},
		{"one scored check among unusable ones", scorecard("typesafe-scorecard-v3", "incomplete", notApplicable+","+noul("office_rules_grounded", 0.4)+","+noul("conversation_responsive", 0.1)+`,"results_reported_truthfully":{"answers":{"results_reported_truthfully":{"type":"score","score":0.1}}}`, `"conversation_responsive":{}`), true, []string{"office_rules_grounded"}, []string{"office_rules_grounded"}, nil},
		{"out of range score is not scored", scorecard("typesafe-scorecard-v4", "complete", noul("request_understood", 1.5), ""), false, []string{}, []string{}, nil},
		{"trace scores valid probabilities", `{"evaluatorVersion":"typesafe-trace-v4","status":"complete","results":{"outcome":{"answers":{"claims_supported":{"type":"boolean","probability":1.2}}},"reaction":{"answers":{"reports_unresolved":{"type":"boolean","probability":0.1}}}}}`, true, []string{"reports_unresolved"}, []string{}, nil},
		{"trace without usable probabilities", `{"evaluatorVersion":"typesafe-trace-v4","status":"complete","results":{"outcome":{"answers":{"claims_supported":{"type":"boolean","probability":-1}}}}}`, false, []string{}, []string{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := readEvaluation(json.RawMessage(tc.raw))
			flags := []string{}
			for _, flag := range got.Flags {
				flags = append(flags, flag.Check)
			}
			scored := append([]string{}, got.Scored...)
			if got.Evaluated != tc.evaluated || !reflect.DeepEqual(scored, tc.scored) || !reflect.DeepEqual(flags, tc.flags) || (got.Sentiment == nil) != (tc.sentiment == nil) || got.Sentiment != nil && *got.Sentiment != *tc.sentiment {
				t.Fatalf("got evaluated=%v scored=%v flags=%v sentiment=%v", got.Evaluated, scored, flags, got.Sentiment)
			}
		})
	}
}

func TestQualityCheckRatesUseScoredCalls(t *testing.T) {
	from := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	q := newQualityAccumulator(analyticsDays(from, from))
	for i, raw := range []string{
		`{"evaluatorVersion":"typesafe-scorecard-v4","status":"complete","results":{"request_understood":{"answers":{"request_understood":{"type":"noul","noul":0.2}}},"conversation_responsive":{"answers":{"conversation_responsive":{"type":"noul","noul":0.9}}},"results_reported_truthfully":{"answers":{"results_reported_truthfully":{"type":"noul","noul":0.1}}},"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":3}}}}}`,
		`{"evaluatorVersion":"typesafe-scorecard-v4","status":"incomplete","results":{"request_understood":{"answers":{"request_understood":{"type":"noul","noul":0.9}}},"office_rules_grounded":{"answers":{"office_rules_grounded":{"type":"noul","noul":0.1}}}},"errors":{"office_rules_grounded":{}}}`,
		`{"evaluatorVersion":"typesafe-scorecard-v4","status":"complete","results":{"request_understood":{"answers":{"request_understood":{"type":"noul","noul":0.1}}}},"errors":{"request_understood":{}}}`,
		`{"evaluatorVersion":"typesafe-scorecard-v4","status":"complete","results":{"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":1}}}}}`,
	} {
		q.add(qualitySample{startedAt: from.Add(time.Duration(i) * time.Minute), evaluation: readEvaluation(json.RawMessage(raw))})
	}
	result := q.finish()
	want := []CheckFlagCount{{Check: "request_understood", Calls: 1, ScoredCalls: 2}, {Check: "conversation_responsive", Calls: 0, ScoredCalls: 1}}
	if result.EvaluatedCalls != 2 || result.UnevaluatedCalls != 2 || result.FlaggedCalls != 1 || result.SentimentCalls != 2 ||
		!reflect.DeepEqual(result.CheckFlags, want) || !reflect.DeepEqual(result.Daily[0].CheckFlags, want) {
		t.Fatalf("result=%+v", result)
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

func TestV8SentimentCountsOnlyMeasuredScoresWithoutChangingChecks(t *testing.T) {
	from := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	q := newQualityAccumulator(analyticsDays(from, from))
	for _, tc := range []struct {
		name, result, errors string
		want                 *float64
	}{
		{"negative endpoint", `{"score":0,"votes":{"a":0,"b":0,"c":0},"errors":{}}`, `{}`, qualityFloat(0)},
		{"fractional with partial failure", `{"score":1.5,"votes":{"a":1,"b":2},"errors":{"c":"TimeoutError"}}`, `{}`, qualityFloat(1.5)},
		{"positive endpoint", `{"score":4,"votes":{"a":4,"b":4,"c":4},"errors":{}}`, `{}`, qualityFloat(4)},
		{"historical missing", `null`, `{}`, nil},
		{"null score", `{"score":null}`, `{}`, nil},
		{"string score", `{"score":"2"}`, `{}`, nil},
		{"below scale", `{"score":-1}`, `{}`, nil},
		{"above scale", `{"score":5}`, `{}`, nil},
		{"no quorum overrides stale score", `{"score":2}`, `{"expressed_sentiment":{"cause":"no_quorum","votes":{"a":2},"errors":{"b":"TimeoutError","c":"TimeoutError"}}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(fmt.Sprintf(`{"evaluatorVersion":"typesafe-scorecard-v8","status":"incomplete","jurors":["a","b","c"],"results":{"expressed_sentiment":%s},"errors":%s}`, tc.result, tc.errors))
			score := jurySentiment(raw)
			reading := readEvaluation(raw)
			if !reflect.DeepEqual(score, tc.want) || !reflect.DeepEqual(reading.Sentiment, tc.want) {
				t.Fatalf("sentiment parser=%v analytics=%v want=%v", score, reading.Sentiment, tc.want)
			}
			if reading.Evaluated || len(reading.Scored) != 0 || len(reading.Flags) != 0 || len(judgeAnswers(raw)) != 0 {
				t.Fatalf("sentiment must not create checks or flags: %+v", reading)
			}
			q.add(qualitySample{startedAt: from, evaluation: reading})
		})
	}
	result := q.finish()
	if result.SentimentCalls != 3 || result.MeanSentiment == nil || *result.MeanSentiment != 5.5/3 || result.Daily[0].SentimentCalls != 3 || result.FlaggedCalls != 0 {
		t.Fatalf("only measured sentiment contributes to analytics: %+v", result)
	}
}
