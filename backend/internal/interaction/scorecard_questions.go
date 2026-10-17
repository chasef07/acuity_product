package interaction

var ScorecardQuestions = func() []ScorecardQuestion {
	questions := []ScorecardQuestion{
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
	}
	for _, question := range legacyScorecardQuestions {
		if question.Source == "code" {
			questions = append(questions, question)
		}
	}
	return questions
}()

func ScorecardQuestionsForVersion(version string) []ScorecardQuestion {
	switch version {
	case "", ScorecardJudgeVersion:
		return ScorecardQuestions
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
