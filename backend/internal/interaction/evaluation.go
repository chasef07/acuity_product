package interaction

import (
	"encoding/json"
	"fmt"
	"math"
)

type evaluationFlag struct {
	Check  string
	Reason string
}

type evaluationReading struct {
	Evaluated bool
	Version   string
	Scored    []string
	Flags     []evaluationFlag
	Sentiment *float64
}

func EvaluationReviewReasons(raw json.RawMessage) []string {
	reasons := []string{}
	for _, flag := range readEvaluation(raw).Flags {
		reasons = append(reasons, flag.Reason)
	}
	return reasons
}

func readEvaluation(raw json.RawMessage) evaluationReading {
	var scorecard struct {
		Version string                     `json:"evaluatorVersion"`
		Status  string                     `json:"status"`
		Results map[string]json.RawMessage `json:"results"`
		Errors  map[string]json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(raw, &scorecard) == nil &&
		(scorecard.Version == "typesafe-scorecard-v1" || scorecard.Version == "typesafe-scorecard-v2" || scorecard.Version == "typesafe-scorecard-v3" || scorecard.Version == "typesafe-scorecard-v4" || scorecard.Version == "typesafe-scorecard-v5") &&
		(scorecard.Status == "complete" || scorecard.Status == "incomplete") {
		reading := evaluationReading{Version: scorecard.Version, Flags: []evaluationFlag{}}
		answer := func(name, answerType string) *float64 {
			if _, failed := scorecard.Errors[name]; failed {
				return nil
			}
			var result struct {
				Status  string `json:"status"`
				Answers map[string]struct {
					Type  string   `json:"type"`
					Noul  *float64 `json:"noul"`
					Score *float64 `json:"score"`
				} `json:"answers"`
			}
			if json.Unmarshal(scorecard.Results[name], &result) != nil || result.Status == "not_applicable" {
				return nil
			}
			value := result.Answers[name]
			if value.Type != answerType {
				return nil
			}
			if answerType == "score" {
				return value.Score
			}
			return value.Noul
		}
		for _, check := range []struct{ name, label string }{
			{"request_understood", "Request understood"},
			{"appointment_datetime_correct", "Appointment date/time"},
			{"office_rules_grounded", "Office rules grounded"},
			{"results_reported_truthfully", "Results reported truthfully"},
			{"conversation_responsive", "Conversation responsive"},
		} {
			if check.name == "request_understood" && scorecard.Version == "typesafe-scorecard-v5" {
				continue
			}
			if check.name == "results_reported_truthfully" && (scorecard.Version == "typesafe-scorecard-v4" || scorecard.Version == "typesafe-scorecard-v5") {
				continue
			}
			score := answer(check.name, "noul")
			if score == nil || *score < 0 || *score > 1 {
				continue
			}
			reading.Scored = append(reading.Scored, check.name)
			if *score <= 0.4 {
				reading.Flags = append(reading.Flags, evaluationFlag{
					Check:  check.name,
					Reason: fmt.Sprintf("%s needs review · %.2f", check.label, *score),
				})
			}
		}
		if score := answer("expressed_sentiment", "score"); score != nil && *score >= 0 && *score <= 4 {
			reading.Sentiment = score
		}
		reading.Evaluated = len(reading.Scored) > 0
		return reading
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
		return evaluationReading{Flags: []evaluationFlag{}}
	}
	reading := evaluationReading{Version: evaluation.Version, Flags: []evaluationFlag{}}
	probability := func(group, name string) *float64 {
		answer := evaluation.Results[group].Answers[name]
		p := answer.Probability
		if answer.Type != "boolean" || p == nil || math.IsNaN(*p) || *p < 0 || *p > 1 {
			return nil
		}
		return p
	}
	if p := probability("outcome", "claims_supported"); p != nil {
		reading.Scored = append(reading.Scored, "claims_supported")
		if *p <= 0.2 {
			reading.Flags = append(reading.Flags, evaluationFlag{Check: "claims_supported", Reason: "Possible unsupported claim"})
		}
	}
	if p := probability("reaction", "reports_unresolved"); p != nil {
		reading.Scored = append(reading.Scored, "reports_unresolved")
		if *p >= 0.8 {
			reading.Flags = append(reading.Flags, evaluationFlag{Check: "reports_unresolved", Reason: "Caller reports unresolved issue"})
		}
	}
	reading.Evaluated = len(reading.Scored) > 0
	return reading
}
