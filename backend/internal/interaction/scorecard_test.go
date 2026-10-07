package interaction

import (
	"encoding/json"
	"fmt"
	"reflect"
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

func TestJudgeAnswersReadOnlyScorecardV6(t *testing.T) {
	evaluation := json.RawMessage(`{
		"evaluatorVersion":"typesafe-scorecard-v6","model":"typesafe-ai/jev","status":"incomplete",
		"results":{
			"booking_requested":{"answers":{"booking_requested":{"type":"noul","noul":0.91}}},
			"clear_and_responsive":{"answers":{"clear_and_responsive":{"type":"noul","noul":0.40}}},
			"time_offered":{"status":"not_applicable","reason":"no_availability_result"},
			"expressed_sentiment":{"answers":{"expressed_sentiment":{"type":"score","score":3}}}
		},
		"errors":{"right_help":{"cause":"TimeoutError"}}
	}`)
	answers := answersByQuestion(judgeAnswers(evaluation))
	if len(answers) != 2 || !answers["booking_requested"].Answer || answers["clear_and_responsive"].Answer {
		t.Fatalf("v6 answers with 0.40 as no: %+v", answers)
	}
	if answers["booking_requested"].JudgeModel != "typesafe-ai/jev" || *answers["booking_requested"].Probability != 0.91 {
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
