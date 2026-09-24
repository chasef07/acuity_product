package interaction

import (
	"encoding/json"
	"math"
)

// EvaluationReviewReasons uses only the two agreed criteria from the current
// evaluator contract. Missing, invalid, or incomplete runs are not passing calls.
func EvaluationReviewReasons(raw json.RawMessage) []string {
	reasons := []string{}
	var evaluation struct {
		Version string `json:"evaluatorVersion"`
		Status  string `json:"status"`
		Results map[string]struct {
			Answers map[string]struct {
				Type        string   `json:"type"`
				Probability *float64 `json:"probability"`
			} `json:"answers"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &evaluation) != nil || evaluation.Status != "complete" || evaluation.Version != "typesafe-trace-v4" {
		return reasons
	}
	probability := func(group, name string) *float64 {
		answer := evaluation.Results[group].Answers[name]
		p := answer.Probability
		if answer.Type != "boolean" || p == nil || math.IsNaN(*p) || *p < 0 || *p > 1 {
			return nil
		}
		return p
	}
	if p := probability("outcome", "claims_supported"); p != nil && *p <= 0.2 {
		reasons = append(reasons, "Possible unsupported claim")
	}
	if p := probability("reaction", "reports_unresolved"); p != nil && *p >= 0.8 {
		reasons = append(reasons, "Caller reports unresolved issue")
	}
	return reasons
}
