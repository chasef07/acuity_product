package interaction

import "slices"

var ScorecardQuestions = withCodeChecks([]ScorecardQuestion{
	{
		Key: "knowledge_available", Code: "K2", Label: "Office knowledge had the answer", Source: "judge", Failure: true,
		Prompt:   "Did the office knowledge contain the answer to each office question?",
		Question: "Did `office_knowledge` contain the information needed to answer the caller's office questions? An office question asks for a fact about this practice that is the same for every caller: hours, locations and directions, phone and fax numbers, providers, services, self-pay fees and prices, and office policies or procedures such as records requests, prescriptions, forms, and glasses or contact lens pickup. These are not office questions: insurance acceptance, coverage, plan referral rules, or costs under a plan; appointment availability or requests to book, change, or cancel; the caller's own appointments, orders, records, prescriptions, bills, or results; medical advice; requests for staff or questions about the assistant; sales or vendor calls; and anything about other businesses. Compare the caller's office questions with that office knowledge, not the agent's answer. Answer FALSE if the office knowledge did not supply, or only said it did not supply, the information an office question needed. Answer TRUE if no office question was asked or office information could not be loaded; a load failure does not prove the knowledge was missing.",
		Yes:      "The office knowledge supplied the information needed to answer the caller's office questions, no office question was asked, or office information could not be loaded.", No: "The office knowledge did not supply the information needed to answer a caller's office question.",
	},
	{
		Key: "office_question_answered", Code: "A3", Label: "Office question answered correctly", Source: "judge", Failure: true,
		Prompt:   "Was each office question answered correctly from the office knowledge?",
		Question: "Did the agent correctly answer every office question using `office_knowledge`? Office questions include hours, locations, providers, services, fees, office policies, and records or prescription procedures. Answer FALSE for an unanswered office question, an invented or incorrect answer, or an answer that goes beyond the office knowledge. A later correction does not support an earlier unsupported answer. Questions about patient-specific records, insurance acceptance, appointment availability, or medical advice do not count. Answer TRUE if no office question was asked.",
		Yes:      "Every office question was correctly answered from the office knowledge, or no office question was asked.", No: "An office question went unanswered or received an incorrect or unsupported answer.",
	},
	{
		Key: "appointment_datetime_correct", Code: "A2", Label: "Appointment matched the caller", Source: "judge", Failure: true,
		Prompt:   "Did each confirmed appointment action match the caller's final agreed date and time?",
		Question: "For every confirmed booking, rescheduling, or cancellation action, did the tool result match the caller's final intended appointment date and time? Use the final agreed date/time, including explicitly accepted alternatives, in the office timezone, reading relative dates such as tomorrow from `call_started`. For rescheduling check both the original appointment and the new date/time; for cancellation check the targeted appointment. Compare actual tool results in `transcript`, not the assistant's claim. Grade confirmed actions only; a failed or missing result does not establish a wrong date/time. For a partial reschedule, grade the confirmed new booking even if the old cancellation is unconfirmed.",
		Yes:      "Every confirmed appointment action's tool result matches the caller's intended date and time.", No: "A confirmed action targets or produces a different appointment or date/time than the caller finally agreed to.",
	},
	{
		Key: "call_ended_correctly", Code: "E1", Label: "Call ended correctly", Source: "judge", Failure: true,
		Prompt:   "Did the agent end the call only after the caller was finished?",
		Question: "Judge only the end_call that ended the call; one whose result says blocked did not end it, and a caller disconnect is not an agent hangup. At that moment the caller was finished if they had said goodbye or thanks, said they were done (such as being on their way), declined further help, or made no request after the agent asked once whether they needed anything else. Caller words transcribed after that end_call do not count if the caller had already finished.",
		Yes:      "The caller was finished with nothing left unhandled, or no end_call ended the call.", No: "Right before the end_call, the caller was mid-sentence, asked a new question, gave new information, requested staff, or had an unhandled request, or the agent had just asked a question without waiting for the answer.",
	},
	{
		Key: "transfer_policy_followed", Code: "T1", Label: "Transfer policy followed", Source: "judge", Failure: true,
		Prompt:   "Did the agent transfer staff requests when its transfer policy requires?",
		Question: "For each caller request to speak with staff, did the agent transfer at the point `transfer_policy` requires: immediately when it requires that, otherwise after the one attempt to help, then with no re-pitch, repeated question, deflection, or intake while the caller still wanted staff? Grade when and whether transfer_call was called, not how the attempt was worded. Human pickup is not required. Naming staff in passing, asking whether staff got a message, or speech-recognition noise is not a request. Eye emergencies are graded separately. An earlier message or callback that went unanswered is an approach that already failed, so the transfer is immediate. When the need is unknown, a general offer to help is the attempt. A question alone, a generic menu for a known need, or claiming it cannot do supported work is not an attempt. Help before the staff request counts only if the caller answered that offer by asking for staff. Clinical, urgent, or medication needs may be transferred without offering a staff task. If the caller accepts (says yes, answers, or lets the agent proceed) and does not ask again, the request is settled. If they drop the request or hang up first, grade only what came before.",
		Yes:      "Every staff request was transferred at the required point, settled by accepted help, or dropped by the caller, or no staff request occurred.", No: "While the caller still wanted staff, the agent transferred without the required offer, pitched or asked more than once, deflected, asked intake questions, delayed a required immediate transfer, or never called transfer_call; or it transferred instead of giving safe help the caller accepted.",
	},
	{
		Key: "emergency_policy_followed", Code: "M1", Label: "Emergency policy followed", Source: "judge", Failure: true,
		Prompt:   "Did the agent immediately transfer an eye emergency listed in its transfer policy?",
		Question: "When the caller described an eye emergency listed in `transfer_policy`, did the agent immediately call transfer_call without first collecting identity or doing routine intake? Use the caller's reported symptoms and that policy; do not require a diagnosis or invent additional emergency triggers. An announcement or staff-task draft does not replace the transfer tool call. Grade the attempt, not whether a human answered. Answer TRUE if no policy-defined eye emergency was described.",
		Yes:      "Every policy-defined eye emergency prompted an immediate transfer_call before identity or routine intake, or no eye emergency was described.", No: "The caller described a policy-defined eye emergency and the agent omitted or delayed the required transfer_call for identity or routine intake.",
	},
})

var v8ScorecardQuestions = withCodeChecks([]ScorecardQuestion{
	{
		Key: "knowledge_available", Code: "K2", Label: "Office knowledge had the answer", Source: "judge", Failure: true,
		Prompt:   "Did returned office knowledge contain the answer?",
		Question: "Did returned office knowledge contain the information needed to answer each office question? A no_results response, missing information, or irrelevant result means no. Failed or absent searches are unknown, not proof of a knowledge gap.",
		Yes:      "Returned office knowledge contained the needed answer.", No: "Returned office knowledge was empty, irrelevant, or missing needed information.",
	},
	{
		Key: "office_question_answered", Code: "A3", Label: "Office question answered correctly", Source: "judge", Failure: true,
		Prompt:   "Was each office question answered correctly from earlier office knowledge?",
		Question: "Did the agent accurately answer every office question from relevant knowledge returned before the answer? Unanswered questions, invented or unsupported claims, and incorrect answers mean no. A later correction does not erase an earlier unsupported answer. Questions about personal appointments, coverage, or medical advice are excluded. Answer yes if no office question was asked.",
		Yes:      "Every office question was answered correctly from earlier knowledge, or none was asked.", No: "An office question went unanswered or received an incorrect or unsupported answer.",
	},
	{
		Key: "appointment_datetime_correct", Code: "A2", Label: "Appointment matched the caller", Source: "judge", Failure: true,
		Prompt:   "Did the appointment action match the caller's final agreed appointment and date/time?",
		Question: "Compare actual booking, rescheduling, or cancellation results with the caller's final agreed target and date/time, including accepted alternatives, in the office timezone. For rescheduling check confirmed original and new appointment actions; a partial reschedule still grades the confirmed new booking. For cancellation check the targeted appointment. Only confirmed action results establish the appointment. No attempted appointment action is not applicable; an attempted action without confirmed success is unknown.",
		Yes:      "Every appointment action result matched the final agreed target and date/time.", No: "A confirmed appointment action targeted or produced the wrong appointment/date/time.",
	},
	{
		Key: "call_ended_correctly", Code: "E1", Label: "Call ended correctly", Source: "judge", Failure: true,
		Prompt:   "Did the agent end the call only after the caller was finished?",
		Question: "Did end_call occur only after the caller finished and no requested action remained unhandled, or after the permitted no-response procedure? Ending while the caller was speaking, giving information, asking a question, requesting staff, or awaiting an action means no. Answer yes if no end_call occurred; a caller disconnect is not an agent hangup.",
		Yes:      "The caller was finished with no unhandled request, the no-response procedure was followed, or no end_call occurred.", No: "The agent ended while the caller was active or a requested action remained unhandled.",
	},
	{
		Key: "transfer_policy_followed", Code: "T1", Label: "Transfer policy followed", Source: "judge", Failure: true,
		Prompt:   "Did the agent follow the staff-transfer policy?",
		Question: "A callback for a named staff member requires immediate transfer_call. For an ordinary staff request, offer one specific way to help unless that attempt already failed. If the caller declines or insists on staff, call transfer_call without more intake or pitches. Explaining the reason for calling is not accepting help. Skipping required help, transferring instead of accepted help, and missing or delaying a required transfer mean no. Judge the tool call, not whether a person answered. Answer yes if no staff request occurred.",
		Yes:      "The agent followed the applicable immediate-transfer or one-help-attempt policy, or no staff request occurred.", No: "The agent skipped required help, transferred instead of accepted help, or missed/delayed a required transfer.",
	},
	{
		Key: "emergency_policy_followed", Code: "M1", Label: "Emergency policy followed", Source: "judge", Failure: true,
		Prompt:   "Did the agent immediately transfer an eye emergency covered by policy?",
		Question: "When the caller described an eye emergency covered by the active office policy, did the agent immediately call transfer_call before identity checks or routine intake? Do not require a human to answer. Answer yes if no covered eye emergency was described.",
		Yes:      "A covered eye emergency triggered immediate transfer_call before routine intake, or no covered emergency was described.", No: "A covered eye emergency did not trigger immediate transfer_call or transfer was delayed for identity or routine intake.",
	},
})

var v9ScorecardQuestions = func() []ScorecardQuestion {
	questions := slices.Clone(ScorecardQuestions)
	for i := range questions {
		if questions[i].Key == "knowledge_available" {
			questions[i].Question = "Did `office_knowledge` contain the information needed to answer the caller's office questions? Compare the caller's questions with that office knowledge, not the agent's answer. Answer FALSE if the office knowledge did not supply, or only said it did not supply, the information a caller's office question needed. Answer TRUE if no office question was asked or office information could not be loaded; a load failure does not prove the knowledge was missing."
		}
	}
	return questions
}()

func withCodeChecks(questions []ScorecardQuestion) []ScorecardQuestion {
	for _, question := range legacyScorecardQuestions {
		if question.Source == "code" {
			questions = append(questions, question)
		}
	}
	return questions
}

func ScorecardQuestionsForVersion(version string) []ScorecardQuestion {
	switch version {
	case "", ScorecardJudgeVersion:
		return ScorecardQuestions
	case v9JudgeVersion:
		return v9ScorecardQuestions
	case v8JudgeVersion:
		return v8ScorecardQuestions
	case legacyJudgeVersion:
		return legacyScorecardQuestions
	case previousJudgeVersion:
		questions := []ScorecardQuestion{}
		for _, question := range previousJuryQuestions {
			label := question.failureLabel
			if label == "" {
				label = question.key
			}
			questions = append(questions, ScorecardQuestion{Key: question.key, Label: label, Prompt: label, Source: "judge", Failure: question.failureLabel != ""})
		}
		for _, question := range ScorecardQuestions {
			if question.Source == "code" {
				questions = append(questions, question)
			}
		}
		return questions
	default:
		return nil
	}
}
