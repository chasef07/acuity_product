package workspace

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

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type pageCursor struct {
	OccurredAt time.Time `json:"occurredAt"`
	ID         string    `json:"id"`
}

func (m *Module) QueryPhoneTimeline(
	ctx context.Context,
	command QueryPhoneTimelineCommand,
) (TimelinePage, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.Phone = strings.TrimSpace(command.Phone)
	if m.database == nil || m.access == nil || command.PracticeID == "" ||
		!isCanonicalPhone(command.Phone) {
		return TimelinePage{}, ErrInvalidInput
	}
	limit, err := timelineLimit(command.Limit)
	if err != nil {
		return TimelinePage{}, err
	}
	cursor, err := decodeTimelineItemCursor(command.Cursor)
	if err != nil {
		return TimelinePage{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TimelinePage{}, fmt.Errorf("begin phone Engagement History: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locationIDs, err := m.authorizedLocationIDs(
		ctx, tx, command.Identity, command.PracticeID, "",
	)
	if err != nil {
		return TimelinePage{}, err
	}

	items := make([]TimelineItem, 0, (limit+1)*4)
	messages, err := queryTimelineMessages(
		ctx, tx, command.PracticeID, locationIDs, command.Phone, "", cursor, limit, true,
	)
	if err != nil {
		return TimelinePage{}, fmt.Errorf("query phone Messages: %w", err)
	}
	items = append(items, messages...)
	calls, err := queryTimelineCalls(
		ctx, tx, command.PracticeID, locationIDs, command.Phone, cursor, limit, true,
	)
	if err != nil {
		return TimelinePage{}, fmt.Errorf("query phone Calls: %w", err)
	}
	items = append(items, calls...)
	aiInteractions, err := queryPhoneInteractions(
		ctx, tx, command.PracticeID, locationIDs, command.Phone, cursor, limit,
	)
	if err != nil {
		return TimelinePage{}, err
	}
	items = append(items, aiInteractions...)
	tasks, err := queryPhoneTaskActivities(
		ctx, tx, command.PracticeID, locationIDs, command.Phone, cursor, limit,
	)
	if err != nil {
		return TimelinePage{}, err
	}
	items = append(items, tasks...)

	page := paginateTimeline(items, limit, true)
	if err := tx.Commit(ctx); err != nil {
		return TimelinePage{}, fmt.Errorf("commit phone Engagement History: %w", err)
	}
	return page, nil
}

func (m *Module) QueryTimeline(
	ctx context.Context,
	command QueryTimelineCommand,
) (TimelinePage, error) {
	command.ThreadID = strings.TrimSpace(command.ThreadID)
	if m.database == nil || m.access == nil || command.ThreadID == "" {
		return TimelinePage{}, ErrInvalidInput
	}
	limit, err := timelineLimit(command.Limit)
	if err != nil {
		return TimelinePage{}, err
	}
	cursor, err := decodePageCursor(command.Cursor)
	if err != nil {
		return TimelinePage{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TimelinePage{}, fmt.Errorf("begin conversation timeline: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	thread, err := loadThread(ctx, tx, command.ThreadID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TimelinePage{}, ErrDenied
	}
	if err != nil {
		return TimelinePage{}, err
	}
	locationIDs, err := m.authorizedLocationIDs(
		ctx, tx, command.Identity, thread.PracticeID, thread.LocationID,
	)
	if err != nil {
		return TimelinePage{}, err
	}

	items := make([]TimelineItem, 0, (limit+1)*3)
	messages, err := queryTimelineMessages(
		ctx, tx, thread.PracticeID, locationIDs, thread.ExternalPhone,
		thread.ID, cursor, limit, false,
	)
	if err != nil {
		return TimelinePage{}, fmt.Errorf("query conversation Messages: %w", err)
	}
	items = append(items, messages...)
	calls, err := queryTimelineCalls(
		ctx, tx, thread.PracticeID, locationIDs, thread.ExternalPhone, cursor, limit, false,
	)
	if err != nil {
		return TimelinePage{}, fmt.Errorf("query conversation Calls: %w", err)
	}
	items = append(items, calls...)
	tasks, err := queryConversationTasks(
		ctx, tx, thread, command.Identity.Subject, cursor, limit,
	)
	if err != nil {
		return TimelinePage{}, err
	}
	items = append(items, tasks...)

	page := paginateTimeline(items, limit, false)
	if err := tx.Commit(ctx); err != nil {
		return TimelinePage{}, fmt.Errorf("commit conversation timeline: %w", err)
	}
	return page, nil
}

const messageProjectionSQL = `
	SELECT
		thread.id::text,
		thread.practice_id::text,
		thread.location_id::text,
		location.name,
		thread.office_phone,
		thread.external_phone,
		COALESCE(thread.display_name, ''),
		COALESCE(thread.name_source, ''),
		thread.outbound_blocked,
		thread.created_at,
		thread.updated_at,
		message.id::text,
		message.direction,
		COALESCE(message.body, ''),
		message.sender,
		message.destination,
		message.delivery_state,
		COALESCE(message.safe_failure_code, ''),
		COALESCE(message.provider_message_id, ''),
		COALESCE(message.task_id::text, ''),
		COALESCE(message.retry_of_message_id::text, ''),
		COALESCE(attachment.id::text, ''),
		COALESCE(attachment.direction, ''),
		COALESCE(attachment.state, ''),
		COALESCE(attachment.file_name, ''),
		COALESCE(attachment.content_type, ''),
		COALESCE(attachment.byte_size, 0),
		COALESCE(attachment.created_at, message.created_at),
		COALESCE(attachment.updated_at, message.updated_at),
		message.created_at,
		message.updated_at,
		message.version
	FROM messaging_messages message
	JOIN messaging_threads thread ON thread.id = message.thread_id
	JOIN access_locations location
		ON location.practice_id = thread.practice_id
		AND location.id = thread.location_id
	LEFT JOIN messaging_attachments attachment ON attachment.message_id = message.id
	WHERE thread.practice_id = $1
		AND thread.external_phone = $2
		AND thread.location_id::text = ANY($3::text[])
		AND ($7::uuid IS NULL OR thread.id = $7)
		AND (
			$4::timestamptz IS NULL
			OR message.created_at < $4
			OR (
				message.created_at = $4
				AND (
					($8 AND 'MESSAGE:' || message.id::text < $5)
					OR (NOT $8 AND message.id::text < $5)
				)
			)
		)
	ORDER BY message.created_at DESC, message.id DESC
	LIMIT $6`

func queryTimelineMessages(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	locationIDs []string,
	phone string,
	threadID string,
	cursor *pageCursor,
	limit int,
	keyedCursor bool,
) ([]TimelineItem, error) {
	var threadArgument any
	if threadID != "" {
		threadArgument = threadID
	}
	rows, err := tx.Query(ctx, messageProjectionSQL,
		practiceID, phone, locationIDs, nullableCursorTime(cursor),
		nullableCursorID(cursor), limit+1, threadArgument, keyedCursor,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TimelineItem, 0, limit+1)
	for rows.Next() {
		message, err := scanMessageProjection(rows)
		if err != nil {
			return nil, fmt.Errorf("scan Message: %w", err)
		}
		items = append(items, TimelineItem{
			Type: "MESSAGE", ID: message.ID, OccurredAt: message.CreatedAt, Message: message,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Messages: %w", err)
	}
	return items, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanMessageProjection(scanner rowScanner) (messaging.Message, error) {
	var message messaging.Message
	var attachment messaging.Attachment
	if err := scanner.Scan(
		&message.Thread.ID,
		&message.Thread.PracticeID,
		&message.Thread.LocationID,
		&message.Thread.LocationName,
		&message.Thread.OfficePhone,
		&message.Thread.ExternalPhone,
		&message.Thread.DisplayName,
		&message.Thread.NameSource,
		&message.Thread.OutboundBlocked,
		&message.Thread.CreatedAt,
		&message.Thread.UpdatedAt,
		&message.ID,
		&message.Direction,
		&message.Body,
		&message.Sender,
		&message.Destination,
		&message.Delivery,
		&message.SafeFailureCode,
		&message.ProviderMessageID,
		&message.TaskID,
		&message.RetryOfMessageID,
		&attachment.ID,
		&attachment.Direction,
		&attachment.State,
		&attachment.FileName,
		&attachment.ContentType,
		&attachment.ByteSize,
		&attachment.CreatedAt,
		&attachment.UpdatedAt,
		&message.CreatedAt,
		&message.UpdatedAt,
		&message.Version,
	); err != nil {
		return messaging.Message{}, err
	}
	if attachment.ID != "" {
		attachment.MessageID = message.ID
		message.Attachment = &attachment
	}
	return message, nil
}

const callProjectionSQL = `
	SELECT
		call.id::text,
		call.direction,
		call.created_at,
		call.ended_at,
		CASE
			WHEN bridged_staff.bridged_at IS NOT NULL AND call.ended_at IS NOT NULL
			THEN GREATEST(
				0,
				EXTRACT(EPOCH FROM (call.ended_at - bridged_staff.bridged_at))::bigint
			)
			ELSE 0
		END,
		call.location_id::text,
		location.name,
		COALESCE(membership.email, platform_operator.email, ''),
		COALESCE(handoff.transfer_reason, ''),
		CASE
			WHEN call.disposition_outcome IN ('FOLLOW_UP_REQUIRED', 'CREATE_TASK')
				THEN 'FOLLOW_UP_REQUIRED'
			WHEN call.disposition_outcome IS NOT NULL THEN 'RESOLVED'
			WHEN call.terminal_outcome = 'VOICEMAIL' THEN 'VOICEMAIL'
			WHEN call.terminal_outcome IN ('MISSED', 'ABANDONED') THEN 'MISSED'
			WHEN call.terminal_outcome IS NOT NULL AND bridged_staff.id IS NOT NULL
				THEN 'NEEDS_DISPOSITION'
			WHEN call.terminal_outcome IS NOT NULL THEN 'UNANSWERED'
			WHEN bridged_staff.state = 'BRIDGED' THEN 'CONNECTED'
			WHEN bridged_staff.state = 'BRIDGE_PENDING' THEN 'CONNECTING'
			ELSE 'RINGING'
		END
	FROM human_calling_calls call
	LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id
	JOIN access_locations location
		ON location.practice_id = call.practice_id
		AND location.id = call.location_id
	LEFT JOIN LATERAL (
		SELECT leg.id, leg.staff_subject, leg.state, leg.bridged_at
		FROM human_calling_call_legs leg
		WHERE leg.call_id = call.id AND leg.role = 'STAFF'
			AND leg.bridged_at IS NOT NULL
		ORDER BY leg.bridged_at DESC NULLS LAST, leg.updated_at DESC, leg.id DESC
		LIMIT 1
	) bridged_staff ON true
	LEFT JOIN access_memberships membership
		ON membership.practice_id = call.practice_id
		AND membership.user_subject = bridged_staff.staff_subject
	LEFT JOIN access_platform_operators platform_operator
		ON platform_operator.user_subject = bridged_staff.staff_subject
	WHERE call.practice_id = $1
		AND call.location_id::text = ANY($2::text[])
		AND COALESCE(handoff.phone, call.destination_phone) = $3
		AND (
			$4::timestamptz IS NULL
			OR call.created_at < $4
			OR (
				call.created_at = $4
				AND (
					($7 AND 'CALL:' || call.id::text < $5)
					OR (NOT $7 AND call.id::text < $5)
				)
			)
		)
	ORDER BY call.created_at DESC, call.id DESC
	LIMIT $6`

func queryTimelineCalls(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	locationIDs []string,
	phone string,
	cursor *pageCursor,
	limit int,
	keyedCursor bool,
) ([]TimelineItem, error) {
	rows, err := tx.Query(ctx, callProjectionSQL,
		practiceID, locationIDs, phone, nullableCursorTime(cursor),
		nullableCursorID(cursor), limit+1, keyedCursor,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TimelineItem, 0, limit+1)
	for rows.Next() {
		var call humancalling.CallHistoryItem
		if err := rows.Scan(
			&call.ID,
			&call.Direction,
			&call.StartedAt,
			&call.EndedAt,
			&call.DurationSeconds,
			&call.LocationID,
			&call.LocationName,
			&call.AnsweredByEmail,
			&call.TransferReason,
			&call.Outcome,
		); err != nil {
			return nil, fmt.Errorf("scan Call: %w", err)
		}
		call.Type = "CALL"
		items = append(items, TimelineItem{
			Type: "CALL", ID: call.ID, OccurredAt: call.StartedAt, Call: call,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Calls: %w", err)
	}
	return items, nil
}

func queryPhoneInteractions(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	locationIDs []string,
	phone string,
	cursor *pageCursor,
	limit int,
) ([]TimelineItem, error) {
	rows, err := tx.Query(ctx, `
		SELECT
			interaction.id::text,
			interaction.location_id::text,
			location.name,
			interaction.source_call_id,
			interaction.phone,
			COALESCE(interaction.external_patient_id, ''),
			interaction.started_at,
			interaction.ended_at,
			interaction.status,
			COALESCE(interaction.summary, ''),
			interaction.appointment_outcome,
			interaction.appointment_occurred_at,
			COALESCE(interaction.old_appointment_id, ''),
			COALESCE(interaction.new_appointment_id, '')
		FROM ai_interactions interaction
		JOIN access_locations location
			ON location.practice_id = interaction.practice_id
			AND location.id = interaction.location_id
		WHERE interaction.practice_id = $1
			AND interaction.location_id::text = ANY($2::text[])
			AND interaction.phone = $3
			AND (
				$4::timestamptz IS NULL
				OR interaction.started_at < $4
				OR (
					interaction.started_at = $4
					AND 'AI_INTERACTION:' || interaction.id::text < $5
				)
			)
		ORDER BY interaction.started_at DESC, interaction.id DESC
		LIMIT $6
	`, practiceID, locationIDs, phone, nullableCursorTime(cursor),
		nullableCursorID(cursor), limit+1)
	if err != nil {
		return nil, fmt.Errorf("query phone AI Interactions: %w", err)
	}
	defer rows.Close()
	items := make([]TimelineItem, 0, limit+1)
	for rows.Next() {
		var item interaction.OutcomeItem
		if err := rows.Scan(
			&item.ID,
			&item.LocationID,
			&item.LocationName,
			&item.SourceCallID,
			&item.Phone,
			&item.ExternalPatientID,
			&item.StartedAt,
			&item.EndedAt,
			&item.Status,
			&item.Summary,
			&item.AppointmentOutcome,
			&item.AppointmentOccurredAt,
			&item.OldAppointmentID,
			&item.NewAppointmentID,
		); err != nil {
			return nil, fmt.Errorf("scan phone AI Interaction: %w", err)
		}
		items = append(items, TimelineItem{
			Type: "AI_INTERACTION", ID: item.ID, OccurredAt: item.StartedAt,
			AIInteraction: item,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate phone AI Interactions: %w", err)
	}
	return items, nil
}

func queryPhoneTaskActivities(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	locationIDs []string,
	phone string,
	cursor *pageCursor,
	limit int,
) ([]TimelineItem, error) {
	rows, err := tx.Query(ctx, phoneTaskActivityQuery,
		practiceID, locationIDs, phone, nullableCursorTime(cursor),
		nullableCursorID(cursor), limit+1,
	)
	if err != nil {
		return nil, fmt.Errorf("query phone Tasks: %w", err)
	}
	defer rows.Close()
	items := make([]TimelineItem, 0, limit+1)
	for rows.Next() {
		var activityID, kind string
		var occurredAt time.Time
		task, err := scanTaskProjection(
			rows, &activityID, &kind, &occurredAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan phone Task Activity: %w", err)
		}
		items = append(items, TimelineItem{
			Type: "TASK", ID: activityID, OccurredAt: occurredAt,
			TaskActivity: kind, Task: task,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate phone Task Activities: %w", err)
	}
	return items, nil
}

func queryConversationTasks(
	ctx context.Context,
	tx pgx.Tx,
	thread messaging.Thread,
	userSubject string,
	cursor *pageCursor,
	limit int,
) ([]TimelineItem, error) {
	rows, err := tx.Query(ctx, conversationTaskQuery,
		thread.PracticeID, thread.LocationID, thread.ID, thread.ExternalPhone,
		nullableCursorTime(cursor), nullableCursorID(cursor), limit+1, userSubject,
	)
	if err != nil {
		return nil, fmt.Errorf("query conversation Tasks: %w", err)
	}
	defer rows.Close()
	items := make([]TimelineItem, 0, limit+1)
	for rows.Next() {
		task, err := scanTaskProjection(rows)
		if err != nil {
			return nil, fmt.Errorf("scan conversation Task: %w", err)
		}
		items = append(items, TimelineItem{
			Type: "TASK", ID: task.ID, OccurredAt: task.CreatedAt, Task: task,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conversation Tasks: %w", err)
	}
	return items, nil
}

func loadThread(ctx context.Context, tx pgx.Tx, threadID string) (messaging.Thread, error) {
	var thread messaging.Thread
	err := tx.QueryRow(ctx, `
		SELECT
			thread.id::text,
			thread.practice_id::text,
			thread.location_id::text,
			location.name,
			thread.office_phone,
			thread.external_phone,
			COALESCE(thread.display_name, ''),
			COALESCE(thread.name_source, ''),
			thread.outbound_blocked,
			thread.created_at,
			thread.updated_at
		FROM messaging_threads thread
		JOIN access_locations location
			ON location.practice_id = thread.practice_id
			AND location.id = thread.location_id
		WHERE thread.id = $1
		FOR SHARE OF thread
	`, threadID).Scan(
		&thread.ID,
		&thread.PracticeID,
		&thread.LocationID,
		&thread.LocationName,
		&thread.OfficePhone,
		&thread.ExternalPhone,
		&thread.DisplayName,
		&thread.NameSource,
		&thread.OutboundBlocked,
		&thread.CreatedAt,
		&thread.UpdatedAt,
	)
	if err != nil {
		return messaging.Thread{}, err
	}
	return thread, nil
}

func paginateTimeline(items []TimelineItem, limit int, keyed bool) TimelinePage {
	sort.Slice(items, func(left int, right int) bool {
		if items[left].OccurredAt.Equal(items[right].OccurredAt) {
			if keyed {
				return timelineItemKey(items[left]) > timelineItemKey(items[right])
			}
			return items[left].ID > items[right].ID
		}
		return items[left].OccurredAt.After(items[right].OccurredAt)
	})
	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		id := last.ID
		if keyed {
			id = timelineItemKey(last)
		}
		nextCursor = encodePageCursor(pageCursor{OccurredAt: last.OccurredAt, ID: id})
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	return TimelinePage{Items: items, NextCursor: nextCursor}
}

func timelineLimit(requested int) (int, error) {
	if requested == 0 {
		return 50, nil
	}
	if requested < 1 || requested > 50 {
		return 0, ErrInvalidInput
	}
	return requested, nil
}

func timelineItemKey(item TimelineItem) string {
	return item.Type + ":" + item.ID
}

func encodePageCursor(cursor pageCursor) string {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodePageCursor(raw string) (*pageCursor, error) {
	return decodeCursor(raw, false)
}

func decodeTimelineItemCursor(raw string) (*pageCursor, error) {
	return decodeCursor(raw, true)
}

func decodeCursor(raw string, keyed bool) (*pageCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var cursor pageCursor
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.OccurredAt.IsZero() {
		return nil, ErrInvalidInput
	}
	if !keyed {
		if uuid.Validate(cursor.ID) != nil {
			return nil, ErrInvalidInput
		}
		return &cursor, nil
	}
	parts := strings.SplitN(cursor.ID, ":", 2)
	if len(parts) != 2 ||
		(parts[0] != "MESSAGE" && parts[0] != "CALL" && parts[0] != "TASK") ||
		uuid.Validate(parts[1]) != nil {
		return nil, ErrInvalidInput
	}
	return &cursor, nil
}

func nullableCursorTime(cursor *pageCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.OccurredAt
}

func nullableCursorID(cursor *pageCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.ID
}

func isCanonicalPhone(phone string) bool {
	normalized, err := messaging.NormalizePhone(phone)
	return err == nil && normalized == phone
}
