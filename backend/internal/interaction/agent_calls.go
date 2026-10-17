package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

type AgentCall struct {
	Intent             *string             `json:"intent,omitempty"`
	Outcome            *string             `json:"outcome,omitempty"`
	LabelingStatus     *string             `json:"labelingStatus,omitempty"`
	ID                 string              `json:"id"`
	Phone              string              `json:"phone"`
	StartedAt          time.Time           `json:"startedAt"`
	DurationSeconds    *int                `json:"durationSeconds,omitempty"`
	AppointmentActions []AppointmentAction `json:"appointmentActions"`
	Transferred        bool                `json:"transferred"`
	IssueFlagged       bool                `json:"issueFlagged"`
	KnowledgeGaps      []string            `json:"knowledgeGaps"`
}
type AgentCallIssueReason string

const (
	AgentCallIssueWrongAppointmentType AgentCallIssueReason = "WRONG_APPOINTMENT_TYPE"
	AgentCallIssueInsurance            AgentCallIssueReason = "INSURANCE_ISSUE"
	AgentCallIssueOther                AgentCallIssueReason = "OTHER"
)

type AgentCallIssue struct {
	Reason    AgentCallIssueReason `json:"reason"`
	Note      string               `json:"note,omitempty"`
	CreatedAt time.Time            `json:"createdAt"`
}
type AgentCallMessage struct {
	Speaker    string    `json:"speaker"`
	Text       string    `json:"text"`
	OccurredAt time.Time `json:"occurredAt"`
}
type AgentCallDetail struct {
	Call         AgentCall          `json:"call"`
	LocationName string             `json:"locationName"`
	Messages     []AgentCallMessage `json:"messages"`
	Issue        *AgentCallIssue    `json:"issue,omitempty"`
	Scorecard    []AgentCallVerdict `json:"scorecard"`
	Sentiment    *float64           `json:"sentiment,omitempty"`
	CheckResult  string             `json:"checkResult"`
}

type AgentCallVerdict struct {
	Question string `json:"question"`
	Code     string `json:"code"`
	Prompt   string `json:"prompt"`
	Answer   string `json:"answer"`
}
type AgentCallsPage struct {
	Calls      []AgentCall `json:"calls"`
	NextCursor string      `json:"nextCursor"`
}
type QueryAgentCallsCommand struct {
	QueryAnalyticsCommand
	Phone              string
	FlaggedOnly        bool
	NeedsKnowledgeOnly bool
}
type agentCallsCursor struct {
	analyticsCursor
	Phone              string `json:"phone"`
	FlaggedOnly        bool   `json:"flaggedOnly"`
	NeedsKnowledgeOnly bool   `json:"needsKnowledgeOnly"`
}

func (m *Module) QueryAgentCalls(ctx context.Context, command QueryAgentCallsCommand) (AgentCallsPage, error) {
	normalizeAnalyticsCommand(&command.QueryAnalyticsCommand)
	duration, validRange := analyticsRangeDuration(command.Range)
	if !validRange || command.Limit < 1 || command.Limit > 100 || len(command.Phone) > 32 || !validCallLabelFilters(command.QueryAnalyticsCommand) {
		return AgentCallsPage{}, ErrInvalidInput
	}
	phone := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, command.Phone)
	if strings.TrimSpace(command.Phone) != "" && phone == "" {
		return AgentCallsPage{}, ErrInvalidInput
	}
	through := m.now().UTC().Truncate(time.Microsecond)
	var startedAt any
	var id any
	if command.Cursor != "" {
		var cursor agentCallsCursor
		if !decodeCursor(command.QueryAnalyticsCommand, &cursor, &cursor.analyticsCursor, through) || cursor.Phone != phone || cursor.FlaggedOnly != command.FlaggedOnly || cursor.NeedsKnowledgeOnly != command.NeedsKnowledgeOnly {
			return AgentCallsPage{}, ErrInvalidInput
		}
		through, startedAt, id = cursor.Through, cursor.StartedAt, cursor.ID
	}
	tx, locations, err := m.beginAnalyticsScope(ctx, command.Identity, command.PracticeID, command.LocationID, audienceStaff)
	if err != nil {
		return AgentCallsPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
  SELECT i.id::text, i.phone, i.started_at, i.ended_at, i.status,
   i.appointment_outcome, i.booking_result, i.cancellation_result,
   i.closeout_payload -> 'domainOutcomes', issue.interaction_id IS NOT NULL,
   classification.intent, classification.outcome, classification.status, gaps.detail
  FROM ai_interactions i
  LEFT JOIN ai_interaction_issues issue ON issue.interaction_id = i.id
  LEFT JOIN ai_interaction_classifications classification ON classification.interaction_id = i.id
  LEFT JOIN ai_interaction_scorecard_answers gaps ON gaps.interaction_id = i.id AND gaps.question = 'knowledge_answered'
  WHERE i.practice_id = $1 AND i.location_id = ANY($2::uuid[])
   AND i.started_at >= $3 AND i.started_at <= $4
   AND ($5::timestamptz IS NULL OR (i.started_at, i.id) < ($5, $6::uuid))
   AND ($7 = '' OR strpos(i.phone, $7) > 0)
   AND (NOT $8 OR issue.interaction_id IS NOT NULL)
   AND (NOT $9 OR EXISTS (
    SELECT 1 FROM ai_interaction_scorecard_answers judged
    WHERE judged.interaction_id = i.id AND NOT judged.answer AND (
     (judged.scorecard_version = 'typesafe-scorecard-v8' AND judged.question = 'knowledge_available') OR
     (judged.scorecard_version <> 'typesafe-scorecard-v8' AND judged.question = 'office_question_answered'))))
   AND ($11 = '' OR classification.intent = $11)
   AND ($12 = '' OR classification.outcome = $12)
  ORDER BY i.started_at DESC, i.id DESC LIMIT $10
 `, command.PracticeID, locations, through.Add(-duration), through, startedAt, id, phone, command.FlaggedOnly, command.NeedsKnowledgeOnly, command.Limit+1, command.Intent, command.Outcome)
	if err != nil {
		return AgentCallsPage{}, err
	}
	page := AgentCallsPage{Calls: []AgentCall{}}
	for rows.Next() {
		var stored Interaction
		var receipts json.RawMessage
		var flagged bool
		var gaps json.RawMessage
		var labels callLabelProjection
		if err := rows.Scan(&stored.ID, &stored.Phone, &stored.StartedAt, &stored.EndedAt, &stored.Status, &stored.AppointmentOutcome, &stored.BookingResult, &stored.CancellationResult, &receipts, &flagged, &labels.Intent, &labels.Outcome, &labels.LabelingStatus, &gaps); err != nil {
			rows.Close()
			return AgentCallsPage{}, err
		}
		call := agentCall(stored, receipts, flagged, gaps)
		call.Intent, call.Outcome, call.LabelingStatus = labels.Intent, labels.Outcome, labels.LabelingStatus
		page.Calls = append(page.Calls, call)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return AgentCallsPage{}, err
	}
	if len(page.Calls) > command.Limit {
		page.Calls = page.Calls[:command.Limit]
		last := page.Calls[len(page.Calls)-1]
		page.NextCursor, err = encodeCursor(agentCallsCursor{analyticsCursor: newAnalyticsCursor(command.QueryAnalyticsCommand, last.StartedAt, last.ID, through), Phone: phone, FlaggedOnly: command.FlaggedOnly, NeedsKnowledgeOnly: command.NeedsKnowledgeOnly})
		if err != nil {
			return AgentCallsPage{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentCallsPage{}, err
	}
	return page, nil
}

func agentCall(stored Interaction, receipts json.RawMessage, flagged bool, knowledgeGaps json.RawMessage) AgentCall {
	actions := []AppointmentAction{}
	add := func(action AppointmentAction) {
		if !slices.Contains(actions, action) {
			actions = append(actions, action)
		}
	}
	var outcomes []map[string]any
	_ = json.Unmarshal(receipts, &outcomes)
	for _, outcome := range outcomes {
		evidence := recordValue(outcome["evidence"])
		if len(evidence) == 0 || boolValue(evidence["replayed"]) {
			continue
		}
		if stringValue(outcome["status"]) == "partial" && appointmentDomainOutcome(stringValue(outcome["outcome"])) {
			if stringValue(recordValue(evidence["bookingResult"])["status"]) == "booked" {
				add(AppointmentBooked)
			}
			if stringValue(recordValue(evidence["cancellationResult"])["status"]) == "cancelled" {
				add(AppointmentCancelled)
			}
			continue
		}
		if stringValue(outcome["status"]) != "success" {
			continue
		}
		switch stringValue(outcome["outcome"]) {
		case "booked":
			add(AppointmentBooked)
		case "rescheduled":
			add(AppointmentRescheduled)
		case "cancelled":
			add(AppointmentCancelled)
		}
	}
	if len(receipts) == 0 {
		switch stored.AppointmentOutcome {
		case OutcomeBooking:
			add(AppointmentBooked)
		case OutcomeCancellation:
			add(AppointmentCancelled)
		case OutcomeReschedule:
			add(AppointmentRescheduled)
		case OutcomePartial:
			if resultStatus(stored.BookingResult) == "booked" {
				add(AppointmentBooked)
			}
			if resultStatus(stored.CancellationResult) == "cancelled" {
				add(AppointmentCancelled)
			}
		}
	}
	call := AgentCall{ID: stored.ID, Phone: stored.Phone, StartedAt: stored.StartedAt, AppointmentActions: actions, Transferred: stored.Status == CallEscalated, IssueFlagged: flagged, KnowledgeGaps: knowledgeGapQueries(knowledgeGaps)}
	if stored.EndedAt != nil {
		seconds := max(0, int(stored.EndedAt.Sub(stored.StartedAt).Seconds()))
		call.DurationSeconds = &seconds
	}
	return call
}

func (m *Module) ReadAgentCall(ctx context.Context, identity access.Identity, id string) (AgentCallDetail, error) {
	tx, _, err := m.beginInteractionAccess(ctx, identity, id, false, audienceStaff)
	if err != nil {
		return AgentCallDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stored, err := readInteraction(ctx, tx, id)
	if err != nil {
		return AgentCallDetail{}, err
	}
	var issue AgentCallIssue
	err = tx.QueryRow(ctx, `SELECT reason, `+issueNoteWithoutPreviousReleaseReasonCopy+`, created_at FROM ai_interaction_issues issue WHERE interaction_id = $1`, id).Scan(&issue.Reason, &issue.Note, &issue.CreatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AgentCallDetail{}, err
	}
	detail := AgentCallDetail{LocationName: stored.LocationName, Messages: []AgentCallMessage{}}
	if err == nil {
		detail.Issue = &issue
	}
	var gaps json.RawMessage
	err = tx.QueryRow(ctx, `SELECT detail FROM ai_interaction_scorecard_answers WHERE interaction_id = $1 AND question = 'knowledge_answered'`, id).Scan(&gaps)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AgentCallDetail{}, err
	}
	var closeout struct {
		DomainOutcomes json.RawMessage `json:"domainOutcomes"`
		Evaluation     json.RawMessage `json:"evaluation"`
	}
	_ = json.Unmarshal(stored.CloseoutPayload, &closeout)
	detail.Call = agentCall(stored, closeout.DomainOutcomes, detail.Issue != nil, gaps)
	labels, err := readCallLabelProjection(ctx, tx, id)
	if err != nil {
		return AgentCallDetail{}, err
	}
	detail.Call.Intent, detail.Call.Outcome, detail.Call.LabelingStatus = labels.Intent, labels.Outcome, labels.LabelingStatus
	detail.Scorecard = agentCallScorecard(closeout.Evaluation)
	detail.CheckResult = agentCallCheckResult(closeout.Evaluation)
	detail.Sentiment = jurySentiment(closeout.Evaluation)
	timeline, _ := normalizeTimeline(stored.Transcript, nil, stored.StartedAt)
	for _, item := range timeline {
		speaker := ""
		switch item.Kind {
		case TimelineCallerMessage:
			speaker = "Caller"
		case TimelineAgentMessage:
			speaker = "Agent"
		}
		if speaker != "" && strings.TrimSpace(item.Text) != "" {
			detail.Messages = append(detail.Messages, AgentCallMessage{Speaker: speaker, Text: item.Text, OccurredAt: item.OccurredAt})
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentCallDetail{}, err
	}
	return detail, nil
}

func knowledgeGapQueries(detail json.RawMessage) []string {
	var recorded struct {
		Queries []string `json:"queries"`
	}
	_ = json.Unmarshal(detail, &recorded)
	queries := []string{}
	for _, query := range recorded.Queries {
		if query = strings.TrimSpace(query); query != "" {
			queries = append(queries, query)
		}
	}
	return queries
}

func agentCallCheckResult(evaluation json.RawMessage) string {
	jury, valid := readJury(evaluation)
	if !valid {
		return "unknown"
	}
	questions := juryQuestions(jury.Version)
	answers := map[string]bool{}
	for _, answer := range juryAnswers(jury, questions) {
		answers[answer.Question] = answer.Answer
	}
	complete, passed := jury.Status == "complete", false
	for _, question := range questions {
		if answer, found := answers[question.key]; found {
			if question.failureLabel != "" {
				if !answer {
					return "fail"
				}
				passed = true
			}
			continue
		}
		var result struct {
			Status string `json:"status"`
		}
		_, failed := jury.Errors[question.key]
		if failed || json.Unmarshal(jury.Results[question.key], &result) != nil || result.Status != "not_applicable" {
			complete = false
		}
	}
	if complete && passed {
		return "pass"
	}
	return "unknown"
}

func agentCallScorecard(evaluation json.RawMessage) []AgentCallVerdict {
	verdicts := map[string]string{}
	jury, valid := readJury(evaluation)
	if !valid {
		return []AgentCallVerdict{}
	}
	for _, answer := range juryAnswers(jury, juryQuestions(jury.Version)) {
		verdicts[answer.Question] = "no"
		if answer.Answer {
			verdicts[answer.Question] = "yes"
		}
	}
	scorecard := []AgentCallVerdict{}
	for _, question := range ScorecardQuestionsForVersion(jury.Version) {
		if verdict, found := verdicts[question.Key]; found {
			scorecard = append(scorecard, AgentCallVerdict{Question: question.Key, Code: question.Code, Prompt: question.Prompt, Answer: verdict})
		}
	}
	return scorecard
}

const issueNoteWithoutPreviousReleaseReasonCopy = `COALESCE(NULLIF(issue.note, issue.reason), '')`

func (m *Module) FlagAgentCallIssue(ctx context.Context, identity access.Identity, id string, reason AgentCallIssueReason, note string) (AgentCallIssue, error) {
	switch reason {
	case AgentCallIssueWrongAppointmentType, AgentCallIssueInsurance, AgentCallIssueOther:
	default:
		return AgentCallIssue{}, ErrInvalidInput
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 2000 {
		return AgentCallIssue{}, ErrInvalidInput
	}
	tx, authorization, err := m.beginInteractionAccess(ctx, identity, id, true, audienceStaff)
	if err != nil {
		return AgentCallIssue{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `INSERT INTO ai_interaction_issues (interaction_id, reported_by, reason, note) VALUES ($1, $2, $3, NULLIF($4, '')) ON CONFLICT (interaction_id) DO NOTHING`, id, identity.Subject, reason, note)
	if err != nil {
		return AgentCallIssue{}, fmt.Errorf("record AI call issue: %w", err)
	}
	if result.RowsAffected() > 0 {
		if err := m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{Action: "ai_interaction.issue_reported", ResourceType: "ai_interaction", ResourceID: id, ResourceVersion: 1, OccurredAt: m.now()}); err != nil {
			return AgentCallIssue{}, err
		}
	}
	var issue AgentCallIssue
	var reporter string
	if err := tx.QueryRow(ctx, `SELECT reason, `+issueNoteWithoutPreviousReleaseReasonCopy+`, created_at, reported_by FROM ai_interaction_issues issue WHERE interaction_id = $1`, id).Scan(&issue.Reason, &issue.Note, &issue.CreatedAt, &reporter); err != nil {
		return AgentCallIssue{}, fmt.Errorf("read AI call issue: %w", err)
	}
	if issue.Reason != reason || reporter != identity.Subject {
		return AgentCallIssue{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentCallIssue{}, err
	}
	return issue, nil
}
