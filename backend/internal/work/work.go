package work

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TaskState string

const (
	TaskOpen      TaskState = "OPEN"
	TaskCompleted TaskState = "COMPLETED"
)

var (
	ErrDenied       = errors.New("work access denied")
	ErrInvalidInput = errors.New("invalid work input")
	ErrConflict     = errors.New("work transition conflict")
)

var canonicalPhone = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

type ActorSnapshot struct {
	Subject string
	Email   string
}

type Task struct {
	ID           string
	PracticeID   string
	LocationID   string
	LocationName string
	CallID       string
	Phone        string
	Title        string
	State        TaskState
	CreatedBy    ActorSnapshot
	CreatedAt    time.Time
	CompletedBy  *ActorSnapshot
	CompletedAt  *time.Time
	Version      int64
	UpdatedAt    time.Time
}

type EnsureCallFollowUpCommand struct {
	CallID     string
	PracticeID string
	LocationID string
	Phone      string
	Reason     string
	Creator    access.Actor
}

type RenameTaskCommand struct {
	Identity         access.Identity
	TaskID           string
	ExpectedVersion  int64
	Title            string
	SupportSessionID string
}

type CompleteTaskCommand struct {
	Identity         access.Identity
	TaskID           string
	ExpectedVersion  int64
	SupportSessionID string
}

type ReopenTaskCommand struct {
	Identity         access.Identity
	TaskID           string
	ExpectedVersion  int64
	SupportSessionID string
}

type QueryTasksCommand struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Search     string
	Cursor     string
	Limit      int
}

type TaskPage struct {
	Items      []Task
	NextCursor string
}

// Module owns durable Task state and lifecycle behavior.
type Module struct {
	pool   *pgxpool.Pool
	access *access.Module
	now    func() time.Time
}

func New(
	pool *pgxpool.Pool,
	accessModule *access.Module,
	now func() time.Time,
) *Module {
	if now == nil {
		now = time.Now
	}
	return &Module{pool: pool, access: accessModule, now: now}
}

// EnsureCallFollowUp creates the one Task linked to a Call. The caller owns the
// transaction so Call disposition and Task creation can commit atomically.
func (m *Module) EnsureCallFollowUp(
	ctx context.Context,
	tx pgx.Tx,
	command EnsureCallFollowUpCommand,
) (Task, error) {
	title := strings.TrimSpace(command.Reason)
	if title == "" {
		title = "Follow up on call"
	}
	command.Creator.Email = strings.ToLower(strings.TrimSpace(command.Creator.Email))
	if tx == nil ||
		m.access == nil ||
		strings.TrimSpace(command.CallID) == "" ||
		strings.TrimSpace(command.PracticeID) == "" ||
		strings.TrimSpace(command.LocationID) == "" ||
		!canonicalPhone.MatchString(command.Phone) ||
		len(title) > 500 ||
		strings.TrimSpace(command.Creator.Subject) == "" ||
		command.Creator.Email == "" {
		return Task{}, ErrInvalidInput
	}

	task, inserted, err := insertTask(ctx, tx, command, title, m.now())
	if err != nil {
		return Task{}, err
	}
	if !inserted {
		if task.PracticeID != command.PracticeID ||
			task.LocationID != command.LocationID ||
			task.Phone != command.Phone {
			return Task{}, ErrConflict
		}
		return task, nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO work_task_activities (
			task_id,
			task_version,
			kind,
			actor_subject,
			actor_email,
			occurred_at
		)
		VALUES ($1, 1, 'TASK_CREATED', $2, $3, $4)
	`, task.ID, task.CreatedBy.Subject, task.CreatedBy.Email, task.CreatedAt); err != nil {
		return Task{}, fmt.Errorf("append Task creation Activity: %w", err)
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, task.PracticeID); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (m *Module) RenameTask(
	ctx context.Context,
	command RenameTaskCommand,
) (Task, error) {
	title := strings.TrimSpace(command.Title)
	if m.access == nil ||
		strings.TrimSpace(command.TaskID) == "" ||
		command.ExpectedVersion <= 0 ||
		title == "" ||
		len(title) > 500 {
		return Task{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Task{}, fmt.Errorf("begin Task rename: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := loadTask(ctx, tx, command.TaskID)
	if err != nil {
		return Task{}, err
	}
	actor, err := m.authorizeMutation(
		ctx,
		tx,
		command.Identity,
		task,
		command.SupportSessionID,
	)
	if err != nil {
		return Task{}, err
	}
	task, err = lockTask(ctx, tx, command.TaskID)
	if err != nil {
		return Task{}, err
	}
	if task.State != TaskOpen || task.Version != command.ExpectedVersion {
		return task, ErrConflict
	}
	changedAt := m.now()
	if err := tx.QueryRow(ctx, `
		UPDATE work_tasks
		SET
			title = $2,
			version = version + 1,
			updated_at = $3
		WHERE id = $1
		RETURNING version
	`, task.ID, title, changedAt).Scan(&task.Version); err != nil {
		return Task{}, fmt.Errorf("rename Task: %w", err)
	}
	task.Title = title
	task.UpdatedAt = changedAt
	if err := appendActivity(
		ctx,
		tx,
		task,
		"TITLE_CHANGED",
		actor,
		changedAt,
	); err != nil {
		return Task{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, task.PracticeID); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, fmt.Errorf("commit Task rename: %w", err)
	}
	return task, nil
}

func (m *Module) CompleteTask(
	ctx context.Context,
	command CompleteTaskCommand,
) (Task, error) {
	if m.access == nil ||
		strings.TrimSpace(command.TaskID) == "" ||
		command.ExpectedVersion <= 0 {
		return Task{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Task{}, fmt.Errorf("begin Task completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := loadTask(ctx, tx, command.TaskID)
	if err != nil {
		return Task{}, err
	}
	actor, err := m.authorizeMutation(
		ctx,
		tx,
		command.Identity,
		task,
		command.SupportSessionID,
	)
	if err != nil {
		return Task{}, err
	}
	task, err = lockTask(ctx, tx, command.TaskID)
	if err != nil {
		return Task{}, err
	}
	if task.State == TaskCompleted {
		return task, nil
	}
	if task.Version != command.ExpectedVersion {
		return task, ErrConflict
	}
	completedAt := m.now()
	if err := tx.QueryRow(ctx, `
		UPDATE work_tasks
		SET
			state = 'COMPLETED',
			completed_by_subject = $2,
			completed_by_email = $3,
			completed_at = $4,
			version = version + 1,
			updated_at = $4
		WHERE id = $1
		RETURNING version
	`, task.ID, actor.Subject, actor.Email, completedAt).Scan(&task.Version); err != nil {
		return Task{}, fmt.Errorf("complete Task: %w", err)
	}
	task.State = TaskCompleted
	task.CompletedBy = &ActorSnapshot{Subject: actor.Subject, Email: actor.Email}
	task.CompletedAt = &completedAt
	task.UpdatedAt = completedAt
	if err := appendActivity(
		ctx,
		tx,
		task,
		"TASK_COMPLETED",
		actor,
		completedAt,
	); err != nil {
		return Task{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, task.PracticeID); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, fmt.Errorf("commit Task completion: %w", err)
	}
	return task, nil
}

func (m *Module) ReopenTask(
	ctx context.Context,
	command ReopenTaskCommand,
) (Task, error) {
	if m.access == nil ||
		strings.TrimSpace(command.TaskID) == "" ||
		command.ExpectedVersion <= 0 {
		return Task{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Task{}, fmt.Errorf("begin Task reopen: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := loadTask(ctx, tx, command.TaskID)
	if err != nil {
		return Task{}, err
	}
	actor, err := m.authorizeMutation(
		ctx,
		tx,
		command.Identity,
		task,
		command.SupportSessionID,
	)
	if err != nil {
		return Task{}, err
	}
	task, err = lockTask(ctx, tx, command.TaskID)
	if err != nil {
		return Task{}, err
	}
	if task.State == TaskOpen {
		return task, nil
	}
	if task.Version != command.ExpectedVersion {
		return task, ErrConflict
	}
	reopenedAt := m.now()
	if err := tx.QueryRow(ctx, `
		UPDATE work_tasks
		SET
			state = 'OPEN',
			completed_by_subject = NULL,
			completed_by_email = NULL,
			completed_at = NULL,
			version = version + 1,
			updated_at = $2
		WHERE id = $1
		RETURNING version
	`, task.ID, reopenedAt).Scan(&task.Version); err != nil {
		return Task{}, fmt.Errorf("reopen Task: %w", err)
	}
	task.State = TaskOpen
	task.CompletedBy = nil
	task.CompletedAt = nil
	task.UpdatedAt = reopenedAt
	if err := appendActivity(
		ctx,
		tx,
		task,
		"TASK_REOPENED",
		actor,
		reopenedAt,
	); err != nil {
		return Task{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, task.PracticeID); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, fmt.Errorf("commit Task reopen: %w", err)
	}
	return task, nil
}

func (m *Module) QueryTasks(
	ctx context.Context,
	command QueryTasksCommand,
) (TaskPage, error) {
	command.Search = strings.TrimSpace(command.Search)
	if m.access == nil ||
		strings.TrimSpace(command.PracticeID) == "" ||
		len(command.Search) > 500 {
		return TaskPage{}, ErrInvalidInput
	}
	limit := command.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 50 {
		return TaskPage{}, ErrInvalidInput
	}
	cursor, err := decodeTaskCursor(command.Cursor)
	if err != nil {
		return TaskPage{}, ErrInvalidInput
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TaskPage{}, fmt.Errorf("begin Task query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		command.Identity,
		command.PracticeID,
		command.LocationID,
	)
	if err != nil {
		return TaskPage{}, ErrDenied
	}
	locationIDs := make([]string, 0, len(authorization.Locations))
	if command.LocationID != "" {
		locationIDs = append(locationIDs, command.LocationID)
	} else {
		for _, location := range authorization.Locations {
			locationIDs = append(locationIDs, location.ID)
		}
	}
	if len(locationIDs) == 0 {
		return TaskPage{}, ErrDenied
	}

	rows, err := tx.Query(ctx, `
		SELECT
			task.id::text,
			task.practice_id::text,
			task.location_id::text,
			location.name,
			task.call_id::text,
			task.phone,
			task.title,
			task.state,
			task.created_by_subject,
			task.created_by_email,
			task.created_at,
			task.completed_by_subject,
			task.completed_by_email,
			task.completed_at,
			task.version,
			task.updated_at
		FROM work_tasks task
		JOIN access_locations location
			ON location.practice_id = task.practice_id
			AND location.id = task.location_id
		WHERE task.practice_id = $1
			AND task.location_id::text = ANY($2::text[])
			AND (
				$3 = ''
				OR strpos(lower(task.title), lower($3)) > 0
				OR ($4 <> '' AND task.phone_digits LIKE '%' || $4 || '%')
			)
			AND (
				NOT $5
				OR (
					$6 = 'OPEN'
					AND (
						(
							task.state = 'OPEN'
							AND (
								task.created_at > $7
								OR (task.created_at = $7 AND task.id::text > $8)
							)
						)
						OR task.state = 'COMPLETED'
					)
				)
				OR (
					$6 = 'COMPLETED'
					AND task.state = 'COMPLETED'
					AND (
						task.completed_at < $7
						OR (task.completed_at = $7 AND task.id::text > $8)
					)
				)
			)
		ORDER BY
			CASE task.state WHEN 'OPEN' THEN 0 ELSE 1 END,
			CASE WHEN task.state = 'OPEN' THEN task.created_at END,
			CASE WHEN task.state = 'COMPLETED' THEN task.completed_at END DESC,
			task.id
		LIMIT $9
	`, command.PracticeID, locationIDs, command.Search,
		normalizedDigits(command.Search), cursor.Present, cursor.State,
		cursor.OrderedAt, cursor.ID, limit+1,
	)
	if err != nil {
		return TaskPage{}, fmt.Errorf("query Tasks: %w", err)
	}
	defer rows.Close()
	items := make([]Task, 0, limit+1)
	for rows.Next() {
		var task Task
		var completedSubject, completedEmail *string
		if err := rows.Scan(
			&task.ID,
			&task.PracticeID,
			&task.LocationID,
			&task.LocationName,
			&task.CallID,
			&task.Phone,
			&task.Title,
			&task.State,
			&task.CreatedBy.Subject,
			&task.CreatedBy.Email,
			&task.CreatedAt,
			&completedSubject,
			&completedEmail,
			&task.CompletedAt,
			&task.Version,
			&task.UpdatedAt,
		); err != nil {
			return TaskPage{}, fmt.Errorf("scan Task query: %w", err)
		}
		setCompletionActor(&task, completedSubject, completedEmail)
		items = append(items, task)
	}
	if err := rows.Err(); err != nil {
		return TaskPage{}, fmt.Errorf("iterate Tasks: %w", err)
	}
	rows.Close()

	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		nextCursor, err = encodeTaskCursor(items[len(items)-1])
		if err != nil {
			return TaskPage{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return TaskPage{}, fmt.Errorf("commit Task query: %w", err)
	}
	return TaskPage{Items: items, NextCursor: nextCursor}, nil
}

func (m *Module) ReadTask(
	ctx context.Context,
	identity access.Identity,
	taskID string,
) (Task, error) {
	if m.access == nil || strings.TrimSpace(taskID) == "" {
		return Task{}, ErrDenied
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Task{}, fmt.Errorf("begin Task read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := loadTask(ctx, tx, taskID)
	if err != nil {
		return Task{}, err
	}
	if _, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		identity,
		task.PracticeID,
		task.LocationID,
	); err != nil {
		return Task{}, ErrDenied
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, fmt.Errorf("commit Task read: %w", err)
	}
	return task, nil
}

type taskQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type taskCursor struct {
	Present   bool      `json:"-"`
	State     TaskState `json:"state"`
	OrderedAt time.Time `json:"orderedAt"`
	ID        string    `json:"id"`
}

func encodeTaskCursor(task Task) (string, error) {
	orderedAt := task.CreatedAt
	if task.State == TaskCompleted {
		if task.CompletedAt == nil {
			return "", fmt.Errorf("encode Task cursor: completed Task has no completion time")
		}
		orderedAt = *task.CompletedAt
	}
	encoded, err := json.Marshal(taskCursor{
		State:     task.State,
		OrderedAt: orderedAt,
		ID:        task.ID,
	})
	if err != nil {
		return "", fmt.Errorf("encode Task cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeTaskCursor(encoded string) (taskCursor, error) {
	if encoded == "" {
		return taskCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return taskCursor{}, err
	}
	var cursor taskCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return taskCursor{}, err
	}
	if (cursor.State != TaskOpen && cursor.State != TaskCompleted) ||
		cursor.OrderedAt.IsZero() ||
		strings.TrimSpace(cursor.ID) == "" {
		return taskCursor{}, ErrInvalidInput
	}
	cursor.Present = true
	return cursor, nil
}

func normalizedDigits(value string) string {
	var digits strings.Builder
	for _, character := range value {
		if character >= '0' && character <= '9' {
			digits.WriteRune(character)
		}
	}
	return digits.String()
}

func (m *Module) authorizeMutation(
	ctx context.Context,
	tx pgx.Tx,
	identity access.Identity,
	task Task,
	supportSessionID string,
) (access.Actor, error) {
	authorization, err := m.access.LockMutationAuthorization(
		ctx,
		tx,
		identity,
		task.PracticeID,
		task.LocationID,
		supportSessionID,
	)
	if err != nil {
		if errors.Is(err, access.ErrSupportRequired) ||
			errors.Is(err, access.ErrSupportExpired) ||
			errors.Is(err, access.ErrSupportRevoked) ||
			errors.Is(err, access.ErrSupportPracticeMismatch) {
			return access.Actor{}, err
		}
		return access.Actor{}, ErrDenied
	}
	return authorization.Actor, nil
}

func appendActivity(
	ctx context.Context,
	tx pgx.Tx,
	task Task,
	kind string,
	actor access.Actor,
	occurredAt time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO work_task_activities (
			task_id,
			task_version,
			kind,
			actor_subject,
			actor_email,
			occurred_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, task.ID, task.Version, kind, actor.Subject,
		strings.ToLower(strings.TrimSpace(actor.Email)), occurredAt,
	); err != nil {
		return fmt.Errorf("append Task Activity: %w", err)
	}
	return nil
}

func insertTask(
	ctx context.Context,
	tx pgx.Tx,
	command EnsureCallFollowUpCommand,
	title string,
	now time.Time,
) (Task, bool, error) {
	var task Task
	var completedSubject, completedEmail *string
	var inserted bool
	err := tx.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO work_tasks (
				practice_id,
				location_id,
				call_id,
				phone,
				title,
				state,
				created_by_subject,
				created_by_email,
				created_at,
				updated_at
			)
			VALUES ($1, $2, $3, $4, $5, 'OPEN', $6, $7, $8, $8)
			ON CONFLICT (call_id) DO NOTHING
			RETURNING *
		)
		SELECT
			task.id::text,
			task.practice_id::text,
			task.location_id::text,
			location.name,
			task.call_id::text,
			task.phone,
			task.title,
			task.state,
			task.created_by_subject,
			task.created_by_email,
			task.created_at,
			task.completed_by_subject,
			task.completed_by_email,
			task.completed_at,
			task.version,
			task.updated_at,
			EXISTS (SELECT 1 FROM inserted)
		FROM (
			SELECT * FROM inserted
			UNION ALL
			SELECT existing.*
			FROM work_tasks existing
			WHERE existing.call_id = $3
				AND NOT EXISTS (SELECT 1 FROM inserted)
		) task
		JOIN access_locations location
			ON location.practice_id = task.practice_id
			AND location.id = task.location_id
	`, command.PracticeID, command.LocationID, command.CallID, command.Phone,
		title, command.Creator.Subject, command.Creator.Email, now,
	).Scan(
		&task.ID,
		&task.PracticeID,
		&task.LocationID,
		&task.LocationName,
		&task.CallID,
		&task.Phone,
		&task.Title,
		&task.State,
		&task.CreatedBy.Subject,
		&task.CreatedBy.Email,
		&task.CreatedAt,
		&completedSubject,
		&completedEmail,
		&task.CompletedAt,
		&task.Version,
		&task.UpdatedAt,
		&inserted,
	)
	if err != nil {
		return Task{}, false, fmt.Errorf("ensure Call follow-up Task: %w", err)
	}
	setCompletionActor(&task, completedSubject, completedEmail)
	return task, inserted, nil
}

func loadTask(
	ctx context.Context,
	querier taskQuerier,
	taskID string,
) (Task, error) {
	var task Task
	var completedSubject, completedEmail *string
	err := querier.QueryRow(ctx, `
		SELECT
			task.id::text,
			task.practice_id::text,
			task.location_id::text,
			location.name,
			task.call_id::text,
			task.phone,
			task.title,
			task.state,
			task.created_by_subject,
			task.created_by_email,
			task.created_at,
			task.completed_by_subject,
			task.completed_by_email,
			task.completed_at,
			task.version,
			task.updated_at
		FROM work_tasks task
		JOIN access_locations location
			ON location.practice_id = task.practice_id
			AND location.id = task.location_id
		WHERE task.id = $1
	`, taskID).Scan(
		&task.ID,
		&task.PracticeID,
		&task.LocationID,
		&task.LocationName,
		&task.CallID,
		&task.Phone,
		&task.Title,
		&task.State,
		&task.CreatedBy.Subject,
		&task.CreatedBy.Email,
		&task.CreatedAt,
		&completedSubject,
		&completedEmail,
		&task.CompletedAt,
		&task.Version,
		&task.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrDenied
	}
	if err != nil {
		return Task{}, fmt.Errorf("read Task: %w", err)
	}
	setCompletionActor(&task, completedSubject, completedEmail)
	return task, nil
}

func lockTask(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
) (Task, error) {
	var task Task
	var completedSubject, completedEmail *string
	err := tx.QueryRow(ctx, `
		SELECT
			task.id::text,
			task.practice_id::text,
			task.location_id::text,
			location.name,
			task.call_id::text,
			task.phone,
			task.title,
			task.state,
			task.created_by_subject,
			task.created_by_email,
			task.created_at,
			task.completed_by_subject,
			task.completed_by_email,
			task.completed_at,
			task.version,
			task.updated_at
		FROM work_tasks task
		JOIN access_locations location
			ON location.practice_id = task.practice_id
			AND location.id = task.location_id
		WHERE task.id = $1
		FOR UPDATE OF task
	`, taskID).Scan(
		&task.ID,
		&task.PracticeID,
		&task.LocationID,
		&task.LocationName,
		&task.CallID,
		&task.Phone,
		&task.Title,
		&task.State,
		&task.CreatedBy.Subject,
		&task.CreatedBy.Email,
		&task.CreatedAt,
		&completedSubject,
		&completedEmail,
		&task.CompletedAt,
		&task.Version,
		&task.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrDenied
	}
	if err != nil {
		return Task{}, fmt.Errorf("lock Task: %w", err)
	}
	setCompletionActor(&task, completedSubject, completedEmail)
	return task, nil
}

func setCompletionActor(
	task *Task,
	subject *string,
	email *string,
) {
	if subject == nil || email == nil {
		return
	}
	task.CompletedBy = &ActorSnapshot{Subject: *subject, Email: *email}
}
