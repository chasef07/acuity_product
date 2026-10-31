package interaction

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestFailuresAreActionableInV12ThroughV8(t *testing.T) {
	six := []string{"knowledge_available", "office_question_answered", "appointment_datetime_correct", "call_ended_correctly", "transfer_policy_followed", "emergency_policy_followed"}
	for _, version := range []string{ScorecardJudgeVersion, v11JudgeVersion, v10JudgeVersion, v9JudgeVersion, v8JudgeVersion} {
		keys := six
		if version == ScorecardJudgeVersion {
			keys = slices.Concat(six, []string{"insurance_listed"})
		}
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
	for _, version := range []string{ScorecardJudgeVersion, v11JudgeVersion, v10JudgeVersion, v9JudgeVersion, v8JudgeVersion} {
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

func TestV11RewordsThreeJudgesWhileV10KeepsItsMeaning(t *testing.T) {
	text := func(version, key string) string {
		for _, question := range ScorecardQuestionsForVersion(version) {
			if question.Key == key {
				return question.Question
			}
		}
		t.Fatalf("%s has no %s judge", version, key)
		return ""
	}
	for key, phrase := range map[string]string{
		"emergency_policy_followed": "Ignore everything before that description",
		"transfer_policy_followed":  "an unfinished attempt is TRUE",
		"office_question_answered":  "honestly saying so and offering staff follow-up is correct",
	} {
		if !strings.Contains(text(ScorecardJudgeVersion, key), phrase) || strings.Contains(text(v10JudgeVersion, key), phrase) {
			t.Fatalf("v11 %s must carry its new rule and v10 must keep its own wording", key)
		}
	}
	if text(ScorecardJudgeVersion, "knowledge_available") != text(v10JudgeVersion, "knowledge_available") {
		t.Fatal("v11 keeps the v10 knowledge question")
	}
}

func TestV12AddsTheInsuranceListJudgeWhileV11KeepsItsSix(t *testing.T) {
	if ScorecardJudgeVersion != "typesafe-scorecard-v12" {
		t.Fatalf("current scorecard = %s", ScorecardJudgeVersion)
	}
	judges := func(version string) []ScorecardQuestion {
		var questions []ScorecardQuestion
		for _, question := range ScorecardQuestionsForVersion(version) {
			if question.Source == "judge" {
				questions = append(questions, question)
			}
		}
		return questions
	}
	v12, v11 := judges(ScorecardJudgeVersion), judges(v11JudgeVersion)
	if !reflect.DeepEqual(v12[:len(v12)-1], v11) {
		t.Fatalf("v12 keeps the v11 judges unchanged: v12=%+v v11=%+v", v12, v11)
	}
	listed := v12[len(v12)-1]
	want := ScorecardQuestion{
		Key: "insurance_listed", Code: "I4", Label: "Insurance list had the plan", Source: "judge", Failure: true,
		Prompt:   "Was every insurance the caller named on the insurance list?",
		Question: "Is every insurance the caller named on `insurance_list`? First, find each insurance plan or company the caller said they have. Second, look for it in `insurance_list` by its label, company heading, or a name it is also said as; a plan listed as not accepted or not confirmed is still on the list. Answer FALSE if an insurance the caller named is not on `insurance_list`, even if the agent offered a staff request; check the list, not the agent's reply. Otherwise answer TRUE, also when the caller named no insurance, will self pay, or the insurance list could not be loaded.",
		Yes:      "Every insurance the caller named is on the insurance list, none was named, or the list could not be loaded.",
		No:       "The caller named an insurance that is not on the insurance list.",
	}
	if listed != want {
		t.Fatalf("insurance_listed = %+v, want %+v", listed, want)
	}
	raw := json.RawMessage(`{"evaluatorVersion":"typesafe-scorecard-v12","status":"complete","jurors":["a","b","c"],"results":{"insurance_listed":{"verdict":false,"probability":0.1,"votes":{"a":0,"b":0.1,"c":0.2},"line":"I have Tricare Prime."}}}`)
	if answers := judgeAnswers(raw); len(answers) != 1 || answers[0].Question != "insurance_listed" || answers[0].Answer {
		t.Fatalf("a false insurance_listed verdict is stored: %+v", answers)
	}
	if answers := judgeAnswers(json.RawMessage(strings.Replace(string(raw), "v12", "v11", 1))); len(answers) != 0 {
		t.Fatalf("v11 has no insurance_listed judge: %+v", answers)
	}
}
