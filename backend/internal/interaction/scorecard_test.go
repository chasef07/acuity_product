package interaction

import (
	"encoding/json"
	"fmt"
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
		"evaluator":"jury","evaluatorVersion":"typesafe-scorecard-v6","jurors":["typesafe-ai/jev","liquid/d1"],"status":"incomplete",
		"results":{
			"booking_requested":{"verdict":true,"probability":0.91,"votes":{"typesafe-ai/jev":0.91,"liquid/d1":0.91},"errors":{}},
			"clear_and_responsive":{"verdict":false,"probability":0.4,"votes":{"typesafe-ai/jev":0.7,"liquid/d1":0.1},"errors":{}},
			"time_offered":{"status":"not_applicable","reason":"no_availability_result"},
			"expressed_sentiment":{"score":3,"probabilities":{"3":1},"model":"typesafe-ai/jev"}
		},
		"errors":{"right_help":{"cause":"no_quorum","votes":{},"errors":{"typesafe-ai/jev":"TimeoutError"}}}
	}`)
	answers := answersByQuestion(judgeAnswers(evaluation))
	if len(answers) != 3 || !answers["booking_requested"].Answer || answers["clear_and_responsive"].Answer {
		t.Fatalf("the jury verdict is the answer: %+v", answers)
	}
	if votes, _ := answers["clear_and_responsive"].Detail["votes"].(map[string]float64); votes["liquid/d1"] != 0.1 {
		t.Fatalf("each juror's vote is kept: %+v", answers["clear_and_responsive"].Detail)
	}
	if sentiment := jurySentiment(evaluation); sentiment == nil || *sentiment != 3 {
		t.Fatalf("sentiment: %v", sentiment)
	}
	reading := readEvaluation(evaluation)
	if !reading.Evaluated || len(reading.Flags) != 1 || reading.Flags[0].Check != "clear_and_responsive" || reading.Sentiment == nil {
		t.Fatalf("a no verdict on a failure question flags the call, a booking no does not: %+v", reading)
	}
	if skipped := answers["time_offered"]; skipped.Answer || skipped.Probability != nil || skipped.Detail["reason"] != "no_availability_result" {
		t.Fatalf("B5 skipped for lack of availability is a no without a probability: %+v", skipped)
	}
	if answers["booking_requested"].JudgeModel != "typesafe-ai/jev,liquid/d1" || *answers["booking_requested"].Probability != 0.91 {
		t.Fatalf("judge provenance: %+v", answers["booking_requested"])
	}
	if got := judgeAnswers(json.RawMessage(`{"evaluatorVersion":"typesafe-scorecard-v5","model":"typesafe-ai/jev","status":"complete","results":{"office_rules_grounded":{"answers":{"office_rules_grounded":{"type":"noul","noul":0.9}}}}}`)); len(got) != 0 {
		t.Fatalf("older scorecards are not the yes/no scorecard: %+v", got)
	}
}

func TestClassifyBooking(t *testing.T) {
	for want, answers := range map[bookingOutcome]map[string]bool{
		bookingNotRequested: {"scheduling_succeeded": true},
		bookingConverted:    {"booking_requested": true, "scheduling_tool_called": true, "scheduling_succeeded": true, "booking_blocked": true},
		bookingBlocked:      {"booking_requested": true, "booking_blocked": true},
		bookingMissed:       {"booking_requested": true},
		bookingAttempted:    {"booking_requested": true, "time_offered": true},
	} {
		if got := classifyBooking(answers); got != want {
			t.Errorf("classifyBooking(%v) = %q, want %q", answers, got, want)
		}
	}
}

func TestReviewFlaggedIgnoresBookingNos(t *testing.T) {
	missed := map[string]bool{"booking_requested": true, "scheduling_tool_called": false, "time_offered": false, "need_understood": true}
	if reviewFlagged(missed, nil) {
		t.Fatal("a booking no alone must not flag a call for review")
	}
	if !reviewFlagged(map[string]bool{"booking_requested": true, "right_help": false}, nil) {
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

func TestStaffTaskIdentifiedCheck(t *testing.T) {
	saved := func(draft, patient string) syntheticTool {
		return syntheticTool{"save_staff_task", `{}`, "saved: Draft saved for submission when the call ends. Not yet sent.\nDraft ID: " + draft + "\nRequest: Synthetic request\nPatient: " + patient}
	}
	for name, test := range map[string]struct {
		tools   []syntheticTool
		applies bool
		want    bool
	}{
		"no staff task":        {nil, false, false},
		"identified patient":   {[]syntheticTool{saved("d1", "Synthetic Sample (unverified).")}, true, true},
		"no patient":           {[]syntheticTool{saved("d1", "not identified.")}, true, false},
		"updated to a patient": {[]syntheticTool{saved("d1", "not identified."), saved("d1", "Synthetic Sample (verified).")}, true, true},
		"cancelled anonymous":  {[]syntheticTool{saved("d1", "not identified."), {"save_staff_task", `{"cancel":true}`, "cancelled: Request cancelled. It will not be submitted.\nDraft ID: d1"}}, false, false},
		"one of two anonymous": {[]syntheticTool{saved("d1", "Synthetic Sample (verified)."), saved("d2", "not identified.")}, true, false},
	} {
		answer, found := answersByQuestion(codeCheckAnswers(syntheticTranscript(t, test.tools...), map[string]any{}))["staff_task_identified"]
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
	for _, key := range []string{"person_request_honored", "staff_task_identified"} {
		if !seen[key] {
			t.Errorf("catalog is missing %s", key)
		}
	}
}

func TestTrustWeightsEachSampleByItsShareOfTheDaysCalls(t *testing.T) {
	no, yes := false, true
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	calls := map[string]*trustCall{
		"flagged-caught": {answers: map[string]bool{"kyle": false}, judge: &no, sample: "flagged", date: "2026-10-08", reviewedAt: now},
		"flagged-alarm":  {answers: map[string]bool{"kyle": true}, judge: &no, sample: "flagged", date: "2026-10-08", reviewedAt: now},
		"random-missed":  {answers: map[string]bool{"kyle": false}, judge: &yes, sample: "random", date: "2026-10-08", reviewedAt: now},
		"random-fine":    {answers: map[string]bool{"kyle": true}, judge: &yes, sample: "random", date: "2026-10-08", reviewedAt: now},
		"imported":       {answers: map[string]bool{"kyle": false}, judge: &yes, sample: "manual", date: "2026-10-01", reviewedAt: now},
	}
	estimate := estimateTrust(calls, map[string]reviewPool{"2026-10-08": {flagged: 20, random: 180}}, now)
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
		calls[id] = &trustCall{answers: answers, judge: verdict, sample: "random", date: "2026-10-08", reviewedAt: now}
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
	pools := map[string]reviewPool{"2026-10-08": {random: 65}}
	if estimate := estimateTrust(calls, pools, now); estimate.Status != trustStatusCollected || *estimate.Catch != 1 {
		t.Fatalf("5 of 5 caught is not yet proof of 90%%: %+v", estimate)
	}
	for index := 5; index < 20; index++ {
		add(fmt.Sprintf("fail-%d", index), false, false, "kyle")
	}
	pools["2026-10-08"] = reviewPool{random: 80}
	if estimate := estimateTrust(calls, pools, now); estimate.Status != trustStatusCollected {
		t.Fatalf("20 of 20 caught only shows at least 84%%, short of 90%%: %+v", estimate)
	}
	for index := 20; index < 40; index++ {
		add(fmt.Sprintf("fail-%d", index), false, false, "kyle")
	}
	for index := 60; index < 140; index++ {
		add(fmt.Sprintf("fine-%d", index), true, true, "kyle")
	}
	pools["2026-10-08"] = reviewPool{random: 180}
	if estimate := estimateTrust(calls, pools, now); estimate.Status != trustStatusTrusted {
		t.Fatalf("40 of 40 caught with reviewers in agreement is trusted: %+v", estimate)
	}
}

func TestStaffScorecardShowsVerdictsInCatalogOrder(t *testing.T) {
	scorecard := agentCallScorecard(json.RawMessage(`{"evaluator":"jury","evaluatorVersion":"typesafe-scorecard-v6","jurors":["typesafe-ai/jev"],"status":"complete","results":{
		"right_help":{"verdict":false,"probability":0.2,"votes":{"typesafe-ai/jev":0.2},"errors":{}},
		"booking_requested":{"verdict":true,"probability":0.9,"votes":{"typesafe-ai/jev":0.9},"errors":{}}}}`))
	if len(scorecard) != 2 || scorecard[0].Code != "B1" || !scorecard[0].Verdict || scorecard[1].Code != "H2" || scorecard[1].Verdict || scorecard[1].Prompt == "" {
		t.Fatalf("staff scorecard: %+v", scorecard)
	}
	if got := agentCallScorecard(nil); len(got) != 0 || got == nil {
		t.Fatalf("no evaluation is an empty scorecard: %+v", got)
	}
}
