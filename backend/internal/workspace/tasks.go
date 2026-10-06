package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (m *Module) QueryTasks(
	ctx context.Context,
	command QueryTasksCommand,
) (work.TaskPage, error) {
	command.Search = strings.TrimSpace(command.Search)
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.LocationID = strings.TrimSpace(command.LocationID)
	if command.State == "" {
		command.State = work.TaskOpen
	}
	if command.Ordering == "" {
		command.Ordering = work.TaskOrderingPriority
	}
	if m.database == nil || m.access == nil || command.PracticeID == "" ||
		len(command.Search) > 500 ||
		(command.Kind != "" && command.Kind != "texts" && command.Kind != "calls" && command.Kind != "appointments" && command.Kind != "follow_up") ||
		(command.Responsibility != "" && command.Responsibility != "mine" && command.Responsibility != "all") ||
		(command.State != work.TaskOpen && command.State != work.TaskCompleted) ||
		(command.Ordering != work.TaskOrderingTime &&
			command.Ordering != work.TaskOrderingPriority &&
			command.Ordering != work.TaskOrderingRecent) {
		return work.TaskPage{}, ErrInvalidInput
	}
	limit := command.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 50 {
		return work.TaskPage{}, ErrInvalidInput
	}
	cursor, err := decodeTaskCursor(
		command.Cursor,
		command.Ordering,
		command.State,
		command.Kind,
	)
	if err != nil {
		return work.TaskPage{}, ErrInvalidInput
	}

	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return work.TaskPage{}, fmt.Errorf("begin Task query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locationIDs, err := m.authorizedLocationIDs(
		ctx, tx, command.Identity, command.PracticeID, command.LocationID,
	)
	if err != nil {
		return work.TaskPage{}, err
	}

	rows, err := tx.Query(ctx, taskQuerySQL(command.State, command.Ordering, command.Grouped),
		command.PracticeID,
		locationIDs,
		command.Search,
		normalizedDigits(command.Search),
		cursor.Present,
		cursor.OrderedAt,
		cursor.ID,
		cursor.Urgency.Rank(),
		limit+1,
		command.Responsibility, strings.ToLower(command.Identity.Email), command.Category, command.Kind,
	)
	if err != nil {
		return work.TaskPage{}, fmt.Errorf("query Tasks: %w", err)
	}
	items := make([]work.Task, 0, limit+1)
	for rows.Next() {
		task, err := scanTaskProjection(rows)
		if err != nil {
			rows.Close()
			return work.TaskPage{}, fmt.Errorf("scan Task query: %w", err)
		}
		items = append(items, task)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return work.TaskPage{}, fmt.Errorf("iterate Tasks: %w", err)
	}
	rows.Close()
	var counts *work.TaskFolderCounts
	if command.IncludeCounts == nil || *command.IncludeCounts {
		value, err := queryTaskFolderCounts(
			ctx, tx, command.PracticeID, locationIDs,
			command.Search, normalizedDigits(command.Search), command.State, command,
		)
		if err != nil {
			return work.TaskPage{}, err
		}
		counts = &value
	}
	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		nextCursor, err = encodeTaskCursor(
			items[len(items)-1],
			command.Ordering,
			command.Kind,
		)
		if err != nil {
			return work.TaskPage{}, err
		}
	}
	if command.Grouped && command.State == work.TaskOpen {
		if err := loadGroupMembers(ctx, tx, items); err != nil {
			return work.TaskPage{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return work.TaskPage{}, fmt.Errorf("commit Task query: %w", err)
	}
	return work.TaskPage{Items: items, NextCursor: nextCursor, Counts: counts}, nil
}

func (m *Module) ReadTask(
	ctx context.Context,
	identity access.Identity,
	taskID string,
) (work.Task, error) {
	taskID = strings.TrimSpace(taskID)
	if m.database == nil || m.access == nil || taskID == "" {
		return work.Task{}, ErrDenied
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return work.Task{}, fmt.Errorf("begin Task read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := scanTaskProjection(tx.QueryRow(ctx, taskReadQuery, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return work.Task{}, ErrDenied
	}
	if err != nil {
		return work.Task{}, fmt.Errorf("read Task: %w", err)
	}
	if err := work.LoadTaskInteractions(ctx, tx, &task); err != nil {
		return work.Task{}, err
	}
	task.RelatedInteractionCount = len(task.Interactions)
	if _, err := m.authorizedLocationIDs(
		ctx, tx, identity, task.PracticeID, task.LocationID,
	); err != nil {
		return work.Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return work.Task{}, fmt.Errorf("commit Task read: %w", err)
	}
	return task, nil
}

const taskColumns = `
		task.id::text,
		task.practice_id::text,
		task.location_id::text,
		location.name,
		task.call_id::text,
		task.phone,
		task.title,
		task.state,
		task.origin,
		task.urgency,
		task.category,
		task.caller_name,
		task.source_call_id,
		task.source_message,
        COALESCE(CASE WHEN ` + work.TaskIsTextReviewSQL + ` THEN (
          SELECT COALESCE(NULLIF(message.body,''),'Attachment') FROM messaging_messages message
          WHERE message.thread_id=task.message_thread_id AND message.direction='INBOUND'
          ORDER BY message.created_at DESC,message.id DESC LIMIT 1
        ) END,''),
		task.source_message_id::text,
		task.message_thread_id::text,
		task.recovery_outcome,
		task.created_by_kind,
		task.created_by_subject,
		task.created_by_email,
		task.created_at,
		task.completed_by_subject,
		task.completed_by_email,
		task.completed_at,
		task.version,
		task.updated_at,
		acknowledgement.state,
		acknowledgement.safe_failure_code,
		acknowledgement.message_id::text,
		acknowledgement.updated_at`

const taskAcknowledgementJoin = `
	LEFT JOIN work_task_acknowledgements acknowledgement
		ON acknowledgement.task_id = task.id
		AND acknowledgement.purpose = 'CALLER_TASK_RECEIVED'`

const taskConversationJoin = `
	LEFT JOIN LATERAL (
		SELECT candidate.id
		FROM messaging_threads candidate
		WHERE candidate.practice_id = task.practice_id
			AND candidate.location_id = task.location_id
			AND (
				(task.message_thread_id IS NOT NULL AND candidate.id = task.message_thread_id)
				OR (task.message_thread_id IS NULL AND candidate.external_phone = task.phone)
			)
		ORDER BY candidate.updated_at DESC, candidate.id DESC
		LIMIT 1
	) conversation ON true`

const taskQueryColumns = `
	SELECT` + taskColumns + `,
		COALESCE(conversation.id::text, ''),
		(
			SELECT count(*)
			FROM work_task_interactions interaction
			WHERE interaction.task_id = task.id
		)`

const taskProjectionJoins = `
	JOIN access_locations location
		ON location.practice_id = task.practice_id
		AND location.id = task.location_id` + taskAcknowledgementJoin + taskConversationJoin

const taskHasRecentTextAttention = `(NOT ` + work.TaskIsTextReviewSQL + `
 OR task.state <> 'OPEN'
 OR EXISTS (
   SELECT 1 FROM messaging_messages recent_message
   WHERE recent_message.thread_id=task.message_thread_id
     AND recent_message.direction='INBOUND'
     AND recent_message.created_at >= CURRENT_TIMESTAMP - INTERVAL '120 hours'
 ))`

const taskIsSpringHillReview = `(` + work.TaskIsAppointmentReviewSQL + ` AND EXISTS (
 SELECT 1 FROM access_abita_office_locations route
 WHERE route.practice_id=task.practice_id AND route.location_id=task.location_id
 AND route.office_key='spring-hill'
))`

const taskMatchSource = `
	FROM work_tasks task
	JOIN access_locations location
		ON location.practice_id = task.practice_id
		AND location.id = task.location_id`

const taskSearchFilter = `
		AND (
			$3 = ''
				OR strpos(lower(task.title), lower($3)) > 0
				OR strpos(lower(COALESCE(task.caller_name, '')), lower($3)) > 0
				OR strpos(lower(location.name), lower($3)) > 0
				OR strpos(lower(COALESCE(task.category, '')), lower($3)) > 0
				OR ($4 <> '' AND task.phone_digits LIKE '%' || $4 || '%')
		)`

var taskQueryFilter = `
	WHERE task.practice_id = $1
		AND task.location_id = ANY($2::uuid[])` + taskResponsibilityFilter("$10", "$11") + `
 AND ($12::text = '' OR task.category=$12)
 AND ($13::text <> 'texts' OR ` + taskHasRecentTextAttention + `)
 AND ($13::text = '' OR ($13='texts' AND ` + work.TaskIsTextReviewSQL + `) OR ($13='calls' AND ` + work.TaskIsCallRecoverySQL + `)
 OR ($13='appointments' AND ` + taskIsSpringHillReview + `)
 OR ($13='follow_up' AND ` + work.TaskIsFollowUpSQL + `))` + taskSearchFilter

func taskQuerySQL(state work.TaskState, ordering work.TaskOrdering, grouped bool) string {
	window, order := taskPageWindow(state, ordering)
	candidates := taskMatchSource + taskQueryFilter
	if grouped && state == work.TaskOpen {
		candidates = `
	FROM (
		SELECT task.*, row_number() OVER (
			PARTITION BY task.practice_id, task.location_id, task.phone, task.category, task.origin
			ORDER BY ` + order + `
		) AS member_rank` + taskMatchSource + taskQueryFilter + `
			AND task.state = 'OPEN'
	) task
	WHERE task.member_rank = 1`
	}
	return `WITH page AS (
	SELECT task.id` + candidates + `
		AND ` + window + `
	ORDER BY ` + order + `
	LIMIT $9
)` + taskQueryColumns + `
	FROM page
	JOIN work_tasks task ON task.id = page.id` + taskProjectionJoins + `
	ORDER BY ` + order
}

func taskPageWindow(state work.TaskState, ordering work.TaskOrdering) (string, string) {
	switch {
	case state == work.TaskOpen && ordering == work.TaskOrderingPriority:
		return `task.state = 'OPEN'
		AND (
			NOT $5
			OR ` + work.TaskUrgencyRankSQL + ` > $8
			OR (
				` + work.TaskUrgencyRankSQL + ` = $8
				AND (task.created_at, task.id::text) > ($6, $7)
			)
		)`, work.TaskUrgencyRankSQL + `, task.created_at, task.id`
	case state == work.TaskOpen && ordering == work.TaskOrderingTime:
		return `task.state = 'OPEN'
		AND $8::int >= 0
		AND (NOT $5 OR (task.created_at, task.id::text) > ($6, $7))`, `task.created_at, task.id`
	case state == work.TaskOpen:
		return `task.state = 'OPEN'
		AND $8::int >= 0
		AND (NOT $5 OR (task.updated_at, task.id::text) < ($6, $7))`, `task.updated_at DESC, task.id DESC`
	default:
		return `task.state = 'COMPLETED'
		AND $8::int >= 0
		AND (NOT $5 OR (task.completed_at, task.id::text) < ($6, $7))`, `task.completed_at DESC, task.id DESC`
	}
}

const taskReadColumns = taskColumns + `,
		COALESCE(conversation.id::text, ''),
		(SELECT count(*) FROM work_task_interactions interaction WHERE interaction.task_id=task.id)`

const taskReadQuery = `SELECT` + taskReadColumns + ` FROM work_tasks task` + taskProjectionJoins + `
	WHERE task.id = $1`

const phoneTaskActivityQuery = `
	SELECT
		activity.id::text,
		activity.kind,
		activity.occurred_at, activity.details,` + taskColumns + `,
		COALESCE(task.message_thread_id::text, ''),
		0
	FROM work_tasks task
	JOIN access_locations location
		ON location.practice_id = task.practice_id
		AND location.id = task.location_id
	LEFT JOIN work_task_acknowledgements acknowledgement
		ON acknowledgement.task_id = task.id
		AND acknowledgement.purpose = 'CALLER_TASK_RECEIVED'
	JOIN work_task_activities activity ON activity.task_id = task.id
	WHERE task.practice_id = $1
		AND task.location_id = ANY($2::uuid[])
		AND task.phone = $3
		AND ($7::uuid[] IS NULL OR activity.id = ANY($7))
		AND (
			$4::timestamptz IS NULL
			OR activity.occurred_at < $4
			OR (
				activity.occurred_at = $4
				AND 'TASK:' || activity.id::text < $5
			)
		)
	ORDER BY activity.occurred_at DESC, activity.id DESC
	LIMIT $6`

func scanTaskProjection(scanner rowScanner, prefix ...any) (work.Task, error) {
	var task work.Task
	var callID, category, callerName, sourceCall, sourceMessage *string
	var messageID, messageThreadID, recoveryOutcome *string
	var createdEmail, completedSubject, completedEmail *string
	var acknowledgementState, acknowledgementFailure, acknowledgementMessageID *string
	var acknowledgementUpdatedAt *time.Time
	destinations := append(prefix,
		&task.ID,
		&task.PracticeID,
		&task.LocationID,
		&task.LocationName,
		&callID,
		&task.Phone,
		&task.Title,
		&task.State,
		&task.Origin,
		&task.Urgency,
		&category,
		&callerName,
		&sourceCall,
		&sourceMessage,
		&task.Preview,
		&messageID,
		&messageThreadID,
		&recoveryOutcome,
		&task.CreatedBy.Kind,
		&task.CreatedBy.Subject,
		&createdEmail,
		&task.CreatedAt,
		&completedSubject,
		&completedEmail,
		&task.CompletedAt,
		&task.Version,
		&task.UpdatedAt,
		&acknowledgementState,
		&acknowledgementFailure,
		&acknowledgementMessageID,
		&acknowledgementUpdatedAt,
		&task.ConversationThreadID,
		&task.RelatedInteractionCount,
	)
	if err := scanner.Scan(destinations...); err != nil {
		return work.Task{}, err
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
	if acknowledgementState != nil && acknowledgementUpdatedAt != nil {
		task.AutomaticAcknowledgement = &work.TaskAcknowledgement{
			State:     *acknowledgementState,
			UpdatedAt: *acknowledgementUpdatedAt,
		}
		if acknowledgementFailure != nil {
			task.AutomaticAcknowledgement.SafeFailureCode = *acknowledgementFailure
		}
		if acknowledgementMessageID != nil {
			task.AutomaticAcknowledgement.MessageID = *acknowledgementMessageID
		}
	}
	if createdEmail != nil {
		task.CreatedBy.Email = *createdEmail
	}
	if completedSubject != nil {
		kind := access.ActorService
		email := ""
		if completedEmail != nil {
			kind = access.ActorHuman
			email = *completedEmail
		}
		task.CompletedBy = &work.ActorSnapshot{
			Kind: kind, Subject: *completedSubject, Email: email,
		}
	}
	return task, nil
}

func queryTaskFolderCounts(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	locationIDs []string,
	search string,
	phoneDigits string,
	state work.TaskState,
	command QueryTasksCommand,
) (work.TaskFolderCounts, error) {
	var counts work.TaskFolderCounts
	err := tx.QueryRow(ctx, `
		WITH scoped AS (
			SELECT
				task.category,
				($8::text <> 'follow_up' OR `+work.TaskIsFollowUpSQL+`) AS listed,
				`+work.TaskIsCallRecoverySQL+` AS call_recovery,
				`+work.TaskIsTextReviewSQL+` AS text_review,
				`+taskHasRecentTextAttention+` AS recent_text_attention,
				`+taskIsSpringHillReview+` AS spring_hill_review`+taskMatchSource+`
			WHERE task.practice_id = $1
				AND task.location_id = ANY($2::uuid[])`+taskSearchFilter+`
				AND task.state = $5`+taskResponsibilityFilter("$6", "$7")+`
		)
		SELECT
			count(*) FILTER (WHERE listed),
			(SELECT count(*)`+taskMatchSource+`
				WHERE task.practice_id = $1
					AND task.location_id = ANY($2::uuid[])
					AND task.state = 'OPEN'
					AND `+work.TaskIsCallRecoverySQL+taskSearchFilter+`),
			count(*) FILTER (WHERE listed AND category = 'billing'),
			count(*) FILTER (WHERE listed AND category = 'appointments'),
			count(*) FILTER (WHERE listed AND category = 'documentation'),
			count(*) FILTER (WHERE listed AND category = 'optical'),
			count(*) FILTER (WHERE listed AND category = 'medication'),
			count(*) FILTER (WHERE listed AND category = 'referrals'),
			count(*) FILTER (WHERE listed AND category = 'other'),
			count(*) FILTER (WHERE listed AND category = 'insurance'),
			count(*) FILTER (WHERE listed AND category = 'pre_op'),
			count(*) FILTER (WHERE listed AND category = 'post_op'),
			count(*) FILTER (WHERE text_review AND recent_text_attention),
			count(*) FILTER (WHERE call_recovery),
			count(*) FILTER (WHERE spring_hill_review)
		FROM scoped
	`, practiceID, locationIDs, search, phoneDigits, state, command.Responsibility, strings.ToLower(command.Identity.Email), command.Kind).Scan(
		&counts.Tasks,
		&counts.MissedCalls,
		&counts.Categories.Billing,
		&counts.Categories.Appointments,
		&counts.Categories.Documentation,
		&counts.Categories.Optical,
		&counts.Categories.Medication,
		&counts.Categories.Referrals,
		&counts.Categories.Other,
		&counts.Categories.Insurance, &counts.Categories.PreOp, &counts.Categories.PostOp,
		&counts.Texts, &counts.CallRecovery, &counts.AppointmentReviews,
	)
	if err != nil {
		return work.TaskFolderCounts{}, fmt.Errorf("count Task folders: %w", err)
	}
	return counts, nil
}

type taskCursor struct {
	Kind      string            `json:"kind,omitempty"`
	Present   bool              `json:"-"`
	Ordering  work.TaskOrdering `json:"ordering"`
	State     work.TaskState    `json:"state"`
	Urgency   work.TaskUrgency  `json:"urgency"`
	OrderedAt time.Time         `json:"orderedAt"`
	ID        string            `json:"id"`
}

func encodeTaskCursor(
	task work.Task,
	ordering work.TaskOrdering,
	kind string,
) (string, error) {
	orderedAt := task.CreatedAt
	if task.State == work.TaskCompleted {
		if task.CompletedAt == nil {
			return "", fmt.Errorf("encode Task cursor: completed Task has no completion time")
		}
		orderedAt = *task.CompletedAt
	} else if ordering == work.TaskOrderingRecent {
		orderedAt = task.UpdatedAt
	}
	encoded, err := json.Marshal(taskCursor{
		Ordering:  ordering,
		State:     task.State,
		Kind:      kind,
		Urgency:   task.Urgency,
		OrderedAt: orderedAt,
		ID:        task.ID,
	})
	if err != nil {
		return "", fmt.Errorf("encode Task cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeTaskCursor(
	raw string,
	ordering work.TaskOrdering,
	state work.TaskState,
	kind string,
) (taskCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return taskCursor{}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return taskCursor{}, err
	}
	var cursor taskCursor
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.OrderedAt.IsZero() ||
		uuid.Validate(cursor.ID) != nil || cursor.Ordering != ordering ||
		cursor.State != state || cursor.Kind != kind ||
		(cursor.Urgency != work.TaskUrgencyHighPriority &&
			cursor.Urgency != work.TaskUrgencyNormal &&
			cursor.Urgency != work.TaskUrgencyNonUrgent) {
		return taskCursor{}, ErrInvalidInput
	}
	cursor.Present = true
	return cursor, nil
}

func normalizedDigits(value string) string {
	var digits strings.Builder
	for _, character := range value {
		if unicode.IsDigit(character) {
			digits.WriteRune(character)
		}
	}
	return digits.String()
}

func taskResponsibilityFilter(responsibility string, email string) string {
	return `
 AND (` + responsibility + `::text <> 'mine' OR task.category IS NULL
 OR ` + work.TaskIsCommunicationReviewSQL + `
 OR NOT EXISTS (SELECT 1 FROM work_responsibility_locations configured WHERE configured.practice_id=task.practice_id AND configured.location_id=task.location_id)
 OR EXISTS (SELECT 1 FROM work_responsibilities responsibility
 WHERE responsibility.practice_id=task.practice_id AND responsibility.location_id=task.location_id
 AND responsibility.category=task.category AND responsibility.account_email=` + email + `))`
}
