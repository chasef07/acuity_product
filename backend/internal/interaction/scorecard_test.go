package interaction

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"
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

func TestClassifyBooking(t *testing.T) {
	for want, answers := range map[bookingOutcome]map[string]bool{
		bookingNotRequested: {"scheduling_succeeded": true, "availability_searched": false},
		bookingConverted:    {"new_booking_requested": true, "scheduling_tool_called": true, "scheduling_succeeded": true, "booking_blocked": true},
		bookingBlocked:      {"new_booking_requested": true, "booking_blocked": true},
		bookingMissed:       {"new_booking_requested": true, "availability_searched": true, "time_offered": false},
		bookingUnknown:      {"new_booking_requested": true},
		bookingAttempted:    {"new_booking_requested": true, "time_offered": true},
		bookingNotSearched:  {"new_booking_requested": true, "availability_searched": false, "time_offered": false},
	} {
		if got := classifyBooking(answers); got != want {
			t.Errorf("classifyBooking(%v) = %q, want %q", answers, got, want)
		}
	}
}

func TestReviewAsksGatedQuestionsOnlyWhenTheyApply(t *testing.T) {
	for name, test := range map[string]struct {
		facts map[string]bool
		want  string
	}{
		"no tools":           {map[string]bool{"availability_searched": false, "scheduling_tool_called": false}, "[new_booking_requested office_rules_grounded office_question_answered promises_backed ended_while_caller_active]"},
		"availability":       {map[string]bool{"availability_searched": true}, "[new_booking_requested time_offered requested_time_honored office_rules_grounded office_question_answered promises_backed ended_while_caller_active]"},
		"scheduling write":   {map[string]bool{"scheduling_tool_called": true}, "[new_booking_requested office_rules_grounded appointment_datetime_correct office_question_answered promises_backed ended_while_caller_active]"},
		"insurance rejected": {map[string]bool{"insurance_verified": false}, "[new_booking_requested office_rules_grounded office_question_answered promises_backed ended_while_caller_active insurance_plan_as_stated]"},
	} {
		if got := fmt.Sprint(reviewQuestions(test.facts)); got != test.want {
			t.Errorf("%s: %s", name, got)
		}
	}
}

func TestBookingConversionCountsEachCallerNumberOnce(t *testing.T) {
	callers := map[string]bookingOutcome{}
	for _, call := range []struct {
		phone   string
		outcome bookingOutcome
	}{
		{"+15555550001", bookingNotSearched}, {"+15555550001", bookingConverted}, {"+15555550001", bookingMissed},
		{"+15555550002", bookingMissed}, {"+15555550002", bookingNotSearched},
		{"+15555550003", bookingNotSearched}, {"+15555550003", bookingNotRequested},
		{"+15555550004", bookingBlocked}, {"+15555550004", bookingNotSearched},
		{"+15555550005", bookingUnknown},
		{"+15555550006", bookingNotRequested},
	} {
		recordCallerBooking(callers, call.phone, call.outcome)
	}
	result := ScorecardResults{}
	result.countBookingCallers(callers)
	if result.BookingCallers != 4 || result.Converted != 1 || result.Missed != 1 || result.NotSearched != 1 || result.Blocked != 1 || *result.Conversion != 1.0/3.0 {
		t.Fatalf("one outcome per caller number, any booked call converts it, blocked numbers leave the denominator: %+v", result)
	}
	if !scorecardProblem(map[string]bool{"new_booking_requested": true, "availability_searched": false}) {
		t.Fatal("a new-booking caller with no availability search is a problem call")
	}
}

func TestReviewFlaggedIgnoresBookingNos(t *testing.T) {
	missed := map[string]bool{"new_booking_requested": true, "scheduling_tool_called": false, "time_offered": false, "ended_while_caller_active": true}
	if reviewFlagged(missed, nil) {
		t.Fatal("a booking no alone must not flag a call for review")
	}
	if !reviewFlagged(map[string]bool{"new_booking_requested": true, "promises_backed": false}, nil) {
		t.Fatal("a failure-question no must flag a call for review")
	}
}

func TestParseGoldenSetJudgeAnswer(t *testing.T) {
	for value, want := range map[string]string{"yes (0.82)": "true 0.82", "NO (0.05)": "false 0.05", "error": "<nil> <nil>", "no": "false <nil>"} {
		answer, probability := parseJudgeAnswer(value)
		got := "<nil>"
		if answer != nil {
			got = fmt.Sprint(*answer)
		}
		if probability != nil {
			got += " " + fmt.Sprint(*probability)
		} else {
			got += " <nil>"
		}
		if got != want {
			t.Errorf("parseJudgeAnswer(%q) = %s, want %s", value, got, want)
		}
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

func TestKnowledgeAnsweredIsAFactNotAFailure(t *testing.T) {
	if scorecardQuestionByKey["knowledge_answered"].Failure {
		t.Fatal("A3 office_question_answered is the failure question for knowledge gaps; K1 must not double-flag")
	}
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
	prefixed := syntheticTool{"check_insurance", `{"plan":"Synthetic"}`, "needs_input: Which of these is on your card: A or B?"}
	accepted := syntheticTool{"check_insurance", `{"plan":"Synthetic A"}`, "Synthetic A is accepted for medical visits at this office."}
	for name, test := range map[string]struct {
		tools   []syntheticTool
		applies bool
		want    bool
	}{
		"no insurance check": {nil, false, false},
		"accepted":           {[]syntheticTool{accepted}, true, true},
		"asked twice":        {[]syntheticTool{ask, choose, accepted}, true, true},
		"asked three times":  {[]syntheticTool{ask, choose, prefixed, accepted}, true, false},
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
	for _, key := range []string{"new_booking_requested", "time_offered", "promises_backed", "ended_while_caller_active", "office_rules_grounded", "office_question_answered", "appointment_datetime_correct", "insurance_plan_as_stated", "requested_time_honored"} {
		if !seen[key] {
			t.Errorf("catalog is missing %s", key)
		}
	}
}

func TestTrustWeightsEachSampleByItsShareOfTheDaysCalls(t *testing.T) {
	no, yes := false, true
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	calls := map[string]*trustCall{
		"flagged-caught": {answers: map[string]bool{"kyle": false}, judge: &no, sample: "flagged", date: "2026-10-08"},
		"flagged-alarm":  {answers: map[string]bool{"kyle": true}, judge: &no, sample: "flagged", date: "2026-10-08"},
		"random-missed":  {answers: map[string]bool{"kyle": false}, judge: &yes, sample: "random", date: "2026-10-08"},
		"random-fine":    {answers: map[string]bool{"kyle": true}, judge: &yes, sample: "random", date: "2026-10-08"},
		"imported":       {answers: map[string]bool{"kyle": false}, judge: &yes, sample: "manual", date: "2026-10-01"},
	}
	estimate := estimateTrust(calls, map[string]reviewPool{"2026-10-08": {flagged: 20, random: 180, reviewedFlagged: 2, reviewedRandom: 2}}, now)
	if estimate.Calls != 4 || estimate.Failures != 2 {
		t.Fatalf("only queue samples with a recorded pool count: %+v", estimate)
	}
	if *estimate.Catch != 10.0/100.0 {
		t.Fatalf("a miss among 180 unflagged calls outweighs a catch among 20 flagged: catch %v", *estimate.Catch)
	}
	if *estimate.Agreement != 100.0/200.0 || estimate.Status != trustStatusBelow {
		t.Fatalf("weighted agreement and verdict: %+v", estimate)
	}
}

func TestTrustWeightsByEveryReviewedCallNotOnlyWhereTheQuestionApplied(t *testing.T) {
	no, yes := false, true
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	calls := map[string]*trustCall{}
	for index := 0; index < 8; index++ {
		calls[fmt.Sprintf("flagged-%d", index)] = &trustCall{answers: map[string]bool{"kyle": false}, judge: &no, sample: "flagged", date: "2026-10-08"}
	}
	calls["random-missed"] = &trustCall{answers: map[string]bool{"kyle": false}, judge: &yes, sample: "random", date: "2026-10-08"}
	calls["random-fine"] = &trustCall{answers: map[string]bool{"kyle": true}, judge: &yes, sample: "random", date: "2026-10-08"}
	pools := map[string]reviewPool{"2026-10-08": {flagged: 20, random: 180, reviewedFlagged: 10, reviewedRandom: 10}}
	estimate := estimateTrust(calls, pools, now)
	if want := 16.0 / 34.0; math.Abs(*estimate.Catch-want) > 1e-9 {
		t.Fatalf("catch = %v, want %v (8 caught at weight 2 vs 1 missed at weight 18)", *estimate.Catch, want)
	}
}

func TestTrustNeedsBoundsNotJustPointEstimates(t *testing.T) {
	no, yes := false, true
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	calls := map[string]*trustCall{}
	add := func(id string, human, judge bool, reviewers ...string) {
		answers := map[string]bool{}
		for _, reviewer := range reviewers {
			answers[reviewer] = human
		}
		verdict := &yes
		if !judge {
			verdict = &no
		}
		calls[id] = &trustCall{answers: answers, judge: verdict, sample: "random", date: "2026-10-08"}
	}
	for index := 0; index < 5; index++ {
		add(fmt.Sprintf("fail-%d", index), false, false, "kyle")
	}
	for index := 0; index < 30; index++ {
		add(fmt.Sprintf("fine-%d", index), true, true, "kyle", "chase")
	}
	for index := 30; index < 60; index++ {
		add(fmt.Sprintf("fine-%d", index), true, true, "kyle")
	}
	pools := map[string]reviewPool{"2026-10-08": {random: 65, reviewedRandom: 65}}
	if estimate := estimateTrust(calls, pools, now); estimate.Status != trustStatusCollected || *estimate.Catch != 1 {
		t.Fatalf("5 of 5 caught is not yet proof of 90%%: %+v", estimate)
	}
	for index := 5; index < 20; index++ {
		add(fmt.Sprintf("fail-%d", index), false, false, "kyle")
	}
	pools["2026-10-08"] = reviewPool{random: 80, reviewedRandom: 80}
	if estimate := estimateTrust(calls, pools, now); estimate.Status != trustStatusCollected {
		t.Fatalf("20 of 20 caught only shows at least 84%%, short of 90%%: %+v", estimate)
	}
	for index := 20; index < 400; index++ {
		add(fmt.Sprintf("fail-%d", index), false, false, "kyle")
	}
	for index := 60; index < 600; index++ {
		add(fmt.Sprintf("fine-%d", index), true, true, "kyle")
	}
	pools["2026-10-08"] = reviewPool{random: 1000, reviewedRandom: 1000}
	if estimate := estimateTrust(calls, pools, now); estimate.Status != trustStatusTrusted {
		t.Fatalf("400 no answers with no false alarm prove 1%%: %+v", estimate)
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
