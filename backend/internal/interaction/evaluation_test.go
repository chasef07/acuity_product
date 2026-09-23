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
