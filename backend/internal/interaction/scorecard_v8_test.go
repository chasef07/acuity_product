package interaction

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestV8SixFailuresAreActionable(t *testing.T) {
	keys := []string{"knowledge_available", "office_question_answered", "appointment_datetime_correct", "call_ended_correctly", "transfer_policy_followed", "emergency_policy_followed"}
	var catalog []string
	for _, question := range ScorecardQuestions {
		if question.Source == "judge" {
			catalog = append(catalog, question.Key)
		}
	}
	if !reflect.DeepEqual(catalog, keys) {
		t.Fatalf("active judges = %v, want %v", catalog, keys)
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"evaluatorVersion": "typesafe-scorecard-v8", "status": "complete", "jurors": []string{"a", "b", "c"}, "results": map[string]any{key: map[string]any{"verdict": false, "probability": 0.4667, "votes": map[string]float64{"a": 0.1, "b": 0.3, "c": 1.0}}}})
			answers := judgeAnswers(raw)
			if len(answers) != 1 || answers[0].Answer || !failedReviewQuestion(map[string]bool{key: false}) {
				t.Fatalf("false v8 verdict must be actionable: %+v", answers)
			}
			if got := readEvaluation(raw); len(got.Flags) != 1 || got.Flags[0].Check != key {
				t.Fatalf("failure reading = %+v", got)
			}
		})
	}
}

func TestV8UnknownKnowledgeIsNeitherPassNorFailure(t *testing.T) {
	raw := json.RawMessage(`{"evaluatorVersion":"typesafe-scorecard-v8","status":"complete","jurors":["a","b","c"],"results":{"knowledge_available":{"status":"unknown","reason":"no_office_knowledge_result"},"appointment_datetime_correct":{"status":"not_applicable","reason":"no_appointment_action"}}}`)
	if answers := judgeAnswers(raw); len(answers) != 0 {
		t.Fatalf("unknown/inapplicable checks are not verdicts: %+v", answers)
	}
	if got := readEvaluation(raw); got.Evaluated || len(got.Flags) != 0 {
		t.Fatalf("unknown is not a pass or failure: %+v", got)
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
	for _, version := range []string{"typesafe-scorecard-v6", "typesafe-scorecard-v7"} {
		if len(ScorecardQuestionsForVersion(version)) == 0 {
			t.Fatalf("missing historical catalog: %s", version)
		}
	}
	if got := ScorecardQuestionsForVersion("unknown-version"); got != nil {
		t.Fatalf("unknown version must not silently use current criteria: %+v", got)
	}
}
