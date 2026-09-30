package interaction

import (
	"encoding/json"
	"fmt"
	"math"
)

func EvaluationReviewReasons(raw json.RawMessage) []string {
	reasons := []string{}
	var scorecard struct {
		Version string                     `json:"evaluatorVersion"`
		Status  string                     `json:"status"`
		Results map[string]json.RawMessage `json:"results"`
		Errors  map[string]json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(raw, &scorecard) == nil &&
		(scorecard.Version == "typesafe-scorecard-v1" || scorecard.Version == "typesafe-scorecard-v2" || scorecard.Version == "typesafe-scorecard-v3" || scorecard.Version == "typesafe-scorecard-v4") &&
		(scorecard.Status == "complete" || scorecard.Status == "incomplete") {
		for _, check := range []struct{ name, label string }{
			{"request_understood", "Request understood"},
			{"appointment_datetime_correct", "Appointment date/time"},
			{"office_rules_grounded", "Office rules grounded"},
			{"results_reported_truthfully", "Results reported truthfully"},
			{"conversation_responsive", "Conversation responsive"},
		} {
			if check.name == "results_reported_truthfully" && scorecard.Version == "typesafe-scorecard-v4" {
				continue
			}
			if _, failed := scorecard.Errors[check.name]; failed {
				continue
			}
			var result struct {
				Status  string `json:"status"`
				Answers map[string]struct {
					Type string   `json:"type"`
					Noul *float64 `json:"noul"`
				} `json:"answers"`
			}
			if json.Unmarshal(scorecard.Results[check.name], &result) != nil || result.Status == "not_applicable" {
				continue
			}
			answer := result.Answers[check.name]
			if answer.Type == "noul" && answer.Noul != nil && *answer.Noul >= 0 && *answer.Noul <= 0.4 {
				reasons = append(reasons, fmt.Sprintf("%s needs review · %.2f", check.label, *answer.Noul))
			}
		}
		return reasons
	}
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
