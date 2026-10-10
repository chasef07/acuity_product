package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

type AgentCall struct {
	ID                 string              `json:"id"`
	Phone              string              `json:"phone"`
	StartedAt          time.Time           `json:"startedAt"`
	DurationSeconds    *int                `json:"durationSeconds,omitempty"`
	AppointmentActions []AppointmentAction `json:"appointmentActions"`
	Transferred        bool                `json:"transferred"`
	IssueFlagged       bool                `json:"issueFlagged"`
}
type AgentCallIssueReason string

const (
	AgentCallIssueWrongAppointmentType AgentCallIssueReason = "WRONG_APPOINTMENT_TYPE"
	AgentCallIssueInsurance            AgentCallIssueReason = "INSURANCE_ISSUE"
	AgentCallIssueOther                AgentCallIssueReason = "OTHER"
)

type AgentCallIssue struct {
	Reason    AgentCallIssueReason `json:"reason"`
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
}

type AgentCallVerdict struct {
	Code   string `json:"code"`
	Prompt string `json:"prompt"`
	Answer string `json:"answer"`
}
type AgentCallsPage struct {
	Calls      []AgentCall `json:"calls"`
	NextCursor string      `json:"nextCursor"`
}
type QueryAgentCallsCommand struct {
	QueryAnalyticsCommand
	Phone       string
	FlaggedOnly bool
}
type agentCallsCursor struct {
	analyticsCursor
	Phone       string `json:"phone"`
	FlaggedOnly bool   `json:"flaggedOnly"`
}

func (m *Module) QueryAgentCalls(ctx context.Context, command QueryAgentCallsCommand) (AgentCallsPage, error) {
	normalizeAnalyticsCommand(&command.QueryAnalyticsCommand)
	duration, validRange := analyticsRangeDuration(command.Range)
	if !validRange || command.Limit < 1 || command.Limit > 100 || len(command.Phone) > 32 {
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
		if !decodeCursor(command.QueryAnalyticsCommand, &cursor, &cursor.analyticsCursor, through) || cursor.Phone != phone || cursor.FlaggedOnly != command.FlaggedOnly {
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
   i.closeout_payload -> 'domainOutcomes', issue.interaction_id IS NOT NULL
  FROM ai_interactions i
  LEFT JOIN ai_interaction_issues issue ON issue.interaction_id = i.id
  WHERE i.practice_id = $1 AND i.location_id = ANY($2::uuid[])
   AND i.started_at >= $3 AND i.started_at <= $4
   AND ($5::timestamptz IS NULL OR (i.started_at, i.id) < ($5, $6::uuid))
   AND ($7 = '' OR strpos(i.phone, $7) > 0)
   AND (NOT $8 OR issue.interaction_id IS NOT NULL)
  ORDER BY i.started_at DESC, i.id DESC LIMIT $9
 `, command.PracticeID, locations, through.Add(-duration), through, startedAt, id, phone, command.FlaggedOnly, command.Limit+1)
	if err != nil {
		return AgentCallsPage{}, err
	}
	page := AgentCallsPage{Calls: []AgentCall{}}
	for rows.Next() {
		var stored Interaction
		var receipts json.RawMessage
		var flagged bool
		if err := rows.Scan(&stored.ID, &stored.Phone, &stored.StartedAt, &stored.EndedAt, &stored.Status, &stored.AppointmentOutcome, &stored.BookingResult, &stored.CancellationResult, &receipts, &flagged); err != nil {
			rows.Close()
			return AgentCallsPage{}, err
		}
		page.Calls = append(page.Calls, agentCall(stored, receipts, flagged))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return AgentCallsPage{}, err
	}
	if len(page.Calls) > command.Limit {
		page.Calls = page.Calls[:command.Limit]
		last := page.Calls[len(page.Calls)-1]
		page.NextCursor, err = encodeCursor(agentCallsCursor{analyticsCursor: newAnalyticsCursor(command.QueryAnalyticsCommand, last.StartedAt, last.ID, through), Phone: phone, FlaggedOnly: command.FlaggedOnly})
		if err != nil {
			return AgentCallsPage{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentCallsPage{}, err
	}
	return page, nil
}

func agentCall(stored Interaction, receipts json.RawMessage, flagged bool) AgentCall {
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
	call := AgentCall{ID: stored.ID, Phone: stored.Phone, StartedAt: stored.StartedAt, AppointmentActions: actions, Transferred: stored.Status == CallEscalated, IssueFlagged: flagged}
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
	err = tx.QueryRow(ctx, `SELECT reason, created_at FROM ai_interaction_issues WHERE interaction_id = $1`, id).Scan(&issue.Reason, &issue.CreatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AgentCallDetail{}, err
	}
	detail := AgentCallDetail{LocationName: stored.LocationName, Messages: []AgentCallMessage{}}
	if err == nil {
		detail.Issue = &issue
	}
	var closeout struct {
		DomainOutcomes json.RawMessage `json:"domainOutcomes"`
		Evaluation     json.RawMessage `json:"evaluation"`
	}
	_ = json.Unmarshal(stored.CloseoutPayload, &closeout)
	detail.Call = agentCall(stored, closeout.DomainOutcomes, detail.Issue != nil)
	detail.Scorecard = agentCallScorecard(closeout.Evaluation)
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

func agentCallScorecard(evaluation json.RawMessage) []AgentCallVerdict {
	levels := map[string]string{}
	for _, answer := range judgeAnswers(evaluation) {
		levels[answer.Question] = scorecardLevel(answer.Answer, answer.Probability)
	}
	scorecard := []AgentCallVerdict{}
	for _, question := range ScorecardQuestions {
		if level, found := levels[question.Key]; found {
			scorecard = append(scorecard, AgentCallVerdict{Code: question.Code, Prompt: question.Prompt, Answer: level})
		}
	}
	return scorecard
}

func (m *Module) FlagAgentCallIssue(ctx context.Context, identity access.Identity, id string, reason AgentCallIssueReason) (AgentCallIssue, error) {
	switch reason {
	case AgentCallIssueWrongAppointmentType, AgentCallIssueInsurance, AgentCallIssueOther:
	default:
		return AgentCallIssue{}, ErrInvalidInput
	}
	tx, authorization, err := m.beginInteractionAccess(ctx, identity, id, true, audienceStaff)
	if err != nil {
		return AgentCallIssue{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `INSERT INTO ai_interaction_issues (interaction_id, reported_by, reason, note) VALUES ($1, $2, $3, $3) ON CONFLICT (interaction_id) DO NOTHING`, id, identity.Subject, reason)
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
	if err := tx.QueryRow(ctx, `SELECT reason, created_at, reported_by FROM ai_interaction_issues WHERE interaction_id = $1`, id).Scan(&issue.Reason, &issue.CreatedAt, &reporter); err != nil {
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
