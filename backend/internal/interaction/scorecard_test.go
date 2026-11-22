package interaction

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type syntheticTool struct {
	name, args, output string
}

func syntheticTranscript(t *testing.T, tools ...syntheticTool) json.RawMessage {
	t.Helper()
	items := []any{map[string]any{"type": "message", "role": "user", "content": []string{"Synthetic caller turn."}}}
	for index, tool := range tools {
		callID := "call-" + string(rune('a'+index))
		items = append(items,
			map[string]any{"type": "function_call", "name": tool.name, "call_id": callID, "arguments": tool.args},
			map[string]any{"type": "function_call_output", "name": tool.name, "call_id": callID, "output": tool.output, "is_error": false},
		)
	}
	raw, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func answersByQuestion(answers []scorecardAnswer) map[string]scorecardAnswer {
	result := map[string]scorecardAnswer{}
	for _, answer := range answers {
		result[answer.Question] = answer
	}
	return result
}

func TestCodeChecksReadToolResultsAndReceipts(t *testing.T) {
	booked := syntheticTranscript(t,
		syntheticTool{"list_available_appointments", `{"visitType":"medical"}`, "success: Openings found.\nslot-1 – Monday"},
		syntheticTool{"book_appointment", `{"appointmentSlotRef":"slot-1"}`, "success: Booked."},
	)
	answers := answersByQuestion(codeCheckAnswers(booked, map[string]any{"domainOutcomes": []any{
		map[string]any{"callId": "call-b", "outcome": "booked", "status": "success"},
	}}))
	if !answers["scheduling_tool_called"].Answer || !answers["scheduling_succeeded"].Answer || answers["booking_blocked"].Answer {
		t.Fatalf("receipt-backed booking: %+v", answers)
	}
	if _, found := answers["insurance_verified"]; found {
		t.Fatal("insurance check must not apply without check_insurance")
	}

	claimOnly := answersByQuestion(codeCheckAnswers(booked, map[string]any{}))
	if claimOnly["scheduling_succeeded"].Answer {
		t.Fatal("a success message without a receipt is not a converted booking")
	}

	blocked := answersByQuestion(codeCheckAnswers(syntheticTranscript(t,
		syntheticTool{"check_insurance", `{"plan":"Synthetic Vision Plus","coverageType":"routine_vision"}`, "blocked: This plan is not accepted for this visit type at this office."},
	), map[string]any{}))
	if !blocked["booking_blocked"].Answer || blocked["booking_blocked"].Detail["reason"] != "Insurance not accepted" {
		t.Fatalf("insurance block: %+v", blocked["booking_blocked"])
	}
	if _, found := blocked["scheduling_succeeded"]; found {
		t.Fatal("B3 applies only after a scheduling write")
	}
	insurance := blocked["insurance_verified"]
	if insurance.Answer || !reflect.DeepEqual(insurance.Detail, map[string]any{"plan": "Synthetic Vision Plus", "result": "blocked: This plan is not accepted for this visit type at this office."}) {
		t.Fatalf("unverified insurance detail: %+v", insurance)
	}

	failure := answersByQuestion(codeCheckAnswers(syntheticTranscript(t,
		syntheticTool{"list_available_appointments", `{}`, "blocked: Availability could not be verified; this does not mean no openings."},
	), map[string]any{}))
	if failure["booking_blocked"].Answer {
		t.Fatal("a system failure is not a legitimate block")
	}

	retried := answersByQuestion(codeCheckAnswers(syntheticTranscript(t,
		syntheticTool{"check_insurance", `{"plan":"Synthetic Basic"}`, "blocked: This plan is not accepted for this visit type at this office."},
		syntheticTool{"check_insurance", `{"plan":"Synthetic Gold"}`, "success: Yes, we accept Synthetic Gold."},
		syntheticTool{"list_available_appointments", `{}`, "no_results: No eligible openings in the searched window."},
		syntheticTool{"list_available_appointments", `{"startDate":"2026-10-21"}`, "success: Found eligible openings."},
	), map[string]any{}))
	if retried["booking_blocked"].Answer {
		t.Fatalf("a later successful check or search clears an earlier block: %+v", retried["booking_blocked"])
	}

	verified := answersByQuestion(codeCheckAnswers(syntheticTranscript(t,
		syntheticTool{"check_insurance", `{"plan":"Synthetic"}`, "needs_input: Which of these is on your card: A or B?"},
		syntheticTool{"check_insurance", `{"plan":"Synthetic A"}`, "success: Yes, we accept Synthetic A."},
	), map[string]any{}))
	if !verified["insurance_verified"].Answer || verified["insurance_verified"].Detail != nil {
		t.Fatalf("last insurance check decides: %+v", verified["insurance_verified"])
	}
}

func TestAvailabilitySearchedNeedsAReturnedResult(t *testing.T) {
	unanswered, _ := json.Marshal(map[string]any{"items": []any{
		map[string]any{"type": "function_call", "name": "list_available_appointments", "call_id": "call-a", "arguments": "{}"},
	}})
	if answer := answersByQuestion(codeCheckAnswers(unanswered, map[string]any{}))["availability_searched"]; answer.Answer {
		t.Fatal("a search with no returned result is not a search, matching the jury's time_offered gate")
	}
	returned := syntheticTranscript(t, syntheticTool{"list_available_appointments", `{}`, "no_results: No eligible openings in the searched window."})
	if answer := answersByQuestion(codeCheckAnswers(returned, map[string]any{}))["availability_searched"]; !answer.Answer {
		t.Fatal("a returned search counts, even with no openings")
	}
}

func TestNoResultsRetryCheck(t *testing.T) {
	noMatch := syntheticTool{"resolve_patient", `{"firstName":"Synthetic"}`, "no_results: A complete search found no matching patient."}
	transfer := syntheticTool{"transfer_call", `{}`, "success: Transferring."}
	for name, test := range map[string]struct {
		tools   []syntheticTool
		applies bool
		want    bool
	}{
		"transfer right after no_results": {[]syntheticTool{noMatch, transfer}, true, false},
		"retried before transfer":         {[]syntheticTool{noMatch, noMatch, transfer}, true, true},
		"no transfer":                     {[]syntheticTool{noMatch}, false, false},
		"transfer without no_results":     {[]syntheticTool{transfer}, false, false},
	} {
		answer, found := answersByQuestion(codeCheckAnswers(syntheticTranscript(t, test.tools...), map[string]any{}))["no_results_retried"]
		if found != test.applies || answer.Answer != test.want {
			t.Errorf("%s: found=%v answer=%v", name, found, answer.Answer)
		}
	}
}

func TestJudgeAnswersReadTheJuryVerdict(t *testing.T) {
	evaluation := json.RawMessage(`{
		"evaluator":"jury","evaluatorVersion":"typesafe-scorecard-v7","jurors":["typesafe-ai/jev","liquid/d1"],"status":"incomplete",
		"results":{
			"new_booking_requested":{"verdict":true,"probability":0.91,"votes":{"typesafe-ai/jev":0.91,"liquid/d1":0.91},"errors":{}},
			"office_question_answered":{"verdict":false,"probability":0.4,"votes":{"typesafe-ai/jev":0.7,"liquid/d1":0.1},"errors":{}},
			"time_offered":{"status":"not_applicable","reason":"no_availability_result"},
			"requested_time_honored":{"status":"not_applicable","reason":"no_availability_result"},
			"insurance_plan_as_stated":{"status":"not_applicable","reason":"no_insurance_check"},
			"expressed_sentiment":{"score":3,"probabilities":{"3":1},"model":"typesafe-ai/jev"}
		},
		"errors":{"promises_backed":{"cause":"no_quorum","votes":{},"errors":{"typesafe-ai/jev":"TimeoutError"}}}
	}`)
	answers := answersByQuestion(judgeAnswers(evaluation))
	if len(answers) != 2 || !answers["new_booking_requested"].Answer || answers["office_question_answered"].Answer {
		t.Fatalf("the jury verdict is the answer: %+v", answers)
	}
	if votes, _ := answers["office_question_answered"].Detail["votes"].(map[string]float64); votes["liquid/d1"] != 0.1 {
		t.Fatalf("each juror's vote is kept: %+v", answers["office_question_answered"].Detail)
	}
	if sentiment := jurySentiment(evaluation); sentiment == nil || *sentiment != 3 {
		t.Fatalf("sentiment: %v", sentiment)
	}
	reading := readEvaluation(evaluation)
	if !reading.Evaluated || len(reading.Flags) != 1 || reading.Flags[0].Check != "office_question_answered" || reading.Sentiment == nil {
		t.Fatalf("a no verdict on a failure question flags the call, a booking no does not: %+v", reading)
	}
	for _, gated := range []string{"time_offered", "requested_time_honored", "insurance_plan_as_stated"} {
		if skipped, found := answers[gated]; found {
			t.Fatalf("a question the jury skipped as not applicable has no answer: %+v", skipped)
		}
	}
	if answers["new_booking_requested"].JudgeModel != "typesafe-ai/jev,liquid/d1" || *answers["new_booking_requested"].Probability != 0.91 {
		t.Fatalf("judge provenance: %+v", answers["new_booking_requested"])
	}
	if got := judgeAnswers(json.RawMessage(`{"evaluatorVersion":"typesafe-scorecard-v5","model":"typesafe-ai/jev","status":"complete","results":{"office_rules_grounded":{"answers":{"office_rules_grounded":{"type":"noul","noul":0.9}}}}}`)); len(got) != 0 {
		t.Fatalf("older scorecards are not the yes/no scorecard: %+v", got)
	}
}

func TestPreviousJuryStillReadsForDisplayButNotAsAnswers(t *testing.T) {
	evaluation := json.RawMessage(`{"evaluator":"jury","evaluatorVersion":"typesafe-scorecard-v6","jurors":["typesafe-ai/jev"],"status":"complete","results":{
		"booking_requested":{"verdict":true,"probability":0.9,"votes":{"typesafe-ai/jev":0.9},"errors":{}},
		"clear_and_responsive":{"verdict":false,"probability":0.2,"votes":{"typesafe-ai/jev":0.2},"errors":{}},
		"expressed_sentiment":{"score":1,"probabilities":{"1":1},"model":"typesafe-ai/jev"}}}`)
	reading := readEvaluation(evaluation)
	if !reading.Evaluated || reading.Version != "typesafe-scorecard-v6" || len(reading.Flags) != 1 || reading.Flags[0].Reason != "Clear and responsive needs review" || reading.Sentiment == nil || *reading.Sentiment != 1 {
		t.Fatalf("v6 calls keep their flags, evaluated count, and sentiment: %+v", reading)
	}
	if got := judgeAnswers(evaluation); len(got) != 0 {
		t.Fatalf("v6 verdicts are not recorded as v7 scorecard answers: %+v", got)
	}
}

func agentTranscript(t *testing.T, said ...string) json.RawMessage {
	t.Helper()
	items := []any{map[string]any{"type": "message", "role": "user", "content": []string{"Synthetic caller turn."}}}
	for _, text := range said {
		items = append(items, map[string]any{"type": "message", "role": "assistant", "content": []string{text}})
	}
	raw, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestKnowledgeAnsweredCheck(t *testing.T) {
	search := func(query, output string) syntheticTool {
		return syntheticTool{"search_office_knowledge", `{"query":"` + query + `"}`, output}
	}
	for name, test := range map[string]struct {
		tools   []syntheticTool
		applies bool
		want    bool
		queries any
	}{
		"no search":             {nil, false, false, nil},
		"answered":              {[]syntheticTool{search("What are the hours?", "success: Open 8 to 5 on weekdays.")}, true, true, nil},
		"other office phone":    {[]syntheticTool{search("Where are your offices?", "success: Spring Hill: Synthetic Eye, 1 Main St. Phone number not supplied.")}, true, true, nil},
		"answer with a gap too": {[]syntheticTool{search("How do I pay?", "success: Payment is handled by billing. Financing options such as CareCredit are not supplied.")}, true, true, nil},
		"only not supplied":     {[]syntheticTool{search("Is there parking?", "success: Directions, parking, and landmark details are not supplied for this office.")}, true, false, []any{"Is there parking?"}},
		"no results":            {[]syntheticTool{search("Do you do LASIK?", "no_results: No information was found for this question. This does not establish that the service is unavailable.")}, true, false, []any{"Do you do LASIK?"}},
		"outage is not a gap":   {[]syntheticTool{search("What are the hours?", "blocked: Office information could not be checked. Offer staff help if needed.")}, true, true, nil},
		"one of two missing":    {[]syntheticTool{search("What are the hours?", "success: Open 8 to 5."), search("Do you take CareCredit?", "success: Financing options such as CareCredit are not supplied.")}, true, false, []any{"Do you take CareCredit?"}},
	} {
		answer, found := answersByQuestion(codeCheckAnswers(syntheticTranscript(t, test.tools...), map[string]any{}))["knowledge_answered"]
		if found != test.applies || answer.Answer != test.want || !reflect.DeepEqual(answer.Detail["queries"], test.queries) {
			t.Errorf("%s: found=%v answer=%v detail=%v", name, found, answer.Answer, answer.Detail)
		}
	}
}

func TestTransferNotRefusedCheck(t *testing.T) {
	for name, test := range map[string]struct {
		said []string
		want bool
	}{
		"transfers":            {[]string{"One moment, I'll transfer you to the front desk."}, true},
		"can transfer":         {[]string{"I can transfer you now if you'd like."}, true},
		"does not transfer":    {[]string{"Sorry, I don't transfer calls, but I can take a message."}, false},
		"cannot transfer":      {[]string{"I can't transfer you right now."}, false},
		"curly apostrophe":     {[]string{"I’m unable to transfer the call."}, false},
		"later refusal counts": {[]string{"Hello, how can I help?", "We do not transfer calls to billing."}, false},
		"records, not a call":  {[]string{"We can't transfer your records over the phone."}, true},
		"prescription":         {[]string{"I can't transfer your prescription to another pharmacy."}, true},
		"spanish cannot":       {[]string{"Lo siento, no puedo transferir la llamada."}, false},
		"spanish cannot, bare": {[]string{"Lo siento, no puedo transferirle."}, false},
		"spanish do not":       {[]string{"No transfiero llamadas, pero puedo tomar un mensaje."}, false},
		"spanish records":      {[]string{"No podemos transferir su receta por teléfono."}, true},
		"spanish transfers":    {[]string{"Le transfiero la llamada ahora."}, true},
	} {
		answer, found := answersByQuestion(codeCheckAnswers(agentTranscript(t, test.said...), map[string]any{}))["transfer_not_refused"]
		if !found || answer.Answer != test.want {
			t.Errorf("%s: found=%v answer=%v", name, found, answer.Answer)
		}
	}
	if _, found := answersByQuestion(codeCheckAnswers(syntheticTranscript(t), map[string]any{}))["transfer_not_refused"]; found {
		t.Error("a call with no agent message has nothing to check")
	}
}

func TestInsuranceResolvedCheck(t *testing.T) {
	ask := syntheticTool{"check_insurance", `{"plan":"Synthetic"}`, "Ask what insurance plan is on the card."}
	choose := syntheticTool{"check_insurance", `{"plan":"Synthetic Medicaid"}`, "Ask which plan is on the card: Synthetic A, Synthetic B."}
	accepted := syntheticTool{"check_insurance", `{"plan":"Synthetic A"}`, "Synthetic A is accepted for medical visits at this office."}
	for name, test := range map[string]struct {
		tools   []syntheticTool
		applies bool
		want    bool
	}{
		"no insurance check": {nil, false, false},
		"accepted":           {[]syntheticTool{accepted}, true, true},
		"asked twice":        {[]syntheticTool{ask, choose, accepted}, true, true},
		"asked three times":  {[]syntheticTool{ask, choose, ask, accepted}, true, false},
	} {
		answer, found := answersByQuestion(codeCheckAnswers(syntheticTranscript(t, test.tools...), map[string]any{}))["insurance_resolved"]
		if found != test.applies || answer.Answer != test.want {
			t.Errorf("%s: found=%v answer=%v", name, found, answer.Answer)
		}
	}
}

func TestScorecardCatalogHasEveryQuestionOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, question := range ScorecardQuestions {
		if seen[question.Key] || seen[question.Code] || question.Prompt == "" || question.Question == "" || question.Yes == "" || question.No == "" {
			t.Fatalf("catalog entry %+v is duplicated or incomplete", question)
		}
		seen[question.Key], seen[question.Code] = true, true
	}
	for _, key := range []string{"knowledge_available", "office_question_answered", "appointment_datetime_correct", "call_ended_correctly", "transfer_policy_followed", "emergency_policy_followed"} {
		if !seen[key] {
			t.Errorf("catalog is missing %s", key)
		}
	}
	if scorecardQuestionByKey["knowledge_answered"].Failure {
		t.Error("A3 office_question_answered is the failure question for knowledge gaps; K1 must not double-flag")
	}
}

func TestStaffScorecardFollowsTheJuryMajorityNotTheMean(t *testing.T) {
	scorecard := agentCallScorecard(json.RawMessage(`{"evaluator":"jury","evaluatorVersion":"typesafe-scorecard-v7","jurors":["a","b","c"],"status":"complete","results":{
		"promises_backed":{"verdict":false,"probability":0.4667,"votes":{"a":0.1,"b":0.3,"c":1.0},"errors":{}},
		"ended_while_caller_active":{"verdict":true,"probability":0.3,"votes":{"a":0.45,"b":0.45,"c":0.0},"errors":{}}}}`))
	if len(scorecard) != 2 || scorecard[0].Answer != "no" || scorecard[1].Answer != "yes" {
		t.Fatalf("a 1-2 split with a high mean is no, and a 2-1 yes with a low mean is yes: %+v", scorecard)
	}
}

func TestStaffScorecardShowsVerdictsInCatalogOrder(t *testing.T) {
	scorecard := agentCallScorecard(json.RawMessage(`{"evaluator":"jury","evaluatorVersion":"typesafe-scorecard-v7","jurors":["typesafe-ai/jev"],"status":"complete","results":{
		"promises_backed":{"verdict":false,"probability":0.2,"votes":{"typesafe-ai/jev":0.2},"errors":{}},
		"office_question_answered":{"verdict":false,"probability":0.35,"votes":{"typesafe-ai/jev":0.35},"errors":{}},
		"new_booking_requested":{"verdict":true,"probability":0.9,"votes":{"typesafe-ai/jev":0.9},"errors":{}}}}`))
	if len(scorecard) != 3 || scorecard[0].Code != "B1" || scorecard[0].Answer != "yes" || scorecard[1].Answer != "no" || scorecard[2].Answer != "no" || scorecard[1].Prompt == "" {
		t.Fatalf("staff scorecard: %+v", scorecard)
	}
	if got := agentCallScorecard(nil); len(got) != 0 || got == nil {
		t.Fatalf("no evaluation is an empty scorecard: %+v", got)
	}
}

func TestJuryScorecardVersionsDefineReadJuryAndFailureKeys(t *testing.T) {
	for n := 1; n <= 10; n++ {
		version := fmt.Sprintf("typesafe-scorecard-v%d", n)
		raw, err := json.Marshal(map[string]any{"evaluatorVersion": version, "status": "complete", "jurors": []string{"synthetic"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, accepted := readJury(raw); accepted != slices.Contains(juryScorecardVersions, version) {
			t.Errorf("readJury(%s) accepted=%v, want %v", version, accepted, !accepted)
		}
	}
	keys := judgeFailureKeys("")
	for _, version := range juryScorecardVersions {
		if !slices.ContainsFunc(keys, func(key string) bool { return strings.HasPrefix(key, version+":") }) {
			t.Errorf("judgeFailureKeys has no key for %s: %v", version, keys)
		}
	}
}
