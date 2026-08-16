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
		(command.State != work.TaskOpen && command.State != work.TaskCompleted) ||
		(command.Folder != "" &&
			command.Folder != work.TaskFolderWork &&
			command.Folder != work.TaskFolderMissedCalls) ||
		(command.Folder != "" && command.State != work.TaskOpen) ||
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
		command.Folder,
	)
	if err != nil {
		return work.TaskPage{}, ErrInvalidInput
	}

	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
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

	rows, err := tx.Query(ctx, taskQuerySQL(command.State, command.Ordering),
		command.PracticeID,
		locationIDs,
		command.Search,
		normalizedDigits(command.Search),
		cursor.Present,
		cursor.OrderedAt,
		cursor.ID,
		urgencyRank(cursor.Urgency),
		limit+1,
		command.Identity.Subject,
		command.Folder,
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
	counts, err := queryTaskFolderCounts(
		ctx,
		tx,
		command.PracticeID,
		locationIDs,
		command.Search,
		normalizedDigits(command.Search),
		command.State,
	)
	if err != nil {
		return work.TaskPage{}, err
	}
	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		nextCursor, err = encodeTaskCursor(
			items[len(items)-1],
			command.Ordering,
			command.Folder,
		)
		if err != nil {
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
	task, err := scanTaskProjection(tx.QueryRow(ctx, taskReadQuery, taskID, identity.Subject))
	if errors.Is(err, pgx.ErrNoRows) {
		return work.Task{}, ErrDenied
	}
	if err != nil {
		return work.Task{}, fmt.Errorf("read Task: %w", err)
	}
	if err := loadTaskInteractions(ctx, tx, &task); err != nil {
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
		task.updated_at`

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

const taskQuerySelect = `
	SELECT` + taskColumns + `,
		COALESCE(conversation.id::text, ''),
		task.state = 'OPEN' AND EXISTS (
			SELECT 1
			FROM messaging_thread_unreads unread
			WHERE unread.thread_id = conversation.id
				AND unread.user_subject = $10
		),
		(
			SELECT count(*)
			FROM work_task_interactions interaction
			WHERE interaction.task_id = task.id
		)
	FROM work_tasks task
	JOIN access_locations location
		ON location.practice_id = task.practice_id
		AND location.id = task.location_id` + taskConversationJoin + `
	WHERE task.practice_id = $1
		AND task.location_id::text = ANY($2::text[])
		AND (
			$3 = ''
				OR strpos(lower(task.title), lower($3)) > 0
				OR strpos(lower(COALESCE(task.caller_name, '')), lower($3)) > 0
				OR strpos(lower(location.name), lower($3)) > 0
				OR strpos(lower(COALESCE(task.category, '')), lower($3)) > 0
				OR ($4 <> '' AND task.phone_digits LIKE '%' || $4 || '%')
		)
		AND (
			$11::text = ''
			OR ($11::text = 'work' AND task.origin NOT IN (
				'MISSED_CALL_RECOVERY',
				'VOICEMAIL_RECOVERY'
			))
			OR ($11::text = 'missed_calls' AND task.origin IN (
				'MISSED_CALL_RECOVERY',
				'VOICEMAIL_RECOVERY'
			))
		)`

func taskQuerySQL(state work.TaskState, ordering work.TaskOrdering) string {
	switch {
	case state == work.TaskOpen && ordering == work.TaskOrderingPriority:
		return taskQuerySelect + `
			AND task.state = 'OPEN'
			AND (
				NOT $5
				OR CASE task.urgency
					WHEN 'high_priority' THEN 0
					WHEN 'normal' THEN 1
					ELSE 2
				END > $8
				OR (
					CASE task.urgency
						WHEN 'high_priority' THEN 0
						WHEN 'normal' THEN 1
						ELSE 2
					END = $8
					AND (task.created_at, task.id::text) > ($6, $7)
				)
			)
		ORDER BY
			CASE task.urgency
				WHEN 'high_priority' THEN 0
				WHEN 'normal' THEN 1
				ELSE 2
			END,
			task.created_at,
			task.id
		LIMIT $9`
	case state == work.TaskOpen && ordering == work.TaskOrderingTime:
		return taskQuerySelect + `
			AND task.state = 'OPEN'
			AND $8::int >= 0
			AND (NOT $5 OR (task.created_at, task.id::text) > ($6, $7))
		ORDER BY task.created_at, task.id
		LIMIT $9`
	case state == work.TaskOpen:
		return taskQuerySelect + `
			AND task.state = 'OPEN'
			AND $8::int >= 0
			AND (NOT $5 OR (task.updated_at, task.id::text) < ($6, $7))
		ORDER BY task.updated_at DESC, task.id DESC
		LIMIT $9`
	case ordering == work.TaskOrderingRecent:
		return taskQuerySelect + `
			AND task.state = 'COMPLETED'
			AND $8::int >= 0
			AND (NOT $5 OR (task.updated_at, task.id::text) < ($6, $7))
		ORDER BY task.updated_at DESC, task.id DESC
		LIMIT $9`
	default:
		return taskQuerySelect + `
			AND task.state = 'COMPLETED'
			AND $8::int >= 0
			AND (NOT $5 OR (task.completed_at, task.id::text) < ($6, $7))
		ORDER BY task.completed_at DESC, task.id DESC
		LIMIT $9`
	}
}

const taskReadQuery = `
	SELECT` + taskColumns + `,
		COALESCE(conversation.id::text, ''),
		task.state = 'OPEN' AND EXISTS (
			SELECT 1
			FROM messaging_thread_unreads unread
			WHERE unread.thread_id = conversation.id
				AND unread.user_subject = $2
		),
		0
	FROM work_tasks task
	JOIN access_locations location
		ON location.practice_id = task.practice_id
		AND location.id = task.location_id` + taskConversationJoin + `
	WHERE task.id = $1`

const conversationTaskQuery = `
	SELECT` + taskColumns + `,
		$3::text,
		task.state = 'OPEN' AND EXISTS (
			SELECT 1
			FROM messaging_thread_unreads unread
			WHERE unread.thread_id = $3::uuid
				AND unread.user_subject = $8
		),
		0
	FROM work_tasks task
	JOIN access_locations location
		ON location.practice_id = task.practice_id
		AND location.id = task.location_id
	WHERE task.practice_id = $1
		AND task.location_id = $2
		AND (
			task.message_thread_id = $3::uuid
			OR (task.message_thread_id IS NULL AND task.phone = $4)
		)
		AND (
			$5::timestamptz IS NULL
			OR task.created_at < $5
			OR (task.created_at = $5 AND task.id::text < $6)
		)
	ORDER BY task.created_at DESC, task.id DESC
	LIMIT $7`

const phoneTaskActivityQuery = `
	SELECT
		activity.id::text,
		activity.kind,
		activity.occurred_at,` + taskColumns + `,
		COALESCE(task.message_thread_id::text, ''),
		false,
		0
	FROM work_tasks task
	JOIN access_locations location
		ON location.practice_id = task.practice_id
		AND location.id = task.location_id
	JOIN work_task_activities activity ON activity.task_id = task.id
	WHERE task.practice_id = $1
		AND task.location_id::text = ANY($2::text[])
		AND task.phone = $3
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
		&task.ConversationThreadID,
		&task.Unread,
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

func loadTaskInteractions(ctx context.Context, tx pgx.Tx, task *work.Task) error {
	rows, err := tx.Query(ctx, `
		SELECT
			interaction.call_id::text,
			interaction.occurred_at,
			CASE WHEN voicemail.outcome = 'VOICEMAIL' THEN 'VOICEMAIL' ELSE 'CALL' END
		FROM work_task_interactions interaction
		LEFT JOIN human_calling_voicemails voicemail
			ON voicemail.call_id = interaction.call_id
		WHERE interaction.task_id = $1
		ORDER BY interaction.occurred_at, interaction.call_id
	`, task.ID)
	if err != nil {
		return fmt.Errorf("query related Task Interactions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var interaction work.TaskInteraction
		if err := rows.Scan(&interaction.CallID, &interaction.OccurredAt, &interaction.Type); err != nil {
			return fmt.Errorf("scan related Task Interaction: %w", err)
		}
		task.Interactions = append(task.Interactions, interaction)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate related Task Interactions: %w", err)
	}
	return nil
}

func queryTaskFolderCounts(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	locationIDs []string,
	search string,
	phoneDigits string,
	state work.TaskState,
) (work.TaskFolderCounts, error) {
	var counts work.TaskFolderCounts
	err := tx.QueryRow(ctx, `
		WITH scoped AS (
			SELECT
				task.origin,
				task.category
			FROM work_tasks task
			JOIN access_locations location
				ON location.practice_id = task.practice_id
				AND location.id = task.location_id
			WHERE task.practice_id = $1
				AND task.location_id::text = ANY($2::text[])
				AND (
					$3 = ''
						OR strpos(lower(task.title), lower($3)) > 0
						OR strpos(lower(COALESCE(task.caller_name, '')), lower($3)) > 0
						OR strpos(lower(location.name), lower($3)) > 0
						OR strpos(lower(COALESCE(task.category, '')), lower($3)) > 0
						OR ($4 <> '' AND task.phone_digits LIKE '%' || $4 || '%')
				)
				AND task.state = $5
		), foldered AS (
			SELECT
				category,
				CASE
					WHEN origin IN ('MISSED_CALL_RECOVERY', 'VOICEMAIL_RECOVERY')
						THEN 'missed_calls'
					ELSE 'tasks'
				END AS folder
			FROM scoped
		)
		SELECT
			count(*) FILTER (WHERE folder = 'tasks'),
			count(*) FILTER (WHERE folder = 'missed_calls'),
			count(*) FILTER (WHERE folder = 'tasks' AND category = 'billing'),
			count(*) FILTER (WHERE folder = 'tasks' AND category = 'appointments'),
			count(*) FILTER (WHERE folder = 'tasks' AND category = 'documentation'),
			count(*) FILTER (WHERE folder = 'tasks' AND category = 'optical'),
			count(*) FILTER (WHERE folder = 'tasks' AND category = 'medication'),
			count(*) FILTER (WHERE folder = 'tasks' AND category = 'referrals'),
			count(*) FILTER (WHERE folder = 'tasks' AND category = 'other')
		FROM foldered
	`, practiceID, locationIDs, search, phoneDigits, state).Scan(
		&counts.Tasks,
		&counts.MissedCalls,
		&counts.Categories.Billing,
		&counts.Categories.Appointments,
		&counts.Categories.Documentation,
		&counts.Categories.Optical,
		&counts.Categories.Medication,
		&counts.Categories.Referrals,
		&counts.Categories.Other,
	)
	if err != nil {
		return work.TaskFolderCounts{}, fmt.Errorf("count Task folders: %w", err)
	}
	return counts, nil
}

type taskCursor struct {
	Present   bool              `json:"-"`
	Ordering  work.TaskOrdering `json:"ordering"`
	State     work.TaskState    `json:"state"`
	Folder    work.TaskFolder   `json:"folder,omitempty"`
	Urgency   work.TaskUrgency  `json:"urgency"`
	OrderedAt time.Time         `json:"orderedAt"`
	ID        string            `json:"id"`
}

func encodeTaskCursor(
	task work.Task,
	ordering work.TaskOrdering,
	folder work.TaskFolder,
) (string, error) {
	orderedAt := task.CreatedAt
	if ordering == work.TaskOrderingRecent {
		orderedAt = task.UpdatedAt
	} else if task.State == work.TaskCompleted {
		if task.CompletedAt == nil {
			return "", fmt.Errorf("encode Task cursor: completed Task has no completion time")
		}
		orderedAt = *task.CompletedAt
	}
	encoded, err := json.Marshal(taskCursor{
		Ordering:  ordering,
		State:     task.State,
		Folder:    folder,
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
	folder work.TaskFolder,
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
		cursor.State != state || cursor.Folder != folder ||
		(cursor.Urgency != work.TaskUrgencyHighPriority &&
			cursor.Urgency != work.TaskUrgencyNormal &&
			cursor.Urgency != work.TaskUrgencyNonUrgent) {
		return taskCursor{}, ErrInvalidInput
	}
	cursor.Present = true
	return cursor, nil
}

func urgencyRank(urgency work.TaskUrgency) int {
	switch urgency {
	case work.TaskUrgencyHighPriority:
		return 0
	case work.TaskUrgencyNormal:
		return 1
	default:
		return 2
	}
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
