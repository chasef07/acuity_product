package interaction

import (
	"encoding/json"
	"testing"
)

func TestAgentCallCheckResult(t *testing.T) {
	for _, tc := range []struct {
		name, status, want string
		change             func(map[string]any, map[string]any)
	}{
		{name: "complete pass", status: "complete", want: "pass"},
		{name: "partial evaluation", status: "incomplete", want: "unknown"},
		{name: "failed check", status: "complete", want: "fail", change: func(results, errors map[string]any) {
			results["call_ended_correctly"] = map[string]any{"verdict": false, "probability": 0.1}
		}},
		{name: "failure despite incomplete evidence", status: "incomplete", want: "fail", change: func(results, errors map[string]any) {
			results["call_ended_correctly"] = map[string]any{"verdict": false, "probability": 0.1}
		}},
		{name: "missing check", status: "complete", want: "unknown", change: func(results, errors map[string]any) { delete(results, "call_ended_correctly") }},
		{name: "unknown check", status: "complete", want: "unknown", change: func(results, errors map[string]any) {
			results["knowledge_available"] = map[string]any{"status": "unknown"}
		}},
		{name: "inapplicable check", status: "complete", want: "pass", change: func(results, errors map[string]any) {
			results["appointment_datetime_correct"] = map[string]any{"status": "not_applicable"}
		}},
		{name: "error overrides answer", status: "complete", want: "unknown", change: func(results, errors map[string]any) {
			errors["call_ended_correctly"] = map[string]any{"cause": "no_quorum"}
		}},
		{name: "invalid verdict", status: "complete", want: "unknown", change: func(results, errors map[string]any) {
			results["call_ended_correctly"] = map[string]any{"verdict": true}
		}},
		{name: "no graded checks", status: "complete", want: "unknown", change: func(results, errors map[string]any) {
			for key := range results {
				results[key] = map[string]any{"status": "not_applicable"}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results, errors := map[string]any{}, map[string]any{}
			for _, q := range juryQuestions(ScorecardJudgeVersion) {
				results[q.key] = map[string]any{"verdict": true, "probability": 0.9}
			}
			if tc.change != nil {
				tc.change(results, errors)
			}
			raw, err := json.Marshal(map[string]any{"evaluatorVersion": ScorecardJudgeVersion, "jurors": []string{"synthetic"}, "status": tc.status, "results": results, "errors": errors})
			if err != nil {
				t.Fatal(err)
			}
			if got := agentCallCheckResult(raw); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`{"evaluatorVersion":"unsupported"}`)} {
		if got := agentCallCheckResult(raw); got != "unknown" {
			t.Fatalf("unsupported evaluation: got %q", got)
		}
	}
	for _, version := range []string{previousJudgeVersion, legacyJudgeVersion} {
		results := map[string]any{}
		for _, q := range juryQuestions(version) {
			results[q.key] = map[string]any{"verdict": q.failureLabel != "", "probability": 0.9}
		}
		raw, err := json.Marshal(map[string]any{"evaluatorVersion": version, "jurors": []string{"synthetic"}, "status": "complete", "results": results})
		if err != nil {
			t.Fatal(err)
		}
		if got := agentCallCheckResult(raw); got != "pass" {
			t.Fatalf("%s: context answers should not fail the call: %q", version, got)
		}
	}
}
