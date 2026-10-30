package interaction

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSixFailuresAreActionableInV10V9AndV8(t *testing.T) {
	keys := []string{"knowledge_available", "office_question_answered", "appointment_datetime_correct", "call_ended_correctly", "transfer_policy_followed", "emergency_policy_followed"}
	for _, version := range []string{ScorecardJudgeVersion, v9JudgeVersion, v8JudgeVersion} {
		var catalog []string
		for _, question := range ScorecardQuestionsForVersion(version) {
			if question.Source == "judge" {
				catalog = append(catalog, question.Key)
			}
		}
		if !reflect.DeepEqual(catalog, keys) {
			t.Fatalf("%s judges = %v, want %v", version, catalog, keys)
		}
		for _, key := range keys {
			t.Run(version+"/"+key, func(t *testing.T) {
				raw, _ := json.Marshal(map[string]any{"evaluatorVersion": version, "status": "complete", "jurors": []string{"a", "b", "c"}, "servedModels": map[string]any{"a": "a-served", "b": nil, "c": "c-served"}, "results": map[string]any{key: map[string]any{"verdict": false, "probability": 0.4667, "votes": map[string]float64{"a": 0.1, "b": 0.3, "c": 1.0}}}})
				answers := judgeAnswers(raw)
				if len(answers) != 1 || answers[0].Answer || !failedReviewQuestion(map[string]bool{key: false}) {
					t.Fatalf("false %s verdict must be actionable: %+v", version, answers)
				}
				if got := readEvaluation(raw); len(got.Flags) != 1 || got.Flags[0].Check != key {
					t.Fatalf("failure reading = %+v", got)
				}
			})
		}
	}
}

func TestUnknownKnowledgeIsNeitherPassNorFailure(t *testing.T) {
	for _, version := range []string{ScorecardJudgeVersion, v9JudgeVersion, v8JudgeVersion} {
		raw := json.RawMessage(`{"evaluatorVersion":"` + version + `","status":"complete","jurors":["a","b","c"],"results":{"knowledge_available":{"status":"unknown","reason":"no_office_knowledge_result"},"appointment_datetime_correct":{"status":"not_applicable","reason":"no_appointment_action"}}}`)
		if answers := judgeAnswers(raw); len(answers) != 0 {
			t.Fatalf("%s unknown/inapplicable checks are not verdicts: %+v", version, answers)
		}
		if got := readEvaluation(raw); got.Evaluated || len(got.Flags) != 0 {
			t.Fatalf("%s unknown is not a pass or failure: %+v", version, got)
		}
	}
}

func TestHistoricalKnowledgeQuestionKeepsItsOriginalMeaning(t *testing.T) {
	var historical, current ScorecardQuestion
	for _, question := range ScorecardQuestionsForVersion("typesafe-scorecard-v7") {
		if question.Key == "office_question_answered" {
			historical = question
		}
	}
	for _, question := range ScorecardQuestionsForVersion("typesafe-scorecard-v8") {
		if question.Key == "office_question_answered" {
			current = question
		}
	}
	if historical.Prompt != "Did office knowledge have the answer to every office question the caller asked?" || current.Prompt == historical.Prompt {
		t.Fatalf("historical meaning must remain separate: v7=%+v v8=%+v", historical, current)
	}
	for _, version := range []string{"typesafe-scorecard-v6", "typesafe-scorecard-v7", "typesafe-scorecard-v8"} {
		if len(ScorecardQuestionsForVersion(version)) == 0 {
			t.Fatalf("missing historical catalog: %s", version)
		}
	}
	if got := ScorecardQuestionsForVersion("unknown-version"); got != nil {
		t.Fatalf("unknown version must not silently use current criteria: %+v", got)
	}
}

func TestV9TransferQuestionGradesAgainstTheAgentsPolicy(t *testing.T) {
	question := func(version string) ScorecardQuestion {
		for _, question := range ScorecardQuestionsForVersion(version) {
			if question.Key == "transfer_policy_followed" {
				return question
			}
		}
		t.Fatalf("%s has no transfer judge", version)
		return ScorecardQuestion{}
	}
	v9, v8 := question("typesafe-scorecard-v9"), question("typesafe-scorecard-v8")
	if !strings.Contains(v9.Question, "`transfer_policy`") || strings.Contains(v8.Question, "transfer_policy") || v8.Prompt != "Did the agent follow the staff-transfer policy?" {
		t.Fatalf("v9 must grade against the agent's transfer policy while v8 keeps its own wording: v9=%+v v8=%+v", v9, v8)
	}
}

func TestV10KnowledgeGapCountsOnlyOfficeQuestions(t *testing.T) {
	question := func(version string) ScorecardQuestion {
		for _, question := range ScorecardQuestionsForVersion(version) {
			if question.Key == "knowledge_available" {
				return question
			}
		}
		t.Fatalf("%s has no knowledge judge", version)
		return ScorecardQuestion{}
	}
	v10, v9 := question("typesafe-scorecard-v10"), question("typesafe-scorecard-v9")
	if !strings.Contains(v10.Question, "Not office questions: insurance acceptance") || strings.Contains(v9.Question, "insurance") {
		t.Fatalf("v10 must exclude insurance and other non-office questions while v9 keeps its own wording: v10=%+v v9=%+v", v10, v9)
	}
	if v10.Prompt != v9.Prompt || v10.Yes == v9.Yes || v9.No != "The office knowledge did not supply the information needed to answer a caller's office question." {
		t.Fatalf("v10 keeps the prompt and v9 keeps its own criteria: v10=%+v v9=%+v", v10, v9)
	}
}
