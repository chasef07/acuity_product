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

	search, err := resolveTaskSearch(ctx, tx, command.PracticeID, locationIDs, command.Search)
	if err != nil {
		return work.TaskPage{}, err
	}
	rows, err := tx.Query(ctx, taskQuerySQL(command.State, command.Ordering, command.Grouped, search), search.arguments(
		command.PracticeID,
		locationIDs,
		search.pattern,
		search.digits,
		cursor.Present,
		cursor.OrderedAt,
		cursor.ID,
		cursor.Urgency.Rank(),
		limit+1,
		command.Responsibility, strings.ToLower(command.Identity.Email), command.Category, command.Kind,
	)...)
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
		value, err := queryTaskFolderCounts(ctx, tx, command, locationIDs, search)
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

const activityCallerEmail = `COALESCE(
					(
						SELECT caller_membership.email
						FROM access_memberships caller_membership
						WHERE caller_membership.practice_id = task.practice_id
							AND caller_membership.user_subject = activity.details ->> 'callerSubject'
					),
					(
						SELECT caller_operator.email
						FROM access_platform_operators caller_operator
						WHERE caller_operator.user_subject = activity.details ->> 'callerSubject'
					)
				)`

const taskColumns = work.TaskColumns + `,
        COALESCE(CASE WHEN ` + work.TaskIsTextReviewSQL + ` THEN (
          SELECT COALESCE(NULLIF(message.body,''),'Attachment') FROM messaging_messages message
          WHERE message.thread_id=task.message_thread_id AND message.direction='INBOUND'
          ORDER BY message.created_at DESC,message.id DESC LIMIT 1
        ) END,''),
		acknowledgement.state,
		acknowledgement.safe_failure_code,
		acknowledgement.message_id::text,
		acknowledgement.updated_at,
		CASE WHEN task.state = 'COMPLETED' AND task.completed_by_subject = 'work-recovery-resolution' THEN (
			SELECT COALESCE(` + activityCallerEmail + `, '')
			FROM work_task_activities activity
			WHERE activity.task_id = task.id
				AND activity.kind = 'TASK_AUTO_COMPLETED_CALLBACK_ATTEMPT'
				AND activity.occurred_at = task.completed_at
			LIMIT 1
		) END`

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

type taskSearch struct {
	pattern     string
	digits      string
	locationIDs []string
}

var taskSearchEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func resolveTaskSearch(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	locationIDs []string,
	text string,
) (taskSearch, error) {
	if text == "" {
		return taskSearch{}, nil
	}
	search := taskSearch{pattern: "%" + taskSearchEscaper.Replace(text) + "%"}
	if digits := normalizedDigits(text); digits != "" {
		search.digits = "%" + digits + "%"
	}
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(array_agg(location.id::text), '{}')
		FROM access_locations location
		WHERE location.practice_id = $1
			AND location.id = ANY($2::uuid[])
			AND lower(location.name) LIKE lower($3)
	`, practiceID, locationIDs, search.pattern).Scan(&search.locationIDs); err != nil {
		return taskSearch{}, fmt.Errorf("search Task Locations: %w", err)
	}
	return search, nil
}

func (search taskSearch) arguments(arguments ...any) []any {
	if search.pattern == "" {
		return arguments
	}
	arguments = append([]any{pgx.QueryExecModeCacheDescribe}, arguments...)
	if len(search.locationIDs) == 0 {
		return arguments
	}
	return append(arguments, search.locationIDs)
}

func (search taskSearch) filter(locations string) string {
	if search.pattern == "" {
		return `
		AND $3::text = '' AND $4::text = ''`
	}
	text := `
			lower(task.title) LIKE lower($3)
			OR lower(task.caller_name) LIKE lower($3)
			OR lower(task.category) LIKE lower($3)`
	if len(search.locationIDs) > 0 {
		text += `
			OR task.location_id = ANY(` + locations + `::uuid[])`
	}
	if search.digits == "" {
		return `
		AND $4::text = ''
		AND (` + text + `
		)`
	}
	return `
		AND (` + text + `
			OR task.phone_digits LIKE $4
		)`
}

func taskQueryFilter(search taskSearch) string {
	return `
	WHERE task.practice_id = $1
		AND task.location_id = ANY($2::uuid[])` + taskResponsibilityFilter("$10", "$11") + `
 AND ($12::text = '' OR task.category=$12)
 AND ($13::text = '' OR ($13='texts' AND ` + work.TaskIsTextReviewSQL + `) OR ($13='calls' AND ` + work.TaskIsCallRecoverySQL + `)
 OR ($13='appointments' AND ` + taskIsSpringHillReview + `)
 OR ($13='follow_up' AND ` + work.TaskIsFollowUpSQL + `))` + search.filter("$14")
}

func taskQuerySQL(state work.TaskState, ordering work.TaskOrdering, grouped bool, search taskSearch) string {
	window, order := taskPageWindow(state, ordering)
	filter := taskQueryFilter(search)
	candidates := taskMatchSource + filter
	if grouped && state == work.TaskOpen {
		candidates = `
	FROM (
		SELECT task.*, row_number() OVER (
			PARTITION BY task.practice_id, task.location_id, task.phone, task.category, task.origin
			ORDER BY ` + order + `
		) AS member_rank` + taskMatchSource + filter + `
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
		activity.occurred_at,
		CASE WHEN activity.kind = 'TASK_AUTO_COMPLETED_CALLBACK_ATTEMPT'
			THEN activity.details || jsonb_strip_nulls(jsonb_build_object(
				'callerEmail', ` + activityCallerEmail + `
			))
			ELSE activity.details
		END,
		activity.actor_kind,
		activity.actor_subject,
		COALESCE(activity.actor_email, ''),` + taskColumns + `,
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

func scanTaskProjection(row pgx.Row, prefix ...any) (work.Task, error) {
	var preview, conversationThreadID string
	var relatedInteractionCount int
	var acknowledgementState, acknowledgementFailure, acknowledgementMessageID *string
	var acknowledgementUpdatedAt *time.Time
	var callbackCallerEmail *string
	task, err := work.ScanTask(row, prefix,
		&preview,
		&acknowledgementState,
		&acknowledgementFailure,
		&acknowledgementMessageID,
		&acknowledgementUpdatedAt,
		&callbackCallerEmail,
		&conversationThreadID,
		&relatedInteractionCount,
	)
	if err != nil {
		return work.Task{}, err
	}
	task.Preview = preview
	task.ConversationThreadID = conversationThreadID
	task.RelatedInteractionCount = relatedInteractionCount
	if callbackCallerEmail != nil {
		task.CallbackCompletion = &work.TaskCallbackCompletion{CallerEmail: *callbackCallerEmail}
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
	return task, nil
}

func queryTaskFolderCounts(
	ctx context.Context,
	tx pgx.Tx,
	command QueryTasksCommand,
	locationIDs []string,
	search taskSearch,
) (work.TaskFolderCounts, error) {
	var counts work.TaskFolderCounts
	categoryCounts := make([]int, len(work.TaskCategories))
	destinations := []any{&counts.Tasks}
	for i := range categoryCounts {
		destinations = append(destinations, &categoryCounts[i])
	}
	destinations = append(destinations, &counts.Texts, &counts.CallRecovery, &counts.AppointmentReviews)
	err := tx.QueryRow(ctx, `
		WITH scoped AS (
			SELECT
				task.category,
				($8::text <> 'follow_up' OR `+work.TaskIsFollowUpSQL+`) AS listed,
				`+work.TaskIsCallRecoverySQL+` AS call_recovery,
				`+work.TaskIsTextReviewSQL+` AS text_review,
				`+taskIsSpringHillReview+` AS spring_hill_review`+taskMatchSource+`
			WHERE task.practice_id = $1
				AND task.location_id = ANY($2::uuid[])`+search.filter("$9")+`
				AND task.state = $5`+taskResponsibilityFilter("$6", "$7")+`
		)
		SELECT
			count(*) FILTER (WHERE listed),`+taskCategoryCountColumns+`
			count(*) FILTER (WHERE text_review),
			count(*) FILTER (WHERE call_recovery),
			count(*) FILTER (WHERE spring_hill_review)
		FROM scoped
	`, search.arguments(command.PracticeID, locationIDs, search.pattern, search.digits, command.State, command.Responsibility, strings.ToLower(command.Identity.Email), command.Kind)...).Scan(destinations...)
	if err != nil {
		return work.TaskFolderCounts{}, fmt.Errorf("count Task folders: %w", err)
	}
	counts.Categories = make(work.TaskCategoryCounts, len(work.TaskCategories))
	for i, category := range work.TaskCategories {
		counts.Categories[category] = categoryCounts[i]
	}
	return counts, nil
}

var taskCategoryCountColumns = func() string {
	var columns strings.Builder
	for _, category := range work.TaskCategories {
		columns.WriteString("\n\t\t\tcount(*) FILTER (WHERE listed AND category = '" + string(category) + "'),")
	}
	return columns.String()
}()

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
 OR NOT EXISTS (SELECT 1 FROM work_responsibilities owned
 WHERE owned.practice_id=task.practice_id AND owned.location_id=task.location_id
 AND owned.category=task.category)
 OR EXISTS (SELECT 1 FROM work_responsibilities responsibility
 WHERE responsibility.practice_id=task.practice_id AND responsibility.location_id=task.location_id
 AND responsibility.category=task.category AND responsibility.account_email=` + email + `))`
}
