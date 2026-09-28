package interaction

import (
	"encoding/json"
	"testing"
)

func TestEvaluationReviewReasons(t *testing.T) {
	for _, tc := range []struct {
		name               string
		claims, unresolved any
		status, version    string
		want               int
	}{
		{"both inclusive thresholds", 0.2, 0.8, "complete", "typesafe-trace-v4", 2},
		{"below thresholds", 0.21, 0.79, "complete", "typesafe-trace-v4", 0},
		{"unsupported claim", 0.1, 0.1, "complete", "typesafe-trace-v4", 1},
		{"unresolved", 0.9, 0.9, "complete", "typesafe-trace-v4", 1},
		{"missing", nil, nil, "complete", "typesafe-trace-v4", 0},
		{"invalid", -1, 2, "complete", "typesafe-trace-v4", 0},
		{"incomplete", 0.1, 0.9, "incomplete", "typesafe-trace-v4", 0},
		{"legacy", 0.1, 0.9, "complete", "typesafe-trace-v1", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"status": tc.status, "evaluatorVersion": tc.version, "results": map[string]any{
				"outcome":  map[string]any{"answers": map[string]any{"claims_supported": map[string]any{"type": "boolean", "probability": tc.claims}}},
				"reaction": map[string]any{"answers": map[string]any{"reports_unresolved": map[string]any{"type": "boolean", "probability": tc.unresolved}}},
			}})
			if got := EvaluationReviewReasons(raw); len(got) != tc.want {
				t.Fatalf("reasons=%v want %d", got, tc.want)
			}
		})
	}
	for _, raw := range []string{"", "null", `{"status":"complete"}`, `{"status":"complete","evaluatorVersion":"typesafe-trace-v4","results":{"outcome":{"answers":{"claims_supported":{"type":"boolean","probability":"0.1"}}}}}`} {
		if got := EvaluationReviewReasons(json.RawMessage(raw)); len(got) != 0 {
			t.Fatalf("invalid evaluation highlighted: %s", raw)
		}
	}
}

func TestScorecardReviewReasons(t *testing.T) {
	for _, name := range []string{"request_understood", "appointment_datetime_correct", "office_rules_grounded", "results_reported_truthfully", "conversation_responsive"} {
		for _, tc := range []struct {
			name                             string
			value                            any
			status, resultStatus, answerType string
			failed                           bool
			want                             int
		}{
			{"inclusive", 0.55, "complete", "", "noul", false, 1},
			{"above", 0.550001, "complete", "", "noul", false, 0},
			{"zero", 0.0, "complete", "", "noul", false, 1},
			{"partial", 0.1, "incomplete", "", "noul", false, 1},
			{"failed judge", 0.1, "incomplete", "", "noul", true, 0},
			{"not applicable", 0.1, "complete", "not_applicable", "noul", false, 0},
			{"missing", nil, "complete", "", "noul", false, 0},
			{"string", "0.1", "complete", "", "noul", false, 0},
			{"negative", -0.1, "complete", "", "noul", false, 0},
			{"wrong type", 0.1, "complete", "", "score", false, 0},
			{"skipped", 0.1, "skipped", "", "noul", false, 0},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				failures := map[string]any{"another_judge": map[string]any{"cause": "TimeoutError"}}
				if tc.failed {
					failures[name] = map[string]any{"cause": "TimeoutError"}
				}
				raw, _ := json.Marshal(map[string]any{
					"evaluatorVersion": "typesafe-scorecard-v2", "status": tc.status, "errors": failures,
					"results": map[string]any{name: map[string]any{"status": tc.resultStatus, "answers": map[string]any{name: map[string]any{"type": tc.answerType, "noul": tc.value}}}},
				})
				if got := EvaluationReviewReasons(raw); len(got) != tc.want {
					t.Fatalf("reasons=%v want %d", got, tc.want)
				}
			})
		}
	}
	raw := json.RawMessage(`{"evaluatorVersion":"typesafe-scorecard-v2","status":"incomplete","results":{"request_understood":{"answers":{"request_understood":{"type":"noul","noul":"bad"}}},"office_rules_grounded":{"answers":{"office_rules_grounded":{"type":"noul","noul":0.55}}},"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":0}}},"resolved_or_handed_off":{"answers":{"resolved_or_handed_off":{"type":"noul","noul":0}}}}}`)
	got := EvaluationReviewReasons(raw)
	if len(got) != 1 || got[0] != "Office rules grounded needs review · 0.55" {
		t.Fatalf("invalid or removed judges affected valid result: %v", got)
	}
}

func TestScorecardReviewVersionCompatibility(t *testing.T) {
	for _, version := range []string{"typesafe-scorecard-v1", "typesafe-scorecard-v2", "typesafe-scorecard-v3", "typesafe-scorecard-v4"} {
		raw, _ := json.Marshal(map[string]any{
			"evaluatorVersion": version, "status": "incomplete",
			"results": map[string]any{
				"request_understood":           map[string]any{"answers": map[string]any{"request_understood": map[string]any{"type": "noul", "noul": 0.55}}},
				"appointment_datetime_correct": map[string]any{"status": "not_applicable", "reason": "no_appointment_action_result"},
			},
		})
		want := 1
		if version == "typesafe-scorecard-v4" {
			want = 0
		}
		if got := EvaluationReviewReasons(raw); len(got) != want {
			t.Fatalf("version=%s reasons=%v want %d", version, got, want)
		}
	}
}
