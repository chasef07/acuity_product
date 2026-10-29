package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	ScorecardJudgeVersion = "typesafe-scorecard-v9"
	v8JudgeVersion        = "typesafe-scorecard-v8"
	legacyJudgeVersion    = "typesafe-scorecard-v7"
	previousJudgeVersion  = "typesafe-scorecard-v6"
	ScorecardCodeVersion  = "code-checks-v2"
	scorecardNoAtOrBelow  = 0.40
)

var juryScorecardVersions = []string{ScorecardJudgeVersion, v8JudgeVersion, legacyJudgeVersion, previousJudgeVersion}

type ScorecardQuestion struct {
	Key      string `json:"key"`
	Code     string `json:"code"`
	Label    string `json:"label"`
	Prompt   string `json:"prompt"`
	Source   string `json:"source"`
	Question string `json:"question"`
	Yes      string `json:"yes"`
	No       string `json:"no"`
	Failure  bool   `json:"failure"`
}

var legacyScorecardQuestions = []ScorecardQuestion{
	{
		Key: "new_booking_requested", Code: "B1", Label: "New booking requested", Prompt: "Did the caller ask to book a new appointment?", Source: "judge",
		Question: "Did the caller ask to book a NEW appointment, for themselves or someone else (for example a child)? Rescheduling, moving, or cancelling an existing appointment does not count. Asking about an existing appointment, insurance, prescriptions, glasses, records, or billing does not count by itself.",
		Yes:      "The caller asked to book a new appointment.",
		No:       "The caller did not ask to book a new appointment (they may have asked to cancel or reschedule, or something else).",
	},
	{
		Key: "scheduling_tool_called", Code: "B2", Label: "Scheduling tool called", Prompt: "Did the agent try to book, reschedule, or cancel?", Source: "code",
		Question: "Did the agent call the matching scheduling tool?",
		Yes:      "The agent called book_appointment, reschedule_appointment, or cancel_appointment.",
		No:       "The agent never attempted a booking, reschedule, or cancellation.",
	},
	{
		Key: "scheduling_succeeded", Code: "B3", Label: "Scheduling succeeded", Prompt: "Did the booking, reschedule, or cancel go through?", Source: "code",
		Question: "Did the scheduling tool return a success?",
		Yes:      "A booking, reschedule, or cancellation receipt came back successful. The call is converted.",
		No:       "No scheduling receipt came back successful.",
	},
	{
		Key: "booking_blocked", Code: "B4", Label: "Blocked by a tool result", Prompt: "Did a tool result block the booking?", Source: "code",
		Question: "Did a tool result block it (no slots, insurance not accepted, policy)?",
		Yes:      "Availability returned no openings or no eligible providers, or the insurance check said the plan is not accepted or needs a referral or prior authorization. Blocked calls are left out of conversion.",
		No:       "No tool result blocked the booking. A system failure, such as availability that could not be verified, is not a block.",
	},
	{
		Key: "availability_searched", Code: "B0", Label: "Availability searched", Prompt: "Did the agent search for open times?", Source: "code",
		Question: "Did a list_available_appointments call return a result?",
		Yes:      "At least one availability search returned a result, even if it found no openings.",
		No:       "The agent never searched availability. A new-booking caller without a search is a not-searched booking.",
	},
	{
		Key: "time_offered", Code: "B5", Label: "Specific time offered", Prompt: "Did the agent offer a specific open time?", Source: "judge",
		Question: "Did the agent offer the caller at least one specific appointment date and time taken from an availability tool result? A vague promise, a staff request to find a time, or a time the caller proposed that the agent did not confirm as available does not count.",
		Yes:      "The agent offered at least one specific date and time from returned availability.",
		No:       "The agent never offered a specific available date and time.",
	},
	{
		Key: "requested_time_honored", Failure: true, Code: "B6", Label: "Requested time honored", Prompt: "Did the agent offer the day or time the caller asked for, or say none matched?", Source: "judge",
		Question: "When the caller asked for a specific day, date, time of day (morning, afternoon), or the earliest opening, did the agent either offer a matching time from the availability results, or plainly say no matching time was available before suggesting other times? If the caller never asked for a specific day or time, answer true.",
		Yes:      "Every specific day or time the caller asked for was offered from the results, or the agent plainly said none matched.",
		No:       "The caller asked for a day or time and the agent offered other times without offering a matching one or saying none matched.",
	},
	{
		Key: "office_rules_grounded", Failure: true, Code: "A1", Label: "Office facts backed", Prompt: "Were all office and record facts backed by an earlier lookup?", Source: "judge",
		Question: "Compare every fact the agent stated about the office or a patient record against the text of tool results that came BEFORE it: hours or whether the office is open, which locations offer which services, where a provider practices, a staff member's title or credential (optician is not optometrist), floor, suite, address, phone, fees, policies, or whether a patient has an appointment. Answer FALSE if any stated fact is not in an earlier tool result, goes beyond what the result says, changes a word in a way that changes the meaning, or confirms a caller's guess that no result confirmed - even if a later lookup corrects it. Saying the information could not be confirmed is fine. Answer true if no such facts were stated or every one matches an earlier result.",
		Yes:      "Every office or record fact the agent stated matches an earlier tool result.",
		No:       "At least one office or record fact was not in, went beyond, or changed an earlier tool result.",
	},
	{
		Key: "appointment_datetime_correct", Failure: true, Code: "A2", Label: "Appointment matched the caller", Prompt: "Did the appointment match what the caller agreed to?", Source: "judge",
		Question: "For every booking, rescheduling, or cancellation action, did the tool result match the caller's final intended appointment date and time? Use the final agreed date/time, including explicitly accepted alternatives, in the office timezone. For rescheduling check both the original appointment and the new date/time; for cancellation check the targeted appointment. Compare actual tool results, not the assistant's claim. Missing results cannot establish a match.",
		Yes:      "Every appointment action's tool result confirms the caller's intended date and time.",
		No:       "Any action targets or produces the wrong date/time, or there is insufficient evidence of a matching appointment action.",
	},
	{
		Key: "office_question_answered", Failure: true, Code: "A3", Label: "Office question answered", Prompt: "Did office knowledge have the answer to every office question the caller asked?", Source: "judge",
		Question: "Did the caller ask a question about the office - hours, locations, phone or fax numbers, providers, services, fees, policies, how to get records or prescriptions, referrals, parking, directions? If so, did the agent give a specific answer taken from an office knowledge or tool result? Answer FALSE if the caller asked an office question and the agent said it could not confirm or find the answer, the knowledge result said the information was not supplied, or the agent had to offer a staff message or transfer only because the answer was missing. Answer true if the caller asked no office questions or every office question was answered from a tool result. Questions about the caller's own appointments, insurance coverage, or medical advice do not count.",
		Yes:      "Every office question the caller asked was answered from office knowledge, or none was asked.",
		No:       "The caller asked an office question that office knowledge could not answer.",
	},
	{
		Key: "promises_backed", Failure: true, Code: "P1", Label: "Promises backed by a tool", Prompt: "Was every promise or \"done\" backed by a tool result?", Source: "judge",
		Question: "Check every statement the agent made about what has happened or will happen next. Did a tool result support each one? Answer FALSE if the agent said any of these without a tool result that says so: that staff WILL call the caller back, follow up, take care of it, email or send something, or contact them by a certain time (a saved staff task result only says a draft was saved for staff - it does not promise a callback); that it is transferring to a specific person or department (transfer_call has no destination); that it is checking the caller's record, appointment, or staff availability when no such lookup tool ran; that something is already done, booked, cancelled, noted, or sent ('listo', 'anotado', 'ya lo dejo') before the tool result for it; or that it will note or do something and then no tool ever did it. Saying staff will review the saved request, or that the request will be sent when the call ends, is supported.",
		Yes:      "Every statement about what happened or will happen next was supported by a tool result.",
		No:       "The agent promised a callback, follow-up, specific transfer destination, lookup, or completed action that no tool result supported.",
	},
	{
		Key: "ended_while_caller_active", Failure: true, Code: "E1", Label: "Ended when the caller was done", Prompt: "Did the agent end the call only after the caller was done?", Source: "judge",
		Question: "Look at the end of the call. Did the agent end the call (end_call) only after the caller said goodbye, said they needed nothing else, or stopped responding? Answer FALSE if, right before the agent ended the call, the caller was still talking, had just asked a question or raised a new concern that went unanswered, asked for a person, or was in the middle of giving information. If the agent did not end the call, answer true.",
		Yes:      "The agent ended the call only after the caller was done, or the agent did not end the call.",
		No:       "The agent ended the call while the caller was still talking, asking something, or giving information.",
	},
	{
		Key: "no_results_retried", Failure: true, Code: "F2", Label: "Retried after no match", Prompt: "After no patient match, did the agent retry before transferring?", Source: "code",
		Question: "After a patient lookup returned no_results, did the agent clarify and retry before transferring?",
		Yes:      "The agent ran the patient lookup again before transferring the call.",
		No:       "The agent transferred without retrying the patient lookup.",
	},
	{
		Key: "knowledge_answered", Code: "K1", Label: "Office knowledge had the answer", Prompt: "Did every office knowledge search return an answer?", Source: "code",
		Question: "Did every search_office_knowledge result answer the search?",
		Yes:      "Every office knowledge search returned an answer, or a knowledge outage stopped the search. An answer that also mentions something not supplied still counts as an answer.",
		No:       "At least one search found nothing (no_results) or returned only \"not supplied\" sentences. This is a fact, not an agent failure: A3 is the failure question. The search queries are recorded so staff can fill the gap.",
	},
	{
		Key: "transfer_not_refused", Failure: true, Code: "T2", Label: "Transfer not refused", Prompt: "Did the agent avoid saying it cannot transfer calls?", Source: "code",
		Question: "Did the agent avoid saying it does not or cannot transfer calls?",
		Yes:      "The agent never said it does not or cannot transfer calls.",
		No:       "The agent said it does not or cannot transfer calls, for example \"I don't transfer calls\".",
	},
	{
		Key: "insurance_resolved", Failure: true, Code: "I3", Label: "Insurance settled without repeated asks", Prompt: "Did the insurance check settle in fewer than 3 asks for plan details?", Source: "code",
		Question: "Did check_insurance ask for more plan information fewer than 3 times?",
		Yes:      "The insurance check asked for more plan information at most twice.",
		No:       "The insurance check asked what plan is on the card 3 or more times.",
	},
	{
		Key: "insurance_verified", Code: "I1", Label: "Insurance verified", Prompt: "Was the caller's insurance verified?", Source: "code",
		Question: "Was the caller's insurance verified?",
		Yes:      "The last insurance check accepted the plan.",
		No:       "The last insurance check ended blocked or still needed information. This is not an agent failure; a task or transfer is fine. The plan name is recorded so staff can update the insurance rules.",
	},
	{
		Key: "insurance_plan_as_stated", Failure: true, Code: "I2", Label: "Insurance checked as stated", Prompt: "Did every insurance check use the plan the caller described?", Source: "judge",
		Question: "Did every check_insurance call use the plan as the caller described it, including words like Medicaid, Medicare, HMO, PPO, or a network name, and was the plan checked again after the caller added to or corrected the plan? Normal spelling or capitalization changes are fine.",
		Yes:      "Every insurance check used the caller's complete plan description, including later additions or corrections.",
		No:       "An insurance check dropped or changed part of the plan the caller described, or was not repeated after the caller corrected it.",
	},
}

var scorecardQuestionByKey = func() map[string]ScorecardQuestion {
	result := map[string]ScorecardQuestion{}
	for _, question := range append(append([]ScorecardQuestion{}, legacyScorecardQuestions...), ScorecardQuestions...) {
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

const insuranceAskLimit = 3

var (
	sentenceEnd     = regexp.MustCompile(`[.!?](\s+|$)|\n`)
	transferRefusal = regexp.MustCompile(`(?i)\b(i|we)(\s+am|\s+are|'m|'re)?\s+(don't|do not|can't|cannot|can not|won't|will not|unable to|not able to)\s+transfer\s+(you|calls|the call|your call|this call)\b` +
		`|\bno\s+(puedo|podemos)\s+transferir(le|la|lo)?(\s+(la|su|esta)\s+llamada|\s+llamadas|\s*[.,!?]|\s*$)|\bno\s+(transfiero|transferimos)\s+llamadas\b`)
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

func knowledgeGap(output string) bool {
	if strings.HasPrefix(output, "no_results:") {
		return true
	}
	answer, found := strings.CutPrefix(output, "success:")
	if !found {
		return false
	}
	gap := false
	for _, sentence := range sentenceEnd.Split(answer, -1) {
		if sentence = strings.TrimSpace(sentence); sentence == "" {
			continue
		}
		if !strings.Contains(sentence, "not supplied") {
			return false
		}
		gap = true
	}
	return gap
}

func codeCheckAnswers(transcript json.RawMessage, closeout map[string]any) []scorecardAnswer {
	calls := scorecardToolCalls(transcript)
	answer := func(question string, value bool, detail map[string]any) scorecardAnswer {
		return scorecardAnswer{Question: question, Source: "code", Answer: value, Version: ScorecardCodeVersion, Detail: detail}
	}
	receipts := domainOutcomeReceiptsByCallID(closeout)
	writeCalled, writeSucceeded, searched := false, false, false
	lastOutput := map[string]string{}
	for _, call := range calls {
		lastOutput[call.Name] = call.Output
		searched = searched || (call.Name == "list_available_appointments" && call.Output != "")
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
	answers := []scorecardAnswer{answer("availability_searched", searched, nil), answer("scheduling_tool_called", writeCalled, nil)}
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

	knowledgeSearched, knowledgeGaps := false, []any{}
	for _, call := range calls {
		if call.Name != "search_office_knowledge" {
			continue
		}
		knowledgeSearched = true
		if knowledgeGap(call.Output) {
			knowledgeGaps = append(knowledgeGaps, strings.TrimSpace(anyString(call.Arguments["query"])))
		}
	}
	if knowledgeSearched {
		var detail map[string]any
		if len(knowledgeGaps) > 0 {
			detail = map[string]any{"queries": knowledgeGaps}
		}
		answers = append(answers, answer("knowledge_answered", len(knowledgeGaps) == 0, detail))
	}

	spoke, refused := false, false
	for _, value := range transcriptItems(decodeRecord(transcript)) {
		if record := recordValue(value); strings.EqualFold(firstRecordString(record, "role"), "assistant") {
			spoke = true
			refused = refused || transferRefusal.MatchString(strings.ReplaceAll(messageText(record), "’", "'"))
		}
	}
	if spoke {
		answers = append(answers, answer("transfer_not_refused", !refused, nil))
	}

	var lastInsurance *scorecardToolCall
	insuranceAsks := 0
	for index := range calls {
		if calls[index].Name == "check_insurance" {
			lastInsurance = &calls[index]
			if strings.Contains(calls[index].Output, "on the card") {
				insuranceAsks++
			}
		}
	}
	if lastInsurance != nil {
		var asks map[string]any
		if insuranceAsks >= insuranceAskLimit {
			asks = map[string]any{"asks": insuranceAsks}
		}
		answers = append(answers, answer("insurance_resolved", insuranceAsks < insuranceAskLimit, asks))
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

type juryEvaluation struct {
	Version string                     `json:"evaluatorVersion"`
	Jurors  []string                   `json:"jurors"`
	Status  string                     `json:"status"`
	Results map[string]json.RawMessage `json:"results"`
	Errors  map[string]json.RawMessage `json:"errors"`
}

type juryQuestion struct{ key, failureLabel string }

var previousJuryQuestions = []juryQuestion{
	{"booking_requested", ""}, {"time_offered", ""}, {"need_understood", "Understood every request"},
	{"right_help", "Right help or next step"}, {"clear_and_responsive", "Clear and responsive"},
	{"person_request_honored", "Person request honored"}, {"office_rules_grounded", "Office facts backed"},
	{"appointment_datetime_correct", "Appointment matched the caller"},
}

func juryQuestions(version string) []juryQuestion {
	if version == previousJudgeVersion {
		return previousJuryQuestions
	}
	questions := []juryQuestion{}
	for _, question := range ScorecardQuestionsForVersion(version) {
		if question.Source != "judge" {
			continue
		}
		label := ""
		if question.Failure {
			label = question.Label
		}
		questions = append(questions, juryQuestion{question.Key, label})
	}
	return questions
}

func readJury(evaluation json.RawMessage) (juryEvaluation, bool) {
	var jury juryEvaluation
	valid := json.Unmarshal(evaluation, &jury) == nil && slices.Contains(juryScorecardVersions, jury.Version) &&
		(jury.Status == "complete" || jury.Status == "incomplete") && len(jury.Jurors) > 0
	for _, juror := range jury.Jurors {
		valid = valid && strings.TrimSpace(juror) != ""
	}
	return jury, valid
}

func validProbability(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && *value >= 0 && *value <= 1
}

func judgeAnswers(evaluation json.RawMessage) []scorecardAnswer {
	jury, valid := readJury(evaluation)
	if !valid || jury.Version == previousJudgeVersion {
		return nil
	}
	return juryAnswers(jury, juryQuestions(jury.Version))
}

func juryAnswers(jury juryEvaluation, questions []juryQuestion) []scorecardAnswer {
	model := strings.Join(jury.Jurors, ",")
	answers := []scorecardAnswer{}
	for _, question := range questions {
		if _, failed := jury.Errors[question.key]; failed {
			continue
		}
		var result struct {
			Status      string             `json:"status"`
			Verdict     *bool              `json:"verdict"`
			Probability *float64           `json:"probability"`
			Votes       map[string]float64 `json:"votes"`
		}
		if json.Unmarshal(jury.Results[question.key], &result) != nil {
			continue
		}
		if result.Status == "unknown" || result.Status == "not_applicable" || result.Verdict == nil || !validProbability(result.Probability) {
			continue
		}
		probability := *result.Probability
		answers = append(answers, scorecardAnswer{
			Question:    question.key,
			Source:      "judge",
			Answer:      *result.Verdict,
			Probability: &probability,
			Version:     jury.Version,
			JudgeModel:  model,
			Detail:      map[string]any{"votes": result.Votes},
		})
	}
	return answers
}

func jurySentiment(evaluation json.RawMessage) *float64 {
	jury, valid := readJury(evaluation)
	if _, failed := jury.Errors["expressed_sentiment"]; !valid || failed {
		return nil
	}
	var sentiment struct {
		Score *float64 `json:"score"`
	}
	if json.Unmarshal(jury.Results["expressed_sentiment"], &sentiment) != nil || sentiment.Score == nil || *sentiment.Score < 0 || *sentiment.Score > 4 {
		return nil
	}
	return sentiment.Score
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
	evaluation, _ := json.Marshal(decodeRecord(interaction.CloseoutPayload)["evaluation"])
	jury, judged := readJury(evaluation)
	keepPreviousJudge := interaction.LifecycleStage == LifecycleClosed && judged && jury.Version == previousJudgeVersion
	if _, err := tx.Exec(ctx, `DELETE FROM ai_interaction_scorecard_answers WHERE interaction_id = $1 AND NOT ($2 AND source = 'judge')`, interaction.ID, keepPreviousJudge); err != nil {
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
