package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errEngagementDenied       = errors.New("engagement access denied")
	errEngagementInvalidInput = errors.New("invalid engagement input")
)

type engagementLocation struct {
	ID   string
	Name string
}

type engagementSummary struct {
	Phone              string
	DisplayName        string
	Locations          []engagementLocation
	LatestActivity     time.Time
	OpenTaskCount      int
	Unread             bool
	TextNeedsAttention bool
}

type engagementPage struct{ Items []engagementSummary }

type engagementQueryCommand struct {
	Identity   access.Identity
	PracticeID string
	Phone      string
	Limit      int
}

type engagementTimelineQueryCommand struct {
	Identity   access.Identity
	PracticeID string
	Phone      string
	Cursor     string
	Limit      int
}

type engagementTimelineCursor struct {
	OccurredAt time.Time `json:"occurredAt"`
	ID         string    `json:"id"`
}

type engagementQueryAdapter struct {
	pool   *pgxpool.Pool
	access *access.Module
}

// This application read adapter owns no domain state. It composes the
// authorized number-inbox projection across the five owning modules' tables.

func newEngagementQueryAdapter(pool *pgxpool.Pool, accessModule *access.Module) *engagementQueryAdapter {
	return &engagementQueryAdapter{pool: pool, access: accessModule}
}

func (m *engagementQueryAdapter) query(ctx context.Context, command engagementQueryCommand) (engagementPage, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.Phone = strings.TrimSpace(command.Phone)
	limit := command.Limit
	if limit == 0 {
		limit = 7
	}
	if m.pool == nil || m.access == nil || command.PracticeID == "" || limit < 1 || limit > 10 {
		return engagementPage{}, errEngagementInvalidInput
	}
	phone := ""
	if command.Phone != "" {
		var err error
		phone, err = messaging.NormalizePhone(command.Phone)
		if err != nil {
			return engagementPage{}, errEngagementInvalidInput
		}
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return engagementPage{}, fmt.Errorf("begin Engagement lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockReadAuthorization(ctx, tx, command.Identity, command.PracticeID, "")
	if err != nil {
		return engagementPage{}, errEngagementDenied
	}
	locationIDs := make([]string, 0, len(authorization.Locations))
	for _, location := range authorization.Locations {
		locationIDs = append(locationIDs, location.ID)
	}
	if len(locationIDs) == 0 {
		return engagementPage{}, errEngagementDenied
	}
	phones := []string{phone}
	if phone == "" {
		rows, queryErr := tx.Query(ctx, `
			WITH evidence AS (
				SELECT external_phone AS phone, updated_at AS occurred_at
				FROM messaging_threads
				WHERE practice_id = $1 AND location_id::text = ANY($2::text[])
				UNION ALL
				SELECT COALESCE(handoff.phone, call.destination_phone), call.updated_at
				FROM human_calling_calls call
				LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.handoff_id
				WHERE call.practice_id = $1 AND call.location_id::text = ANY($2::text[])
				UNION ALL
				SELECT phone, updated_at FROM work_tasks
				WHERE practice_id = $1 AND location_id::text = ANY($2::text[])
				UNION ALL
				SELECT phone, created_at FROM work_staff_notes
				WHERE practice_id = $1 AND location_id::text = ANY($2::text[])
			)
			SELECT phone FROM evidence WHERE phone IS NOT NULL AND phone <> ''
			GROUP BY phone ORDER BY max(occurred_at) DESC, phone LIMIT $3
		`, command.PracticeID, locationIDs, limit)
		if queryErr != nil {
			return engagementPage{}, fmt.Errorf("query recent Engagement phones: %w", queryErr)
		}
		phones = phones[:0]
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				return engagementPage{}, fmt.Errorf("scan recent Engagement phone: %w", err)
			}
			phones = append(phones, value)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return engagementPage{}, fmt.Errorf("iterate recent Engagement phones: %w", err)
		}
		rows.Close()
	}
	items := make([]engagementSummary, 0, len(phones))
	for _, value := range phones {
		summary, found, queryErr := querySummary(ctx, tx, command, locationIDs, value)
		if queryErr != nil {
			return engagementPage{}, queryErr
		}
		if found {
			items = append(items, summary)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return engagementPage{}, fmt.Errorf("commit Engagement lookup: %w", err)
	}
	return engagementPage{Items: items}, nil
}

func querySummary(ctx context.Context, tx pgx.Tx, command engagementQueryCommand, locationIDs []string, phone string) (engagementSummary, bool, error) {
	var summary engagementSummary
	var found bool
	err := tx.QueryRow(ctx, `
		WITH evidence AS (
			SELECT location_id, updated_at AS occurred_at, COALESCE(display_name, '') AS display_name
			FROM messaging_threads WHERE practice_id = $1 AND location_id::text = ANY($2::text[]) AND external_phone = $3
			UNION ALL
			SELECT call.location_id, call.updated_at, COALESCE(handoff.display_name, '')
			FROM human_calling_calls call LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.handoff_id
			WHERE call.practice_id = $1 AND call.location_id::text = ANY($2::text[]) AND COALESCE(handoff.phone, call.destination_phone) = $3
			UNION ALL
			SELECT location_id, updated_at, COALESCE(caller_name, '') FROM work_tasks
			WHERE practice_id = $1 AND location_id::text = ANY($2::text[]) AND phone = $3
			UNION ALL
			SELECT location_id, created_at, '' FROM work_staff_notes
			WHERE practice_id = $1 AND location_id::text = ANY($2::text[]) AND phone = $3
		)
		SELECT $3,
			COALESCE((SELECT display_name FROM evidence WHERE display_name <> '' ORDER BY occurred_at DESC LIMIT 1), ''),
			COALESCE(max(occurred_at), '-infinity'::timestamptz),
			(SELECT count(*) FROM work_tasks WHERE practice_id = $1 AND location_id::text = ANY($2::text[]) AND phone = $3 AND state = 'OPEN'),
			EXISTS (SELECT 1 FROM messaging_threads thread JOIN messaging_thread_unreads unread ON unread.thread_id = thread.id WHERE thread.practice_id = $1 AND thread.location_id::text = ANY($2::text[]) AND thread.external_phone = $3 AND unread.user_subject = $4),
			EXISTS (
				SELECT 1 FROM messaging_threads thread
				WHERE thread.practice_id = $1 AND thread.location_id::text = ANY($2::text[]) AND thread.external_phone = $3
				AND EXISTS (
					SELECT 1 FROM messaging_messages inbound
					WHERE inbound.thread_id = thread.id AND inbound.direction = 'INBOUND'
					AND inbound.created_at > COALESCE((SELECT handled_through FROM messaging_thread_handled handled WHERE handled.thread_id = thread.id), '-infinity'::timestamptz)
				)
			),
			count(*) > 0
		FROM evidence
	`, command.PracticeID, locationIDs, phone, command.Identity.Subject).Scan(
		&summary.Phone, &summary.DisplayName, &summary.LatestActivity, &summary.OpenTaskCount,
		&summary.Unread, &summary.TextNeedsAttention, &found,
	)
	if err != nil {
		return engagementSummary{}, false, fmt.Errorf("query Engagement summary: %w", err)
	}
	if !found {
		return engagementSummary{}, false, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT location.id::text, location.name
		FROM access_locations location
		JOIN (
			SELECT location_id FROM messaging_threads WHERE practice_id = $1 AND external_phone = $3
			UNION SELECT call.location_id FROM human_calling_calls call LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.handoff_id WHERE call.practice_id = $1 AND COALESCE(handoff.phone, call.destination_phone) = $3
			UNION SELECT location_id FROM work_tasks WHERE practice_id = $1 AND phone = $3
			UNION SELECT location_id FROM work_staff_notes WHERE practice_id = $1 AND phone = $3
		) evidence ON evidence.location_id = location.id
		WHERE location.practice_id = $1 AND location.id::text = ANY($2::text[])
		ORDER BY location.name, location.id::text
	`, command.PracticeID, locationIDs, phone)
	if err != nil {
		return engagementSummary{}, false, fmt.Errorf("query Engagement Locations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var location engagementLocation
		if err := rows.Scan(&location.ID, &location.Name); err != nil {
			return engagementSummary{}, false, fmt.Errorf("scan Engagement engagementLocation: %w", err)
		}
		summary.Locations = append(summary.Locations, location)
	}
	if err := rows.Err(); err != nil {
		return engagementSummary{}, false, fmt.Errorf("iterate Engagement Locations: %w", err)
	}
	return summary, true, nil
}

func (m *engagementQueryAdapter) queryTimeline(
	ctx context.Context,
	command engagementTimelineQueryCommand,
) (messaging.TimelinePage, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	phone, err := messaging.NormalizePhone(command.Phone)
	if err != nil || m.pool == nil || m.access == nil || command.PracticeID == "" {
		return messaging.TimelinePage{}, messaging.ErrInvalidInput
	}
	limit := command.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 50 {
		return messaging.TimelinePage{}, messaging.ErrInvalidInput
	}
	cursor, err := decodeEngagementTimelineCursor(command.Cursor)
	if err != nil {
		return messaging.TimelinePage{}, messaging.ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return messaging.TimelinePage{}, fmt.Errorf("begin Engagement History: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockReadAuthorization(
		ctx, tx, command.Identity, command.PracticeID, "",
	)
	if err != nil {
		return messaging.TimelinePage{}, messaging.ErrDenied
	}
	locationIDs := make([]string, 0, len(authorization.Locations))
	for _, location := range authorization.Locations {
		locationIDs = append(locationIDs, location.ID)
	}
	if len(locationIDs) == 0 {
		return messaging.TimelinePage{}, messaging.ErrDenied
	}

	items := make([]messaging.TimelineItem, 0, (limit+1)*4)
	messageRows, err := tx.Query(ctx, `
		SELECT thread.id::text, thread.practice_id::text, thread.location_id::text,
			location.name, thread.office_phone, thread.external_phone,
			COALESCE(thread.display_name, ''), COALESCE(thread.name_source, ''),
			thread.outbound_blocked, thread.created_at, thread.updated_at,
			message.id::text, message.direction, COALESCE(message.body, ''),
			message.sender, message.destination, message.delivery_state,
			COALESCE(message.safe_failure_code, ''), COALESCE(message.provider_message_id, ''),
			COALESCE(message.task_id::text, ''), COALESCE(message.retry_of_message_id::text, ''),
			COALESCE(attachment.id::text, ''), COALESCE(attachment.direction, ''),
			COALESCE(attachment.state, ''), COALESCE(attachment.file_name, ''),
			COALESCE(attachment.content_type, ''), COALESCE(attachment.byte_size, 0),
			COALESCE(attachment.created_at, message.created_at),
			COALESCE(attachment.updated_at, message.updated_at),
			message.created_at, message.updated_at, message.version
		FROM messaging_messages message
		JOIN messaging_threads thread ON thread.id = message.thread_id
		JOIN access_locations location ON location.practice_id = thread.practice_id
			AND location.id = thread.location_id
		LEFT JOIN messaging_attachments attachment ON attachment.message_id = message.id
		WHERE thread.practice_id = $1 AND thread.external_phone = $2
			AND thread.location_id::text = ANY($3::text[])
			AND ($4::timestamptz IS NULL OR message.created_at < $4
				OR (message.created_at = $4 AND 'MESSAGE:' || message.id::text < $5))
		ORDER BY message.created_at DESC, message.id DESC LIMIT $6
	`, command.PracticeID, phone, locationIDs, engagementCursorTime(cursor),
		engagementCursorID(cursor), limit+1)
	if err != nil {
		return messaging.TimelinePage{}, fmt.Errorf("query Engagement Messages: %w", err)
	}
	for messageRows.Next() {
		var message messaging.Message
		var attachment messaging.Attachment
		if err := messageRows.Scan(
			&message.Thread.ID, &message.Thread.PracticeID, &message.Thread.LocationID,
			&message.Thread.LocationName, &message.Thread.OfficePhone,
			&message.Thread.ExternalPhone, &message.Thread.DisplayName,
			&message.Thread.NameSource, &message.Thread.OutboundBlocked,
			&message.Thread.CreatedAt, &message.Thread.UpdatedAt, &message.ID,
			&message.Direction, &message.Body, &message.Sender, &message.Destination,
			&message.Delivery, &message.SafeFailureCode, &message.ProviderMessageID,
			&message.TaskID, &message.RetryOfMessageID, &attachment.ID,
			&attachment.Direction, &attachment.State, &attachment.FileName,
			&attachment.ContentType, &attachment.ByteSize, &attachment.CreatedAt,
			&attachment.UpdatedAt, &message.CreatedAt, &message.UpdatedAt,
			&message.Version,
		); err != nil {
			messageRows.Close()
			return messaging.TimelinePage{}, fmt.Errorf("scan Engagement Message: %w", err)
		}
		if attachment.ID != "" {
			attachment.MessageID = message.ID
			message.Attachment = &attachment
		}
		items = append(items, messaging.TimelineItem{
			Type: "MESSAGE", ID: message.ID, OccurredAt: message.CreatedAt, Message: message,
		})
	}
	if err := messageRows.Err(); err != nil {
		messageRows.Close()
		return messaging.TimelinePage{}, fmt.Errorf("iterate Engagement Messages: %w", err)
	}
	messageRows.Close()

	callRows, err := tx.Query(ctx, `
		SELECT call.id::text, call.direction, call.created_at, call.ended_at,
			CASE WHEN call.connected_at IS NOT NULL AND call.ended_at IS NOT NULL
				THEN GREATEST(0, EXTRACT(EPOCH FROM (call.ended_at - call.connected_at))::bigint)
				ELSE 0 END,
			call.location_id::text, location.name, COALESCE(membership.email, ''),
			COALESCE(handoff.transfer_reason, ''), call.state
		FROM human_calling_calls call
		LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.handoff_id
		JOIN access_locations location ON location.practice_id = call.practice_id
			AND location.id = call.location_id
		LEFT JOIN access_memberships membership ON membership.practice_id = call.practice_id
			AND membership.user_subject = call.winner_subject
		WHERE call.practice_id = $1 AND call.location_id::text = ANY($2::text[])
			AND COALESCE(handoff.phone, call.destination_phone) = $3
			AND ($4::timestamptz IS NULL OR call.created_at < $4
				OR (call.created_at = $4 AND 'CALL:' || call.id::text < $5))
		ORDER BY call.created_at DESC, call.id DESC LIMIT $6
	`, command.PracticeID, locationIDs, phone, engagementCursorTime(cursor),
		engagementCursorID(cursor), limit+1)
	if err != nil {
		return messaging.TimelinePage{}, fmt.Errorf("query Engagement Calls: %w", err)
	}
	for callRows.Next() {
		var call humancalling.CallHistoryItem
		if err := callRows.Scan(&call.ID, &call.Direction, &call.StartedAt,
			&call.EndedAt, &call.DurationSeconds, &call.LocationID,
			&call.LocationName, &call.AnsweredByEmail, &call.TransferReason,
			&call.Outcome); err != nil {
			callRows.Close()
			return messaging.TimelinePage{}, fmt.Errorf("scan Engagement Call: %w", err)
		}
		call.Type = "CALL"
		items = append(items, messaging.TimelineItem{
			Type: "CALL", ID: call.ID, OccurredAt: call.StartedAt, Call: call,
		})
	}
	if err := callRows.Err(); err != nil {
		callRows.Close()
		return messaging.TimelinePage{}, fmt.Errorf("iterate Engagement Calls: %w", err)
	}
	callRows.Close()

	taskRows, err := tx.Query(ctx, `
		SELECT activity.id::text, activity.kind, activity.occurred_at,
			task.id::text, task.practice_id::text, task.location_id::text, location.name,
			task.call_id::text, task.phone, task.title, task.state, task.origin,
			task.urgency, task.category, task.caller_name, task.source_call_id,
			task.source_message, task.source_message_id::text, task.message_thread_id::text,
			task.recovery_outcome, task.created_by_kind, task.created_by_subject,
			task.created_by_email, task.created_at, task.completed_by_subject,
			task.completed_by_email, task.completed_at, task.version, task.updated_at
		FROM work_tasks task
		JOIN work_task_activities activity ON activity.task_id = task.id
		JOIN access_locations location ON location.practice_id = task.practice_id
			AND location.id = task.location_id
		WHERE task.practice_id = $1 AND task.location_id::text = ANY($2::text[])
			AND task.phone = $3
			AND ($4::timestamptz IS NULL OR activity.occurred_at < $4
				OR (activity.occurred_at = $4 AND 'TASK:' || activity.id::text < $5))
		ORDER BY activity.occurred_at DESC, activity.id DESC LIMIT $6
	`, command.PracticeID, locationIDs, phone, engagementCursorTime(cursor),
		engagementCursorID(cursor), limit+1)
	if err != nil {
		return messaging.TimelinePage{}, fmt.Errorf("query Engagement Tasks: %w", err)
	}
	for taskRows.Next() {
		var item messaging.TimelineItem
		item.Type = "TASK"
		if err := scanEngagementTask(taskRows, &item); err != nil {
			taskRows.Close()
			return messaging.TimelinePage{}, fmt.Errorf("scan Engagement Task: %w", err)
		}
		items = append(items, item)
	}
	if err := taskRows.Err(); err != nil {
		taskRows.Close()
		return messaging.TimelinePage{}, fmt.Errorf("iterate Engagement Tasks: %w", err)
	}
	taskRows.Close()

	noteRows, err := tx.Query(ctx, `
		SELECT note.id::text, note.practice_id::text, note.location_id::text,
			location.name, note.phone, note.body, note.created_by_subject,
			note.created_by_email, note.created_at
		FROM work_staff_notes note
		JOIN access_locations location ON location.practice_id = note.practice_id
			AND location.id = note.location_id
		WHERE note.practice_id = $1 AND note.location_id::text = ANY($2::text[])
			AND note.phone = $3
			AND ($4::timestamptz IS NULL OR note.created_at < $4
				OR (note.created_at = $4 AND 'NOTE:' || note.id::text < $5))
		ORDER BY note.created_at DESC, note.id DESC LIMIT $6
	`, command.PracticeID, locationIDs, phone, engagementCursorTime(cursor),
		engagementCursorID(cursor), limit+1)
	if err != nil {
		return messaging.TimelinePage{}, fmt.Errorf("query Engagement Staff Notes: %w", err)
	}
	for noteRows.Next() {
		var note work.StaffNote
		if err := noteRows.Scan(&note.ID, &note.PracticeID, &note.LocationID,
			&note.LocationName, &note.Phone, &note.Body, &note.CreatedBy.Subject,
			&note.CreatedBy.Email, &note.CreatedAt); err != nil {
			noteRows.Close()
			return messaging.TimelinePage{}, fmt.Errorf("scan Engagement Staff Note: %w", err)
		}
		note.CreatedBy.Kind = access.ActorHuman
		items = append(items, messaging.TimelineItem{
			Type: "NOTE", ID: note.ID, OccurredAt: note.CreatedAt, Note: note,
		})
	}
	if err := noteRows.Err(); err != nil {
		noteRows.Close()
		return messaging.TimelinePage{}, fmt.Errorf("iterate Engagement Staff Notes: %w", err)
	}
	noteRows.Close()

	sort.Slice(items, func(left, right int) bool {
		if items[left].OccurredAt.Equal(items[right].OccurredAt) {
			return engagementTimelineItemKey(items[left]) > engagementTimelineItemKey(items[right])
		}
		return items[left].OccurredAt.After(items[right].OccurredAt)
	})
	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		nextCursor = encodeEngagementTimelineCursor(engagementTimelineCursor{
			OccurredAt: last.OccurredAt, ID: engagementTimelineItemKey(last),
		})
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	if err := tx.Commit(ctx); err != nil {
		return messaging.TimelinePage{}, fmt.Errorf("commit Engagement History: %w", err)
	}
	return messaging.TimelinePage{Items: items, NextCursor: nextCursor}, nil
}

func scanEngagementTask(scanner interface{ Scan(...any) error }, item *messaging.TimelineItem) error {
	var task work.Task
	var callID, category, callerName, sourceCall, sourceMessage *string
	var messageID, messageThreadID, recoveryOutcome *string
	var createdEmail, completedSubject, completedEmail *string
	if err := scanner.Scan(
		&item.ID, &item.TaskActivity, &item.OccurredAt,
		&task.ID, &task.PracticeID, &task.LocationID, &task.LocationName,
		&callID, &task.Phone, &task.Title, &task.State, &task.Origin,
		&task.Urgency, &category, &callerName, &sourceCall, &sourceMessage,
		&messageID, &messageThreadID, &recoveryOutcome, &task.CreatedBy.Kind,
		&task.CreatedBy.Subject, &createdEmail, &task.CreatedAt,
		&completedSubject, &completedEmail, &task.CompletedAt,
		&task.Version, &task.UpdatedAt,
	); err != nil {
		return err
	}
	if callID != nil {
		task.CallID = *callID
	}
	if category != nil {
		task.Category = work.TaskCategory(*category)
	}
	if callerName != nil {
		task.CallerName = *callerName
	}
	if sourceCall != nil {
		task.SourceCallID = *sourceCall
	}
	if sourceMessage != nil {
		task.SourceMessage = *sourceMessage
	}
	if messageID != nil {
		task.MessageID = *messageID
	}
	if messageThreadID != nil {
		task.MessageThreadID = *messageThreadID
	}
	if recoveryOutcome != nil {
		task.RecoveryOutcome = work.RecoveryOutcome(*recoveryOutcome)
	}
	if createdEmail != nil {
		task.CreatedBy.Email = *createdEmail
	}
	if completedSubject != nil && completedEmail != nil {
		task.CompletedBy = &work.ActorSnapshot{
			Kind: access.ActorHuman, Subject: *completedSubject, Email: *completedEmail,
		}
	}
	item.Task = task
	return nil
}

func engagementTimelineItemKey(item messaging.TimelineItem) string {
	return item.Type + ":" + item.ID
}

func encodeEngagementTimelineCursor(cursor engagementTimelineCursor) string {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeEngagementTimelineCursor(raw string) (*engagementTimelineCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var cursor engagementTimelineCursor
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.OccurredAt.IsZero() {
		return nil, messaging.ErrInvalidInput
	}
	parts := strings.SplitN(cursor.ID, ":", 2)
	if len(parts) != 2 ||
		(parts[0] != "MESSAGE" && parts[0] != "CALL" &&
			parts[0] != "TASK" && parts[0] != "NOTE") ||
		uuid.Validate(parts[1]) != nil {
		return nil, messaging.ErrInvalidInput
	}
	return &cursor, nil
}

func engagementCursorTime(cursor *engagementTimelineCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.OccurredAt
}

func engagementCursorID(cursor *engagementTimelineCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.ID
}
