package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	ScorecardJudgeVersion = "typesafe-scorecard-v6"
	ScorecardCodeVersion  = "code-checks-v1"
	scorecardNoAtOrBelow  = 0.40
)

type ScorecardQuestion struct {
	Key         string `json:"key"`
	Code        string `json:"code"`
	Label       string `json:"label"`
	Group       string `json:"group"`
	Source      string `json:"source"`
	Question    string `json:"question"`
	Yes         string `json:"yes"`
	No          string `json:"no"`
	AppliesWhen string `json:"appliesWhen"`
}

var ScorecardQuestions = []ScorecardQuestion{
	{
		Key: "booking_requested", Code: "B1", Label: "Booking requested", Group: "Booking conversion", Source: "judge",
		Question:    "Did the caller ask to book a new appointment, or to reschedule or cancel an existing appointment, at any point in the call? Count a request for an appointment for someone else (for example a child). Asking about an existing appointment's time or location, confirming it, or asking about prescriptions, orders, insurance, or billing is not a booking request by itself.",
		Yes:         "The caller asked to book, reschedule, or cancel an appointment.",
		No:          "The caller never asked to book, reschedule, or cancel an appointment.",
		AppliesWhen: "Every reviewed call. A yes makes this a booking call.",
	},
	{
		Key: "scheduling_tool_called", Code: "B2", Label: "Scheduling tool called", Group: "Booking conversion", Source: "code",
		Question:    "Did the agent call the matching scheduling tool?",
		Yes:         "The agent called book_appointment, reschedule_appointment, or cancel_appointment.",
		No:          "The agent never attempted a booking, reschedule, or cancellation.",
		AppliesWhen: "Counted for booking calls (B1 yes).",
	},
	{
		Key: "scheduling_succeeded", Code: "B3", Label: "Scheduling succeeded", Group: "Booking conversion", Source: "code",
		Question:    "Did the scheduling tool return a success?",
		Yes:         "A booking, reschedule, or cancellation receipt came back successful. The call is converted.",
		No:          "No scheduling receipt came back successful.",
		AppliesWhen: "Only when a scheduling tool was called (B2 yes).",
	},
	{
		Key: "booking_blocked", Code: "B4", Label: "Blocked by a tool result", Group: "Booking conversion", Source: "code",
		Question:    "Did a tool result block it (no slots, insurance not accepted, policy)?",
		Yes:         "Availability returned no openings or no eligible providers, or the insurance check said the plan is not accepted or needs a referral or prior authorization. Blocked calls are left out of conversion.",
		No:          "No tool result blocked the booking. A system failure, such as availability that could not be verified, is not a block.",
		AppliesWhen: "Counted for booking calls (B1 yes) that did not convert.",
	},
	{
		Key: "time_offered", Code: "B5", Label: "Specific time offered", Group: "Booking conversion", Source: "judge",
		Question:    "Did the agent offer the caller at least one specific appointment date and time taken from an availability tool result? A vague promise, a staff request to find a time, or a time the caller proposed that the agent did not confirm as available does not count.",
		Yes:         "The agent offered at least one specific date and time from returned availability.",
		No:          "The agent never offered a specific available date and time.",
		AppliesWhen: "Only for booking calls (B1 yes). The judge only runs once availability was returned; otherwise the answer is no.",
	},
	{
		Key: "need_understood", Code: "H1", Label: "Understood every request", Group: "Right help", Source: "judge",
		Question:    "Did the agent correctly identify every request the caller made, including corrections and additional requests? First list each caller request, then check whether the agent's questions, tool calls, and statements address the request the caller actually made. Judge only whether the agent understood what was asked, not whether its answer was correct; factual accuracy is scored separately. Mishearing that the agent later corrected still counts as understood if the correction happened before any action was taken on the wrong understanding.",
		Yes:         "The agent correctly identified every caller request, even if an answer it gave was wrong.",
		No:          "The agent misunderstood, ignored, or acted on the wrong version of at least one caller request.",
		AppliesWhen: "Every reviewed call.",
	},
	{
		Key: "right_help", Code: "H2", Label: "Right help or next step", Group: "Right help", Source: "judge",
		Question:    "By the end of the call, did the caller get the right help for each request: either the request was completed (supported by a successful tool result), or the caller was given a correct, clearly explained next step, such as a saved staff request they were told about, or a transfer to staff when the agent could not handle the request? A transfer counts only if the agent could not reasonably handle the request itself. If the call ended before any request was resolved or handed off, answer false.",
		Yes:         "Every request was completed or handed off with a correct, clearly explained next step.",
		No:          "At least one request was left unresolved, handed off unnecessarily, or the next step was missing, wrong, or unclear.",
		AppliesWhen: "Every reviewed call.",
	},
	{
		Key: "clear_and_responsive", Code: "H4", Label: "Clear and responsive", Group: "Right help", Source: "judge",
		Question:    "Was the conversation clear and responsive from the caller's point of view? Answer false if the agent asked avoidable repeated questions, collected information it could not use for the caller's request, gave confusing or contradictory statements, or if the caller had to check whether the agent was still there or repeat themselves because the agent did not respond. These are not failures: a caller who asks for a person or does not want to talk to an AI and is transferred promptly (a brief offer to help first is fine); a transfer or staff request because a tool result says the agent cannot complete the request, such as insurance that cannot be verified; a short call; ordinary clarification of a hard-to-hear name or number, asked once or twice; caller-requested pauses.",
		Yes:         "The conversation was clear and responsive, including prompt transfers the caller asked for.",
		No:          "The conversation had avoidable repetition, wasted questions, confusing statements, or unresponsiveness.",
		AppliesWhen: "Every reviewed call.",
	},
	{
		Key: "office_rules_grounded", Code: "A1", Label: "Office facts backed", Group: "Accuracy and follow-through", Source: "judge",
		Question:    "Was every factual claim about office hours, whether the office is open, providers, locations, services, or policies supported by recorded office instructions or a successful knowledge result available BEFORE the claim? Check each claim, including claims in Spanish, against earlier evidence for the relevant office. A caller's suggestion, the agent's own statements, general knowledge, or an unrelated tool result is not supporting evidence. If even one claim lacks earlier support or contradicts it, answer false. For example, saying 'we are open until five today' without earlier supporting hours fails. A later lookup, correction, or otherwise grounded answer does not erase an earlier unsupported claim. Greetings, acknowledgments, and explicit statements that information is unknown are not factual office claims.",
		Yes:         "Every factual office claim has supporting evidence available before it was made, or no factual office claims were made.",
		No:          "At least one factual office claim lacks earlier supporting evidence or contradicts it, even if the rest of the call is grounded or the claim is later corrected.",
		AppliesWhen: "Every reviewed call.",
	},
	{
		Key: "appointment_datetime_correct", Code: "A2", Label: "Appointment matched the caller", Group: "Accuracy and follow-through", Source: "judge",
		Question:    "For every booking, rescheduling, or cancellation action, did the tool result match the caller's final intended appointment date and time? Use the final agreed date/time, including explicitly accepted alternatives, in the office timezone. For rescheduling check both the original appointment and the new date/time; for cancellation check the targeted appointment. Compare actual tool results, not the assistant's claim. Missing results cannot establish a match.",
		Yes:         "Every appointment action's tool result confirms the caller's intended date and time.",
		No:          "Any action targets or produces the wrong date/time, or there is insufficient evidence of a matching appointment action.",
		AppliesWhen: "Only when a scheduling tool ran (B2 yes).",
	},
	{
		Key: "no_results_retried", Code: "F2", Label: "Retried after no match", Group: "Accuracy and follow-through", Source: "code",
		Question:    "After a patient lookup returned no_results, did the agent clarify and retry before transferring?",
		Yes:         "The agent ran the patient lookup again before transferring the call.",
		No:          "The agent transferred without retrying the patient lookup.",
		AppliesWhen: "Only when a patient lookup returned no_results and the call was later transferred.",
	},
	{
		Key: "insurance_verified", Code: "I1", Label: "Insurance verified", Group: "Accuracy and follow-through", Source: "code",
		Question:    "Was the caller's insurance verified?",
		Yes:         "The last insurance check accepted the plan.",
		No:          "The last insurance check ended blocked or still needed information. This is not an agent failure; a task or transfer is fine. The plan name is recorded so staff can update the insurance rules.",
		AppliesWhen: "Only when the agent checked insurance.",
	},
}

var scorecardQuestionByKey = func() map[string]ScorecardQuestion {
	result := map[string]ScorecardQuestion{}
	for _, question := range ScorecardQuestions {
		result[question.Key] = question
	}
	return result
}()

type scorecardAnswer struct {
	Question    string
	Source      string
	Answer      bool
	Probability *float64
	Version     string
	JudgeModel  string
	Detail      map[string]any
}

type scorecardToolCall struct {
	Name      string
	Arguments map[string]any
	Output    string
	Status    string
	CallID    string
}

var (
	schedulingWrites = map[string]bool{"book_appointment": true, "reschedule_appointment": true, "cancel_appointment": true}
	blockingResults  = []struct{ tool, prefix, reason string }{
		{"list_available_appointments", "no_results:", "No openings in the searched window"},
		{"list_available_appointments", "blocked: No providers are eligible", "No eligible providers"},
		{"check_insurance", "blocked: This plan is not accepted", "Insurance not accepted"},
		{"check_insurance", "blocked: This office does not accept coverage", "Office does not accept this coverage"},
		{"check_insurance", "blocked: This plan requires prior authorization", "Prior authorization required"},
		{"check_insurance", "blocked: This plan requires a referral", "Referral required"},
		{"check_insurance", "blocked: We accept this plan, but none of its doctors", "No doctor for the patient's age"},
	}
)

func scorecardToolCalls(transcript json.RawMessage) []scorecardToolCall {
	items := transcriptItems(decodeRecord(transcript))
	outputs := map[string]map[string]any{}
	for _, value := range items {
		record := recordValue(value)
		if strings.EqualFold(firstRecordString(record, "type"), "function_call_output") {
			if callID := firstRecordString(record, "call_id", "callId"); callID != "" {
				outputs[callID] = record
			}
		}
	}
	calls := []scorecardToolCall{}
	for _, value := range items {
		record := recordValue(value)
		if !strings.EqualFold(firstRecordString(record, "type"), "function_call") {
			continue
		}
		call := scorecardToolCall{
			Name:      firstRecordString(record, "name"),
			CallID:    firstRecordString(record, "call_id", "callId"),
			Arguments: normalizedPayload(firstRecordValue(record, "arguments", "args")),
		}
		if call.Name == "" {
			continue
		}
		if output, found := outputs[call.CallID]; found && call.CallID != "" {
			switch typed := firstRecordValue(output, "output").(type) {
			case string:
				call.Output = strings.TrimSpace(typed)
			case nil:
			default:
				encoded, _ := json.Marshal(typed)
				call.Output = string(encoded)
			}
			if prefix, _, found := strings.Cut(call.Output, ":"); found && !strings.ContainsAny(prefix, " \n") {
				call.Status = strings.ToLower(prefix)
			}
		}
		calls = append(calls, call)
	}
	return calls
}

func codeCheckAnswers(transcript json.RawMessage, closeout map[string]any) []scorecardAnswer {
	calls := scorecardToolCalls(transcript)
	answer := func(question string, value bool, detail map[string]any) scorecardAnswer {
		return scorecardAnswer{Question: question, Source: "code", Answer: value, Version: ScorecardCodeVersion, Detail: detail}
	}
	receipts := domainOutcomeReceiptsByCallID(closeout)
	writeCalled, writeSucceeded := false, false
	lastOutput := map[string]string{}
	for _, call := range calls {
		lastOutput[call.Name] = call.Output
		if schedulingWrites[call.Name] {
			writeCalled = true
			receipt := receipts[call.CallID]
			if appointmentDomainOutcome(firstRecordString(receipt, "outcome")) && normalizedDomainStatus(firstRecordString(receipt, "status")) == "success" {
				writeSucceeded = true
			}
		}
	}
	var blocked map[string]any
	for _, rule := range blockingResults {
		if output, called := lastOutput[rule.tool]; blocked == nil && called && strings.HasPrefix(output, rule.prefix) {
			blocked = map[string]any{"reason": rule.reason, "tool": rule.tool}
		}
	}
	answers := []scorecardAnswer{answer("scheduling_tool_called", writeCalled, nil)}
	if writeCalled {
		answers = append(answers, answer("scheduling_succeeded", writeSucceeded, nil))
	}
	answers = append(answers, answer("booking_blocked", blocked != nil, blocked))

	noResults, retried := false, false
	for _, call := range calls {
		if call.Name == "resolve_patient" && noResults {
			retried = true
		} else if call.Name == "resolve_patient" && call.Status == "no_results" {
			noResults = true
		} else if call.Name == "transfer_call" && noResults {
			answers = append(answers, answer("no_results_retried", retried, nil))
			break
		}
	}

	var lastInsurance *scorecardToolCall
	for index := range calls {
		if calls[index].Name == "check_insurance" {
			lastInsurance = &calls[index]
		}
	}
	if lastInsurance != nil {
		verified := lastInsurance.Status == "success"
		var detail map[string]any
		if !verified {
			result, _, _ := strings.Cut(lastInsurance.Output, "\n")
			detail = map[string]any{"plan": strings.TrimSpace(anyString(lastInsurance.Arguments["plan"])), "result": result}
		}
		answers = append(answers, answer("insurance_verified", verified, detail))
	}
	return answers
}

func judgeAnswers(evaluation json.RawMessage) []scorecardAnswer {
	var scorecard struct {
		Version string                     `json:"evaluatorVersion"`
		Model   string                     `json:"model"`
		Status  string                     `json:"status"`
		Results map[string]json.RawMessage `json:"results"`
		Errors  map[string]json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(evaluation, &scorecard) != nil || scorecard.Version != ScorecardJudgeVersion ||
		(scorecard.Status != "complete" && scorecard.Status != "incomplete") || scorecard.Model == "" {
		return nil
	}
	answers := []scorecardAnswer{}
	for _, question := range ScorecardQuestions {
		if question.Source != "judge" {
			continue
		}
		if _, failed := scorecard.Errors[question.Key]; failed {
			continue
		}
		var result struct {
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Answers map[string]struct {
				Type string   `json:"type"`
				Noul *float64 `json:"noul"`
			} `json:"answers"`
		}
		if json.Unmarshal(scorecard.Results[question.Key], &result) != nil {
			continue
		}
		if result.Status == "not_applicable" {
			if question.Key == "time_offered" && result.Reason == "no_availability_result" {
				answers = append(answers, scorecardAnswer{
					Question: question.Key, Source: "judge", Answer: false, Version: scorecard.Version,
					JudgeModel: scorecard.Model, Detail: map[string]any{"reason": result.Reason},
				})
			}
			continue
		}
		value := result.Answers[question.Key]
		if value.Type != "noul" || value.Noul == nil || math.IsNaN(*value.Noul) || *value.Noul < 0 || *value.Noul > 1 {
			continue
		}
		probability := *value.Noul
		answers = append(answers, scorecardAnswer{
			Question:    question.Key,
			Source:      "judge",
			Answer:      probability > scorecardNoAtOrBelow,
			Probability: &probability,
			Version:     scorecard.Version,
			JudgeModel:  scorecard.Model,
		})
	}
	return answers
}

func scorecardAnswersFor(transcript, closeoutPayload json.RawMessage) []scorecardAnswer {
	closeout := decodeRecord(closeoutPayload)
	if len(transcriptItems(decodeRecord(transcript))) == 0 {
		return nil
	}
	evaluation, _ := json.Marshal(closeout["evaluation"])
	return append(codeCheckAnswers(transcript, closeout), judgeAnswers(evaluation)...)
}

func recordScorecard(ctx context.Context, tx pgx.Tx, interaction Interaction, computedAt time.Time) (int, error) {
	if _, err := tx.Exec(ctx, `DELETE FROM ai_interaction_scorecard_answers WHERE interaction_id = $1`, interaction.ID); err != nil {
		return 0, fmt.Errorf("clear scorecard answers: %w", err)
	}
	if interaction.LifecycleStage != LifecycleClosed {
		return 0, nil
	}
	agentVersion := nullIfEmpty(strings.TrimSpace(firstRecordString(recordValue(decodeRecord(interaction.CloseoutPayload)["versions"]), "agent")))
	if agentVersion == nil {
		agentVersion = nullIfEmpty(strings.TrimSpace(firstRecordString(decodeRecord(interaction.CloseoutPayload), "agentVersion")))
	}
	answers := scorecardAnswersFor(interaction.Transcript, interaction.CloseoutPayload)
	for _, answer := range answers {
		var detail any
		if answer.Detail != nil {
			detail = answer.Detail
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ai_interaction_scorecard_answers (
				interaction_id, practice_id, question, source, answer, probability,
				scorecard_version, judge_model, agent_version, detail, computed_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`, interaction.ID, interaction.PracticeID, answer.Question, answer.Source, answer.Answer, answer.Probability,
			answer.Version, nullIfEmpty(answer.JudgeModel), agentVersion, detail, computedAt); err != nil {
			return 0, fmt.Errorf("record scorecard answer: %w", err)
		}
	}
	return len(answers), nil
}

type bookingOutcome string

const (
	bookingNotRequested bookingOutcome = ""
	bookingConverted    bookingOutcome = "converted"
	bookingBlocked      bookingOutcome = "blocked"
	bookingMissed       bookingOutcome = "missed"
	bookingAttempted    bookingOutcome = "attempted"
)

func classifyBooking(answers map[string]bool) bookingOutcome {
	switch {
	case !answers["booking_requested"]:
		return bookingNotRequested
	case answers["scheduling_succeeded"]:
		return bookingConverted
	case answers["booking_blocked"]:
		return bookingBlocked
	case !answers["scheduling_tool_called"] && !answers["time_offered"]:
		return bookingMissed
	default:
		return bookingAttempted
	}
}

type ScorecardBackfill struct {
	Calls   int `json:"calls"`
	Answers int `json:"answers"`
}

func (m *Module) BackfillScorecards(ctx context.Context, since time.Time, batch int) (ScorecardBackfill, error) {
	if err := m.available(); err != nil {
		return ScorecardBackfill{}, err
	}
	if batch < 1 {
		return ScorecardBackfill{}, ErrInvalidInput
	}
	result := ScorecardBackfill{}
	cursorStarted, cursorID := since, "00000000-0000-0000-0000-000000000000"
	for {
		tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return result, fmt.Errorf("begin scorecard backfill: %w", err)
		}
		rows, err := tx.Query(ctx, interactionSelect+`
			WHERE interaction.lifecycle_stage = 3 AND (interaction.started_at, interaction.id) > ($1, $2::uuid)
			ORDER BY interaction.started_at, interaction.id
			LIMIT $3
			FOR UPDATE OF interaction
		`, cursorStarted, cursorID, batch)
		if err != nil {
			_ = tx.Rollback(ctx)
			return result, fmt.Errorf("read scorecard backfill: %w", err)
		}
		interactions := []Interaction{}
		for rows.Next() {
			interaction, err := scanInteraction(rows)
			if err != nil {
				rows.Close()
				_ = tx.Rollback(ctx)
				return result, fmt.Errorf("scan scorecard backfill: %w", err)
			}
			interactions = append(interactions, interaction)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			_ = tx.Rollback(ctx)
			return result, fmt.Errorf("read scorecard backfill: %w", err)
		}
		for _, interaction := range interactions {
			recorded, err := recordScorecard(ctx, tx, interaction, m.now())
			if err != nil {
				_ = tx.Rollback(ctx)
				return result, err
			}
			result.Answers += recorded
		}
		if err := tx.Commit(ctx); err != nil {
			return result, fmt.Errorf("commit scorecard backfill: %w", err)
		}
		result.Calls += len(interactions)
		if len(interactions) < batch {
			return result, nil
		}
		last := interactions[len(interactions)-1]
		cursorStarted, cursorID = last.StartedAt, last.ID
	}
}
