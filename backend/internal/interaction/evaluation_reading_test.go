package interaction

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
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
		{"inclusive flag threshold and sentiment", `{"evaluatorVersion":"typesafe-scorecard-v4","status":"incomplete","results":{"request_understood":{"answers":{"request_understood":{"type":"noul","noul":0.4}}},"office_rules_grounded":{"answers":{"office_rules_grounded":{"type":"noul","noul":0.41}}},"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":3.6}}}}}`, true, []string{"request_understood", "office_rules_grounded"}, []string{"request_understood"}, evaluationFloat(3.6)},
		{"failed judges and sentiment are unevaluated", `{"evaluatorVersion":"typesafe-scorecard-v3","status":"incomplete","results":{"request_understood":{"answers":{"request_understood":{"type":"noul","noul":0.1}}},"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":0}}}},"errors":{"request_understood":{},"expressed_sentiment":{}}}`, false, []string{}, []string{}, nil},
		{"out of range sentiment", `{"evaluatorVersion":"typesafe-scorecard-v4","status":"complete","results":{"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":5}}}}}`, false, []string{}, []string{}, nil},
		{"trace flags unsupported claim", `{"evaluatorVersion":"typesafe-trace-v4","status":"complete","results":{"outcome":{"answers":{"claims_supported":{"type":"boolean","probability":0.1}}}}}`, true, []string{"claims_supported"}, []string{"claims_supported"}, nil},
		{"v4 ignores removed truthfulness judge", scorecard("typesafe-scorecard-v4", "complete", all, ""), true, []string{"request_understood", "appointment_datetime_correct", "office_rules_grounded", "conversation_responsive"}, []string{}, nil},
		{"v3 scores truthfulness", scorecard("typesafe-scorecard-v3", "complete", all, ""), true, []string{"request_understood", "appointment_datetime_correct", "office_rules_grounded", "results_reported_truthfully", "conversation_responsive"}, []string{"results_reported_truthfully"}, nil},
		{"complete but every check errored", scorecard("typesafe-scorecard-v4", "complete", noul("request_understood", 0.1)+","+noul("conversation_responsive", 0.9), `"request_understood":{},"conversation_responsive":{}`), false, []string{}, []string{}, nil},
		{"incomplete with every check not applicable", scorecard("typesafe-scorecard-v4", "incomplete", notApplicable, ""), false, []string{}, []string{}, nil},
		{"sentiment alone is unevaluated", scorecard("typesafe-scorecard-v4", "complete", `"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":2}}}`, ""), false, []string{}, []string{}, evaluationFloat(2)},
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

func evaluationFloat(value float64) *float64 { return &value }

func TestV8SentimentReadsOnlyMeasuredScoresWithoutChangingChecks(t *testing.T) {
	for _, tc := range []struct {
		name, result, errors string
		want                 *float64
	}{
		{"negative endpoint", `{"score":0,"votes":{"a":0,"b":0,"c":0},"errors":{}}`, `{}`, evaluationFloat(0)},
		{"fractional with partial failure", `{"score":1.5,"votes":{"a":1,"b":2},"errors":{"c":"TimeoutError"}}`, `{}`, evaluationFloat(1.5)},
		{"positive endpoint", `{"score":4,"votes":{"a":4,"b":4,"c":4},"errors":{}}`, `{}`, evaluationFloat(4)},
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
		})
	}
}
