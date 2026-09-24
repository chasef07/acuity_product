package interaction

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

// AgentCall is the staff projection. Diagnostic payloads never cross this boundary.
type AgentCall struct {
	ID                 string              `json:"id"`
	Phone              string              `json:"phone"`
	StartedAt          time.Time           `json:"startedAt"`
	DurationSeconds    *int                `json:"durationSeconds,omitempty"`
	AppointmentActions []AppointmentAction `json:"appointmentActions"`
	Transferred        bool                `json:"transferred"`
	IssueFlagged       bool                `json:"issueFlagged"`
}
type AgentCallIssue struct {
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"createdAt"`
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
	if m.database == nil || m.access == nil || !validRange || !validUUID(command.PracticeID) ||
		(command.LocationID != "" && !validUUID(command.LocationID)) || command.Limit < 1 || command.Limit > 100 || len(command.Phone) > 32 {
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
		raw, err := base64.RawURLEncoding.DecodeString(command.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.PracticeID != command.PracticeID || cursor.LocationID != command.LocationID || cursor.Range != command.Range || cursor.Phone != phone || cursor.FlaggedOnly != command.FlaggedOnly || !validUUID(cursor.ID) || cursor.Through.After(through) || cursor.Through.IsZero() || cursor.StartedAt.After(cursor.Through) || cursor.StartedAt.Before(cursor.Through.Add(-duration)) {
			return AgentCallsPage{}, ErrInvalidInput
		}
		through, startedAt, id = cursor.Through, cursor.StartedAt, cursor.ID
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AgentCallsPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockReadAuthorization(ctx, tx, command.Identity, command.PracticeID, command.LocationID)
	if err != nil {
		return AgentCallsPage{}, ErrDenied
	}
	locations := authorizedLocationIDs(authorization, command.LocationID)
	if len(locations) == 0 {
		return AgentCallsPage{}, ErrDenied
	}
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
		raw, err := json.Marshal(agentCallsCursor{analyticsCursor: analyticsCursor{Through: through, Range: command.Range, PracticeID: command.PracticeID, LocationID: command.LocationID, StartedAt: last.StartedAt, ID: last.ID}, Phone: phone, FlaggedOnly: command.FlaggedOnly})
		if err != nil {
			return AgentCallsPage{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
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
		if stringValue(outcome["status"]) != "success" || boolValue(recordValue(outcome["evidence"])["replayed"]) {
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
	// Historical calls retain receipt-derived appointment outcomes. An attempted
	// action or a successful tool invocation alone is never appointment proof.
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
	tx, stored, err := m.authorizeAgentCall(ctx, identity, id)
	if err != nil {
		return AgentCallDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var issue AgentCallIssue
	err = tx.QueryRow(ctx, `SELECT note, created_at FROM ai_interaction_issues WHERE interaction_id = $1`, id).Scan(&issue.Note, &issue.CreatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AgentCallDetail{}, err
	}
	detail := AgentCallDetail{LocationName: stored.LocationName, Messages: []AgentCallMessage{}}
	if err == nil {
		detail.Issue = &issue
	}
	var closeout struct {
		DomainOutcomes json.RawMessage `json:"domainOutcomes"`
	}
	_ = json.Unmarshal(stored.CloseoutPayload, &closeout)
	detail.Call = agentCall(stored, closeout.DomainOutcomes, detail.Issue != nil)
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

func (m *Module) FlagAgentCallIssue(ctx context.Context, identity access.Identity, id, note string) (AgentCallIssue, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) < 1 || utf8.RuneCountInString(note) > 2000 {
		return AgentCallIssue{}, ErrInvalidInput
	}
	tx, _, err := m.authorizeAgentCall(ctx, identity, id)
	if err != nil {
		return AgentCallIssue{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// One report per call makes retries safe and preserves the original reporter.
	_, err = tx.Exec(ctx, `INSERT INTO ai_interaction_issues (interaction_id, reported_by, note) VALUES ($1, $2, $3) ON CONFLICT (interaction_id) DO NOTHING`, id, identity.Subject, note)
	if err != nil {
		return AgentCallIssue{}, err
	}
	var issue AgentCallIssue
	var reporter string
	if err := tx.QueryRow(ctx, `SELECT note, created_at, reported_by FROM ai_interaction_issues WHERE interaction_id = $1`, id).Scan(&issue.Note, &issue.CreatedAt, &reporter); err != nil {
		return AgentCallIssue{}, err
	}
	if issue.Note != note || reporter != identity.Subject {
		return AgentCallIssue{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentCallIssue{}, err
	}
	return issue, nil
}

func (m *Module) authorizeAgentCall(ctx context.Context, identity access.Identity, id string) (pgx.Tx, Interaction, error) {
	if m.database == nil || m.access == nil || !validUUID(id) {
		return nil, Interaction{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, Interaction{}, err
	}
	stored, err := scanInteraction(tx.QueryRow(ctx, interactionSelect+` WHERE interaction.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrDenied
	}
	if err == nil {
		_, authErr := m.access.LockReadAuthorization(ctx, tx, identity, stored.PracticeID, stored.LocationID)
		if authErr != nil {
			err = ErrDenied
		}
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, Interaction{}, err
	}
	return tx, stored, nil
}
