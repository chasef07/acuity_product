package work

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TaskState string

const (
	TaskOpen      TaskState = "OPEN"
	TaskCompleted TaskState = "COMPLETED"
)

type TaskOrigin string

const (
	TaskOriginHumanCallFollowUp    TaskOrigin = "HUMAN_CALL_FOLLOW_UP"
	TaskOriginAbitaAI              TaskOrigin = "ABITA_AI"
	TaskOriginStaffMessageFollowUp TaskOrigin = "STAFF_MESSAGE_FOLLOW_UP"
	TaskOriginVoicemail            TaskOrigin = "VOICEMAIL_RECOVERY"
	TaskOriginMissedCall           TaskOrigin = "MISSED_CALL_RECOVERY"
)

type RecoveryOutcome string

const (
	RecoveryOutcomeVoicemail  RecoveryOutcome = "VOICEMAIL"
	RecoveryOutcomeMissedCall RecoveryOutcome = "MISSED_CALL"
)

type TaskUrgency string

const (
	TaskUrgencyHighPriority TaskUrgency = "high_priority"
	TaskUrgencyNormal       TaskUrgency = "normal"
	TaskUrgencyNonUrgent    TaskUrgency = "non_urgent"
)

type TaskCategory string

const (
	TaskCategoryBilling       TaskCategory = "billing"
	TaskCategoryAppointments  TaskCategory = "appointments"
	TaskCategoryDocumentation TaskCategory = "documentation"
	TaskCategoryOptical       TaskCategory = "optical"
	TaskCategoryMedication    TaskCategory = "medication"
	TaskCategoryReferrals     TaskCategory = "referrals"
	TaskCategoryOther         TaskCategory = "other"
)

type TaskCreateStatus string

const (
	TaskCreated   TaskCreateStatus = "created"
	TaskDuplicate TaskCreateStatus = "duplicate"
)

type TaskOrdering string

const (
	TaskOrderingTime     TaskOrdering = "time"
	TaskOrderingPriority TaskOrdering = "priority"
	TaskOrderingRecent   TaskOrdering = "recent"
)

var (
	ErrDenied       = errors.New("work access denied")
	ErrInvalidInput = errors.New("invalid work input")
	ErrConflict     = errors.New("work transition conflict")
)

var (
	canonicalPhone = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
	officeKey      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)
	idempotencyKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)
)

type ActorSnapshot struct {
	Kind    access.ActorKind
	Subject string
	Email   string
}

type Task struct {
	ID                      string
	PracticeID              string
	LocationID              string
	LocationName            string
	CallID                  string
	Phone                   string
	Title                   string
	State                   TaskState
	Origin                  TaskOrigin
	Urgency                 TaskUrgency
	Category                TaskCategory
	CallerName              string
	SourceCallID            string
	SourceMessage           string
	MessageID               string
	MessageThreadID         string
	ConversationThreadID    string
	RecoveryOutcome         RecoveryOutcome
	RelatedInteractionCount int
	Interactions            []TaskInteraction
	Unread                  bool
	CreatedBy               ActorSnapshot
	CreatedAt               time.Time
	CompletedBy             *ActorSnapshot
	CompletedAt             *time.Time
	Version                 int64
	UpdatedAt               time.Time
}

// StaffNote is durable staff-authored evidence attached to an exact phone.
// It belongs to Work because it is authored operational work context rather
// than a Message or Call.
type StaffNote struct {
	ID           string
	PracticeID   string
	LocationID   string
	LocationName string
	Phone        string
	Body         string
	CreatedBy    ActorSnapshot
	CreatedAt    time.Time
}

type CreateStaffNoteCommand struct {
	Identity         access.Identity
	PracticeID       string
	LocationID       string
	Phone            string
	Body             string
	SupportSessionID string
}

// TaskInteraction is the authorized communication evidence attached to a
// Task. It remains a Call owned by HumanCalling rather than a copied Work
// aggregate.
type TaskInteraction struct {
	CallID     string
	OccurredAt time.Time
	Type       string
}

type CreateAITaskCommand struct {
	Service                 access.ServiceIdentity
	OfficeKey               string
	OfficePhone             string
	InboundOfficePhone      string
	SourceCallID            string
	IdempotencyKey          string
	Phone                   string
	CallerName              string
	CompatibilityPatientID  string
	CompatibilityPatientDOB string
	Summary                 string
	Message                 string
	Category                TaskCategory
	Urgency                 TaskUrgency
}

type EnsureCallFollowUpCommand struct {
	CallID     string
	PracticeID string
	LocationID string
	Phone      string
	Reason     string
	Creator    access.Actor
}

type EnsureMessageFollowUpCommand struct {
	MessageID  string
	ThreadID   string
	PracticeID string
	LocationID string
	Phone      string
	Title      string
	Creator    access.Actor
}

type EnsureRecoveryTaskCommand struct {
	CallID     string
	PracticeID string
	LocationID string
	Phone      string
	CallerName string
	Outcome    RecoveryOutcome
	OccurredAt time.Time
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
	State      TaskState
	Ordering   TaskOrdering
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

// CreateStaffNote records immutable staff-authored context in the authorized
// Location and advances the workspace version in the same transaction.
func (m *Module) CreateStaffNote(
	ctx context.Context,
	command CreateStaffNoteCommand,
) (StaffNote, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.LocationID = strings.TrimSpace(command.LocationID)
	command.Phone = strings.TrimSpace(command.Phone)
	command.Body = strings.TrimSpace(command.Body)
	if m.pool == nil || m.access == nil ||
		command.PracticeID == "" || command.LocationID == "" ||
		!canonicalPhone.MatchString(command.Phone) ||
		!textLengthBetween(command.Body, 1, 2500) {
		return StaffNote{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return StaffNote{}, fmt.Errorf("begin Staff Note creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockMutationAuthorization(
		ctx, tx, command.Identity, command.PracticeID, command.LocationID,
		command.SupportSessionID,
	)
	if err != nil {
		if errors.Is(err, access.ErrSupportRequired) ||
			errors.Is(err, access.ErrSupportExpired) ||
			errors.Is(err, access.ErrSupportRevoked) ||
			errors.Is(err, access.ErrSupportPracticeMismatch) {
			return StaffNote{}, err
		}
		return StaffNote{}, ErrDenied
	}
	createdAt := m.now()
	note := StaffNote{
		PracticeID: command.PracticeID,
		LocationID: command.LocationID,
		Phone:      command.Phone,
		Body:       command.Body,
		CreatedBy: ActorSnapshot{
			Kind:    access.ActorHuman,
			Subject: authorization.Actor.Subject,
			Email:   strings.ToLower(strings.TrimSpace(authorization.Actor.Email)),
		},
		CreatedAt: createdAt,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO work_staff_notes (
			practice_id, location_id, phone, body,
			created_by_subject, created_by_email, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id::text
	`, note.PracticeID, note.LocationID, note.Phone, note.Body,
		note.CreatedBy.Subject, note.CreatedBy.Email, note.CreatedAt,
	).Scan(&note.ID)
	if err != nil {
		return StaffNote{}, fmt.Errorf("create Staff Note: %w", err)
	}
	for _, location := range authorization.Locations {
		if location.ID == note.LocationID {
			note.LocationName = location.Name
			break
		}
	}
	if err := m.access.AuditSupportedMutation(
		ctx,
		tx,
		authorization,
		access.SupportedMutationAudit{
			Action:          "staff_note.created",
			ResourceType:    "staff_note",
			ResourceID:      note.ID,
			ResourceVersion: 1,
			OccurredAt:      createdAt,
		},
	); err != nil {
		return StaffNote{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, note.PracticeID); err != nil {
		return StaffNote{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StaffNote{}, fmt.Errorf("commit Staff Note creation: %w", err)
	}
	return note, nil
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
			actor_kind,
			actor_subject,
			actor_email,
			occurred_at
		)
		VALUES ($1, 1, 'TASK_CREATED', 'HUMAN', $2, $3, $4)
	`, task.ID, task.CreatedBy.Subject, task.CreatedBy.Email, task.CreatedAt); err != nil {
		return Task{}, fmt.Errorf("append Task creation Activity: %w", err)
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, task.PracticeID); err != nil {
		return Task{}, err
	}
	return task, nil
}

// EnsureMessageFollowUp creates at most one Task from one source Message. The
// caller owns the transaction so Messaging authorization, Work creation, and a
// required Support Mode audit can commit or roll back together.
func (m *Module) EnsureMessageFollowUp(
	ctx context.Context,
	tx pgx.Tx,
	command EnsureMessageFollowUpCommand,
) (Task, TaskCreateStatus, error) {
	title := strings.TrimSpace(command.Title)
	if title == "" {
		title = "Follow up on text"
	}
	command.Creator.Email = strings.ToLower(strings.TrimSpace(command.Creator.Email))
	if tx == nil ||
		m.access == nil ||
		strings.TrimSpace(command.MessageID) == "" ||
		strings.TrimSpace(command.ThreadID) == "" ||
		strings.TrimSpace(command.PracticeID) == "" ||
		strings.TrimSpace(command.LocationID) == "" ||
		!canonicalPhone.MatchString(command.Phone) ||
		!textLengthBetween(title, 1, 500) ||
		strings.TrimSpace(command.Creator.Subject) == "" ||
		command.Creator.Email == "" {
		return Task{}, "", ErrInvalidInput
	}
	createdAt := m.now()
	var taskID string
	err := tx.QueryRow(ctx, `
		INSERT INTO work_tasks (
			practice_id,
			location_id,
			call_id,
			phone,
			title,
			state,
			origin,
			urgency,
			created_by_kind,
			created_by_subject,
			created_by_email,
			created_at,
			source_message_id,
			message_thread_id,
			updated_at
		)
		VALUES (
			$1, $2, NULL, $3, $4, 'OPEN', 'STAFF_MESSAGE_FOLLOW_UP',
			'normal', 'HUMAN', $5, $6, $7, $8, $9, $7
		)
		ON CONFLICT (source_message_id)
			WHERE source_message_id IS NOT NULL
		DO NOTHING
		RETURNING id::text
	`, command.PracticeID, command.LocationID, command.Phone, title,
		command.Creator.Subject, command.Creator.Email, createdAt,
		command.MessageID, command.ThreadID,
	).Scan(&taskID)
	status := TaskCreated
	if errors.Is(err, pgx.ErrNoRows) {
		status = TaskDuplicate
		if err := tx.QueryRow(ctx, `
			SELECT id::text
			FROM work_tasks
			WHERE source_message_id = $1
			FOR SHARE
		`, command.MessageID).Scan(&taskID); err != nil {
			return Task{}, "", fmt.Errorf("load replayed Message Task: %w", err)
		}
	} else if err != nil {
		return Task{}, "", fmt.Errorf("create Message follow-up Task: %w", err)
	}
	task, err := loadTask(ctx, tx, taskID)
	if err != nil {
		return Task{}, "", err
	}
	if task.PracticeID != command.PracticeID ||
		task.LocationID != command.LocationID ||
		task.Phone != command.Phone ||
		task.MessageThreadID != command.ThreadID {
		return Task{}, "", ErrConflict
	}
	if status == TaskDuplicate {
		return task, status, nil
	}
	if err := appendActivity(
		ctx,
		tx,
		task,
		"TASK_CREATED",
		task.CreatedBy,
		createdAt,
	); err != nil {
		return Task{}, "", err
	}
	if _, err := m.access.RecordWorkspaceChange(
		ctx,
		tx,
		task.PracticeID,
	); err != nil {
		return Task{}, "", err
	}
	return task, status, nil
}

// EnsureRecoveryTask attaches compatible missed-call and voicemail evidence to
// one open recovery Task. HumanCalling owns the caller outcome transaction;
// Work owns the Task and Interaction attachment written inside it.
func (m *Module) EnsureRecoveryTask(
	ctx context.Context,
	tx pgx.Tx,
	command EnsureRecoveryTaskCommand,
) (Task, error) {
	command.CallID = strings.TrimSpace(command.CallID)
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.LocationID = strings.TrimSpace(command.LocationID)
	command.Phone = strings.TrimSpace(command.Phone)
	command.CallerName = strings.TrimSpace(command.CallerName)
	if command.OccurredAt.IsZero() {
		command.OccurredAt = m.now()
	}
	title := ""
	origin := TaskOrigin("")
	switch command.Outcome {
	case RecoveryOutcomeVoicemail:
		title = "Review voicemail"
		origin = TaskOriginVoicemail
	case RecoveryOutcomeMissedCall:
		title = "Return missed call"
		origin = TaskOriginMissedCall
	default:
		return Task{}, ErrInvalidInput
	}
	if tx == nil ||
		m.access == nil ||
		command.CallID == "" ||
		command.PracticeID == "" ||
		command.LocationID == "" ||
		!canonicalPhone.MatchString(command.Phone) ||
		!textLengthBetween(command.CallerName, 0, 200) {
		return Task{}, ErrInvalidInput
	}

	var taskID string
	err := tx.QueryRow(ctx, `
		INSERT INTO work_tasks (
			practice_id,
			location_id,
			call_id,
			phone,
			title,
			state,
			origin,
			urgency,
			caller_name,
			created_by_kind,
			created_by_subject,
			created_at,
			recovery_outcome,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, 'OPEN', $6, 'normal',
			NULLIF($7, ''), 'SERVICE', 'human-calling', $8, $9, $8
		)
		ON CONFLICT DO NOTHING
		RETURNING id::text
	`, command.PracticeID, command.LocationID, command.CallID, command.Phone,
		title, origin, command.CallerName, command.OccurredAt, command.Outcome,
	).Scan(&taskID)
	inserted := true
	if errors.Is(err, pgx.ErrNoRows) {
		inserted = false
		if err := tx.QueryRow(ctx, `
			SELECT task.id::text
			FROM work_tasks task
			WHERE task.practice_id = $1
				AND task.location_id = $2
				AND task.phone = $3
				AND task.state = 'OPEN'
				AND task.origin IN (
					'VOICEMAIL_RECOVERY',
					'MISSED_CALL_RECOVERY'
				)
			ORDER BY task.created_at, task.id
			LIMIT 1
			FOR UPDATE
		`, command.PracticeID, command.LocationID, command.Phone).Scan(&taskID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Task{}, ErrConflict
			}
			return Task{}, fmt.Errorf("load compatible recovery Task: %w", err)
		}
	} else if err != nil {
		return Task{}, fmt.Errorf("create recovery Task: %w", err)
	}
	interaction, err := tx.Exec(ctx, `
		INSERT INTO work_task_interactions (
			task_id,
			call_id,
			practice_id,
			location_id,
			occurred_at
		)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (call_id) DO NOTHING
	`, taskID, command.CallID, command.PracticeID, command.LocationID,
		command.OccurredAt)
	if err != nil {
		return Task{}, fmt.Errorf("attach recovery Interaction: %w", err)
	}
	if interaction.RowsAffected() == 0 {
		var linkedTaskID string
		if err := tx.QueryRow(ctx, `
			SELECT task_id::text
			FROM work_task_interactions
			WHERE call_id = $1
		`, command.CallID).Scan(&linkedTaskID); err != nil {
			return Task{}, fmt.Errorf("load replayed recovery Interaction: %w", err)
		}
		if linkedTaskID != taskID {
			return Task{}, ErrConflict
		}
		task, err := loadTask(ctx, tx, taskID)
		if err != nil {
			return Task{}, err
		}
		return task, nil
	}
	if !inserted {
		if command.Outcome == RecoveryOutcomeVoicemail {
			origin = TaskOriginVoicemail
			title = "Review voicemail"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE work_tasks
			SET
				title = CASE
					WHEN $2 = 'VOICEMAIL_RECOVERY' THEN $3
					ELSE title
				END,
				origin = CASE
					WHEN $2 = 'VOICEMAIL_RECOVERY' THEN $2
					ELSE origin
				END,
				recovery_outcome = CASE
					WHEN $2 = 'VOICEMAIL_RECOVERY' THEN 'VOICEMAIL'
					ELSE recovery_outcome
				END,
				version = version + 1,
				updated_at = GREATEST(updated_at, $4)
			WHERE id = $1
		`, taskID, origin, title, command.OccurredAt); err != nil {
			return Task{}, fmt.Errorf("enrich recovery Task: %w", err)
		}
	}
	task, err := loadTask(ctx, tx, taskID)
	if err != nil {
		return Task{}, err
	}
	if task.PracticeID != command.PracticeID ||
		task.LocationID != command.LocationID ||
		task.Phone != command.Phone ||
		(task.Origin != TaskOriginVoicemail &&
			task.Origin != TaskOriginMissedCall) {
		return Task{}, ErrConflict
	}
	activityKind := "TASK_CREATED"
	if !inserted {
		activityKind = "INTERACTION_ATTACHED"
	}
	if err := appendActivity(
		ctx,
		tx,
		task,
		activityKind,
		task.CreatedBy,
		command.OccurredAt,
	); err != nil {
		return Task{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(
		ctx,
		tx,
		task.PracticeID,
	); err != nil {
		return Task{}, err
	}
	return task, nil
}

// LockOpenMessageTask validates the exact destination exposed by a Task
// composer while serializing against completion. Messaging owns the provider
// effect; Work owns whether the Task can currently originate it.
func (m *Module) LockOpenMessageTask(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	practiceID string,
	locationID string,
	threadID string,
	phone string,
) (Task, error) {
	if tx == nil ||
		strings.TrimSpace(taskID) == "" ||
		strings.TrimSpace(practiceID) == "" ||
		strings.TrimSpace(locationID) == "" ||
		strings.TrimSpace(threadID) == "" ||
		!canonicalPhone.MatchString(phone) {
		return Task{}, ErrInvalidInput
	}
	task, err := lockTask(ctx, tx, taskID)
	if err != nil {
		return Task{}, err
	}
	if task.State != TaskOpen ||
		task.PracticeID != practiceID ||
		task.LocationID != locationID ||
		task.Phone != phone ||
		(task.MessageThreadID != "" && task.MessageThreadID != threadID) {
		return Task{}, ErrConflict
	}
	return task, nil
}

// LockOpenOutboundTask resolves the immutable Location and destination used by
// HumanCalling. The caller owns the transaction and must apply current Access
// authorization before committing a Call.
func (m *Module) LockOpenOutboundTask(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
) (Task, error) {
	if tx == nil || strings.TrimSpace(taskID) == "" {
		return Task{}, ErrInvalidInput
	}
	task, err := lockTask(ctx, tx, taskID)
	if err != nil {
		return Task{}, err
	}
	if task.State != TaskOpen {
		return Task{}, ErrConflict
	}
	return task, nil
}

// ApplyCallTaskDisposition atomically completes the existing Task or preserves
// it open after a connected Task-originated Call. It never creates another
// piece of work.
func (m *Module) ApplyCallTaskDisposition(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	complete bool,
	actor access.Actor,
	occurredAt time.Time,
) (Task, error) {
	if tx == nil || strings.TrimSpace(taskID) == "" {
		return Task{}, ErrInvalidInput
	}
	task, err := lockTask(ctx, tx, taskID)
	if err != nil {
		return Task{}, err
	}
	if !complete || task.State == TaskCompleted {
		return task, nil
	}
	if task.State != TaskOpen {
		return Task{}, ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE work_tasks
		SET
			state = 'COMPLETED',
			completed_by_subject = $2,
			completed_by_email = $3,
			completed_at = $4,
			version = version + 1,
			updated_at = $4
		WHERE id = $1
	`, task.ID, actor.Subject, actor.Email, occurredAt); err != nil {
		return Task{}, fmt.Errorf("complete Call Task: %w", err)
	}
	task.State = TaskCompleted
	task.Version++
	task.UpdatedAt = occurredAt
	task.CompletedAt = &occurredAt
	completedBy := humanActorSnapshot(actor)
	task.CompletedBy = &completedBy
	if err := appendActivity(
		ctx,
		tx,
		task,
		"TASK_COMPLETED",
		completedBy,
		occurredAt,
	); err != nil {
		return Task{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(
		ctx,
		tx,
		task.PracticeID,
	); err != nil {
		return Task{}, err
	}
	return task, nil
}

// CreateAITask accepts one authenticated Abita outcome and commits its Task,
// immutable source, creation Activity, idempotency fingerprint, and workspace
// version in one transaction.
func (m *Module) CreateAITask(
	ctx context.Context,
	command CreateAITaskCommand,
) (Task, TaskCreateStatus, error) {
	normalizeAITaskCommand(&command)
	if m.pool == nil || m.access == nil || !validAITaskCommand(command) {
		return Task{}, "", ErrInvalidInput
	}
	fingerprint, err := aiTaskFingerprint(command)
	if err != nil {
		return Task{}, "", err
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Task{}, "", fmt.Errorf("begin AI Task creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockServiceAuthorization(
		ctx,
		tx,
		command.Service,
		command.OfficeKey,
		access.ServiceCapabilityCreateTask,
	)
	if err != nil {
		if errors.Is(err, access.ErrDenied) {
			return Task{}, "", ErrDenied
		}
		return Task{}, "", fmt.Errorf("authorize AI Task creation: %w", err)
	}

	createdAt := m.now()
	var taskID string
	err = tx.QueryRow(ctx, `
		INSERT INTO work_tasks (
			practice_id,
			location_id,
			call_id,
			phone,
			title,
			state,
			origin,
			urgency,
			category,
			caller_name,
			source_call_id,
			source_message,
			created_by_kind,
			created_by_subject,
			created_by_email,
			created_at,
			ai_idempotency_key,
			ai_input_fingerprint,
			updated_at
		)
		VALUES (
			$1, $2, NULL, $3, $4, 'OPEN', 'ABITA_AI', $5, $6, $7, $8,
			$9, 'SERVICE', $10, NULL, $11, $12, $13, $11
		)
		ON CONFLICT (created_by_subject, ai_idempotency_key)
			WHERE origin = 'ABITA_AI'
		DO NOTHING
		RETURNING id::text
	`,
		authorization.PracticeID,
		authorization.LocationID,
		command.Phone,
		command.Summary,
		command.Urgency,
		command.Category,
		nullIfEmpty(command.CallerName),
		command.SourceCallID,
		command.Message,
		command.Service.Subject,
		createdAt,
		command.IdempotencyKey,
		fingerprint[:],
	).Scan(&taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingFingerprint []byte
		if err := tx.QueryRow(ctx, `
			SELECT id::text, ai_input_fingerprint
			FROM work_tasks
			WHERE origin = 'ABITA_AI'
				AND created_by_subject = $1
				AND ai_idempotency_key = $2
			FOR SHARE
		`, command.Service.Subject, command.IdempotencyKey).Scan(
			&taskID,
			&existingFingerprint,
		); err != nil {
			return Task{}, "", fmt.Errorf("load replayed AI Task: %w", err)
		}
		if !bytes.Equal(existingFingerprint, fingerprint[:]) {
			return Task{}, "", ErrConflict
		}
		task, err := loadTask(ctx, tx, taskID)
		if err != nil {
			return Task{}, "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return Task{}, "", fmt.Errorf("commit AI Task replay: %w", err)
		}
		return task, TaskDuplicate, nil
	}
	if err != nil {
		return Task{}, "", fmt.Errorf("create AI Task: %w", err)
	}
	task, err := loadTask(ctx, tx, taskID)
	if err != nil {
		return Task{}, "", err
	}
	if err := appendActivity(ctx, tx, task, "TASK_CREATED", task.CreatedBy, createdAt); err != nil {
		return Task{}, "", err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, task.PracticeID); err != nil {
		return Task{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, "", fmt.Errorf("commit AI Task creation: %w", err)
	}
	return task, TaskCreated, nil
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
	authorization, err := m.authorizeMutation(
		ctx,
		tx,
		command.Identity,
		task,
		command.SupportSessionID,
	)
	if err != nil {
		return Task{}, err
	}
	actor := authorization.Actor
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
		humanActorSnapshot(actor),
		changedAt,
	); err != nil {
		return Task{}, err
	}
	if err := m.auditSupportedMutation(
		ctx,
		tx,
		authorization,
		task,
		"task.title_changed",
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
	authorization, err := m.authorizeMutation(
		ctx,
		tx,
		command.Identity,
		task,
		command.SupportSessionID,
	)
	if err != nil {
		return Task{}, err
	}
	actor := authorization.Actor
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
	completionActor := humanActorSnapshot(actor)
	task.CompletedBy = &completionActor
	task.CompletedAt = &completedAt
	task.UpdatedAt = completedAt
	if err := appendActivity(
		ctx,
		tx,
		task,
		"TASK_COMPLETED",
		humanActorSnapshot(actor),
		completedAt,
	); err != nil {
		return Task{}, err
	}
	if err := m.auditSupportedMutation(
		ctx,
		tx,
		authorization,
		task,
		"task.completed",
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
	authorization, err := m.authorizeMutation(
		ctx,
		tx,
		command.Identity,
		task,
		command.SupportSessionID,
	)
	if err != nil {
		return Task{}, err
	}
	actor := authorization.Actor
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
		humanActorSnapshot(actor),
		reopenedAt,
	); err != nil {
		return Task{}, err
	}
	if err := m.auditSupportedMutation(
		ctx,
		tx,
		authorization,
		task,
		"task.reopened",
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
	if command.State == "" {
		command.State = TaskOpen
	}
	if command.Ordering == "" {
		command.Ordering = TaskOrderingPriority
	}
	if m.access == nil ||
		strings.TrimSpace(command.PracticeID) == "" ||
		len(command.Search) > 500 ||
		(command.State != TaskOpen && command.State != TaskCompleted) ||
		(command.Ordering != TaskOrderingTime &&
			command.Ordering != TaskOrderingPriority &&
			command.Ordering != TaskOrderingRecent) {
		return TaskPage{}, ErrInvalidInput
	}
	limit := command.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 50 {
		return TaskPage{}, ErrInvalidInput
	}
	cursor, err := decodeTaskCursor(command.Cursor, command.Ordering, command.State)
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
	)
	if err != nil {
		return TaskPage{}, fmt.Errorf("query Tasks: %w", err)
	}
	defer rows.Close()
	items := make([]Task, 0, limit+1)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return TaskPage{}, fmt.Errorf("scan Task query: %w", err)
		}
		items = append(items, task)
	}
	if err := rows.Err(); err != nil {
		return TaskPage{}, fmt.Errorf("iterate Tasks: %w", err)
	}
	rows.Close()
	for index := range items {
		if err := m.loadRelatedInteractionCount(ctx, tx, &items[index]); err != nil {
			return TaskPage{}, err
		}
		if err := loadTaskUnread(ctx, tx, command.Identity, &items[index]); err != nil {
			return TaskPage{}, err
		}
	}

	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		nextCursor, err = encodeTaskCursor(
			items[len(items)-1],
			command.Ordering,
		)
		if err != nil {
			return TaskPage{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return TaskPage{}, fmt.Errorf("commit Task query: %w", err)
	}
	return TaskPage{Items: items, NextCursor: nextCursor}, nil
}

const taskQuerySelect = `
		SELECT
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
			)`

func taskQuerySQL(state TaskState, ordering TaskOrdering) string {
	switch {
	case state == TaskOpen && ordering == TaskOrderingPriority:
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
					AND (task.updated_at, task.id::text) < ($6, $7)
				)
			)
		ORDER BY
			CASE task.urgency
				WHEN 'high_priority' THEN 0
				WHEN 'normal' THEN 1
				ELSE 2
			END,
			task.updated_at DESC,
			task.id DESC
		LIMIT $9`
	case state == TaskOpen && ordering == TaskOrderingTime:
		return taskQuerySelect + `
			AND task.state = 'OPEN'
			AND $8::int >= 0
			AND (NOT $5 OR (task.created_at, task.id::text) > ($6, $7))
		ORDER BY task.created_at, task.id
		LIMIT $9`
	case state == TaskOpen:
		return taskQuerySelect + `
			AND task.state = 'OPEN'
			AND $8::int >= 0
			AND (NOT $5 OR (task.updated_at, task.id::text) < ($6, $7))
		ORDER BY task.updated_at DESC, task.id DESC
		LIMIT $9`
	case ordering == TaskOrderingRecent:
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
	if err := m.loadRelatedInteractionCount(ctx, tx, &task); err != nil {
		return Task{}, err
	}
	if err := m.loadTaskInteractions(ctx, tx, &task); err != nil {
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
	if err := loadTaskUnread(ctx, tx, identity, &task); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, fmt.Errorf("commit Task read: %w", err)
	}
	return task, nil
}

// MarkTaskRead records that one User listened to or otherwise reviewed a
// recovery Task. It deliberately does not complete or mutate the Task.
func (m *Module) MarkTaskRead(
	ctx context.Context,
	identity access.Identity,
	taskID string,
) error {
	taskID = strings.TrimSpace(taskID)
	if m.pool == nil || m.access == nil || taskID == "" {
		return ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin Task read receipt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := loadTask(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if _, err := m.access.LockReadAuthorization(
		ctx, tx, identity, task.PracticeID, task.LocationID,
	); err != nil {
		return ErrDenied
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO work_task_reads (task_id, user_subject, read_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (task_id, user_subject) DO NOTHING
	`, task.ID, identity.Subject, m.now())
	if err != nil {
		return fmt.Errorf("record Task read receipt: %w", err)
	}
	if tag.RowsAffected() > 0 {
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, task.PracticeID); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Task read receipt: %w", err)
	}
	return nil
}

func loadTaskUnread(
	ctx context.Context,
	tx pgx.Tx,
	identity access.Identity,
	task *Task,
) error {
	if task == nil {
		return ErrInvalidInput
	}
	task.Unread = false
	if task.State != TaskOpen ||
		(task.Origin != TaskOriginMissedCall && task.Origin != TaskOriginVoicemail) {
		return nil
	}
	if err := tx.QueryRow(ctx, `
		SELECT NOT EXISTS (
			SELECT 1 FROM work_task_reads
			WHERE task_id = $1 AND user_subject = $2
		)
	`, task.ID, identity.Subject).Scan(&task.Unread); err != nil {
		return fmt.Errorf("read Task unread state: %w", err)
	}
	return nil
}

func (m *Module) loadTaskInteractions(ctx context.Context, tx pgx.Tx, task *Task) error {
	if task == nil || task.ID == "" {
		return ErrInvalidInput
	}
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
	task.Interactions = nil
	for rows.Next() {
		var interaction TaskInteraction
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

func (m *Module) loadRelatedInteractionCount(ctx context.Context, querier taskQuerier, task *Task) error {
	if task == nil || task.ID == "" {
		return ErrInvalidInput
	}
	if err := querier.QueryRow(ctx, `
		SELECT count(*)
		FROM work_task_interactions
		WHERE task_id = $1
	`, task.ID).Scan(&task.RelatedInteractionCount); err != nil {
		return fmt.Errorf("count related Task Interactions: %w", err)
	}
	return nil
}

type taskQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type taskCursor struct {
	Present   bool         `json:"-"`
	Ordering  TaskOrdering `json:"ordering"`
	State     TaskState    `json:"state"`
	Urgency   TaskUrgency  `json:"urgency"`
	OrderedAt time.Time    `json:"orderedAt"`
	ID        string       `json:"id"`
}

func encodeTaskCursor(task Task, ordering TaskOrdering) (string, error) {
	orderedAt := task.CreatedAt
	if ordering == TaskOrderingRecent ||
		(ordering == TaskOrderingPriority && task.State == TaskOpen) {
		orderedAt = task.UpdatedAt
	} else if task.State == TaskCompleted {
		if task.CompletedAt == nil {
			return "", fmt.Errorf("encode Task cursor: completed Task has no completion time")
		}
		orderedAt = *task.CompletedAt
	}
	encoded, err := json.Marshal(taskCursor{
		Ordering:  ordering,
		State:     task.State,
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
	encoded string,
	ordering TaskOrdering,
	state TaskState,
) (taskCursor, error) {
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
	if cursor.Urgency == "" {
		cursor.Urgency = TaskUrgencyNormal
	}
	if cursor.Ordering != ordering ||
		cursor.State != state ||
		(cursor.State == TaskOpen && !validTaskUrgency(cursor.Urgency)) ||
		cursor.OrderedAt.IsZero() ||
		strings.TrimSpace(cursor.ID) == "" {
		return taskCursor{}, ErrInvalidInput
	}
	cursor.Present = true
	return cursor, nil
}

func urgencyRank(urgency TaskUrgency) int {
	switch urgency {
	case TaskUrgencyHighPriority:
		return 0
	case TaskUrgencyNormal:
		return 1
	default:
		return 2
	}
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
) (access.Authorization, error) {
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
			return access.Authorization{}, err
		}
		return access.Authorization{}, ErrDenied
	}
	return authorization, nil
}

func (m *Module) auditSupportedMutation(
	ctx context.Context,
	tx pgx.Tx,
	authorization access.Authorization,
	task Task,
	action string,
	occurredAt time.Time,
) error {
	return m.access.AuditSupportedMutation(
		ctx,
		tx,
		authorization,
		access.SupportedMutationAudit{
			Action:          action,
			ResourceType:    "task",
			ResourceID:      task.ID,
			ResourceVersion: task.Version,
			OccurredAt:      occurredAt,
		},
	)
}

func appendActivity(
	ctx context.Context,
	tx pgx.Tx,
	task Task,
	kind string,
	actor ActorSnapshot,
	occurredAt time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO work_task_activities (
			task_id,
			task_version,
			kind,
			actor_kind,
			actor_subject,
			actor_email,
			occurred_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, task.ID, task.Version, kind, actor.Kind, actor.Subject,
		nullIfEmpty(strings.ToLower(strings.TrimSpace(actor.Email))), occurredAt,
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
	var category, callerName, sourceCall, sourceMessage *string
	var messageID, messageThreadID, recoveryOutcome *string
	var createdEmail, completedSubject, completedEmail *string
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
				origin,
				urgency,
				created_by_kind,
				created_by_subject,
				created_by_email,
				created_at,
				updated_at
			)
			VALUES (
				$1, $2, $3, $4, $5, 'OPEN', 'HUMAN_CALL_FOLLOW_UP',
				'normal', 'HUMAN', $6, $7, $8, $8
			)
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
		&inserted,
	)
	if err != nil {
		return Task{}, false, fmt.Errorf("ensure Call follow-up Task: %w", err)
	}
	setTaskSource(
		&task,
		category,
		callerName,
		sourceCall,
		sourceMessage,
		messageID,
		messageThreadID,
		recoveryOutcome,
		createdEmail,
	)
	setCompletionActor(&task, completedSubject, completedEmail)
	return task, inserted, nil
}

func loadTask(
	ctx context.Context,
	querier taskQuerier,
	taskID string,
) (Task, error) {
	task, err := scanTask(querier.QueryRow(ctx, `
		SELECT
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
			task.updated_at
		FROM work_tasks task
		JOIN access_locations location
			ON location.practice_id = task.practice_id
			AND location.id = task.location_id
		WHERE task.id = $1
	`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrDenied
	}
	if err != nil {
		return Task{}, fmt.Errorf("read Task: %w", err)
	}
	return task, nil
}

func lockTask(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
) (Task, error) {
	task, err := scanTask(tx.QueryRow(ctx, `
		SELECT
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
			task.updated_at
		FROM work_tasks task
		JOIN access_locations location
			ON location.practice_id = task.practice_id
			AND location.id = task.location_id
		WHERE task.id = $1
		FOR UPDATE OF task
	`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrDenied
	}
	if err != nil {
		return Task{}, fmt.Errorf("lock Task: %w", err)
	}
	return task, nil
}

type taskScanner interface {
	Scan(...any) error
}

func scanTask(scanner taskScanner) (Task, error) {
	var task Task
	var callID, category, callerName, sourceCall, sourceMessage *string
	var messageID, messageThreadID, recoveryOutcome *string
	var createdEmail, completedSubject, completedEmail *string
	if err := scanner.Scan(
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
	); err != nil {
		return Task{}, err
	}
	if callID != nil {
		task.CallID = *callID
	}
	setTaskSource(
		&task,
		category,
		callerName,
		sourceCall,
		sourceMessage,
		messageID,
		messageThreadID,
		recoveryOutcome,
		createdEmail,
	)
	setCompletionActor(&task, completedSubject, completedEmail)
	return task, nil
}

func setTaskSource(
	task *Task,
	category *string,
	callerName *string,
	sourceCall *string,
	sourceMessage *string,
	messageID *string,
	messageThreadID *string,
	recoveryOutcome *string,
	createdEmail *string,
) {
	if category != nil {
		task.Category = TaskCategory(*category)
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
		task.RecoveryOutcome = RecoveryOutcome(*recoveryOutcome)
	}
	if createdEmail != nil {
		task.CreatedBy.Email = *createdEmail
	}
}

func setCompletionActor(
	task *Task,
	subject *string,
	email *string,
) {
	if subject == nil || email == nil {
		return
	}
	task.CompletedBy = &ActorSnapshot{
		Kind:    access.ActorHuman,
		Subject: *subject,
		Email:   *email,
	}
}

func humanActorSnapshot(actor access.Actor) ActorSnapshot {
	return ActorSnapshot{
		Kind:    access.ActorHuman,
		Subject: actor.Subject,
		Email:   actor.Email,
	}
}

func normalizeAITaskCommand(command *CreateAITaskCommand) {
	command.Service.Subject = strings.TrimSpace(command.Service.Subject)
	command.Service.PracticeID = strings.TrimSpace(command.Service.PracticeID)
	command.OfficeKey = strings.TrimSpace(command.OfficeKey)
	command.OfficePhone = strings.TrimSpace(command.OfficePhone)
	command.InboundOfficePhone = strings.TrimSpace(command.InboundOfficePhone)
	command.SourceCallID = strings.TrimSpace(command.SourceCallID)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.Phone = strings.TrimSpace(command.Phone)
	command.CallerName = strings.TrimSpace(command.CallerName)
	command.CompatibilityPatientID = strings.TrimSpace(
		command.CompatibilityPatientID,
	)
	command.CompatibilityPatientDOB = strings.TrimSpace(
		command.CompatibilityPatientDOB,
	)
	command.Summary = strings.TrimSpace(command.Summary)
	command.Message = strings.TrimSpace(command.Message)
}

func validAITaskCommand(command CreateAITaskCommand) bool {
	return officeKey.MatchString(command.OfficeKey) &&
		textLengthBetween(command.SourceCallID, 1, 255) &&
		idempotencyKey.MatchString(command.IdempotencyKey) &&
		canonicalPhone.MatchString(command.Phone) &&
		canonicalPhone.MatchString(command.OfficePhone) &&
		(command.InboundOfficePhone == "" ||
			canonicalPhone.MatchString(command.InboundOfficePhone)) &&
		textLengthBetween(command.Summary, 1, 240) &&
		textLengthBetween(command.Message, 1, 2500) &&
		textLengthBetween(command.CallerName, 0, 200) &&
		textLengthBetween(command.CompatibilityPatientID, 0, 255) &&
		textLengthBetween(command.CompatibilityPatientDOB, 0, 64) &&
		validTaskCategory(command.Category) &&
		validTaskUrgency(command.Urgency)
}

func validTaskCategory(category TaskCategory) bool {
	switch category {
	case TaskCategoryBilling,
		TaskCategoryAppointments,
		TaskCategoryDocumentation,
		TaskCategoryOptical,
		TaskCategoryMedication,
		TaskCategoryReferrals,
		TaskCategoryOther:
		return true
	default:
		return false
	}
}

func validTaskUrgency(urgency TaskUrgency) bool {
	switch urgency {
	case TaskUrgencyHighPriority, TaskUrgencyNormal, TaskUrgencyNonUrgent:
		return true
	default:
		return false
	}
}

func textLengthBetween(value string, minimum int, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum
}

func aiTaskFingerprint(command CreateAITaskCommand) ([32]byte, error) {
	payload, err := json.Marshal(struct {
		ServiceSubject          string       `json:"serviceSubject"`
		PracticeID              string       `json:"practiceId"`
		OfficeKey               string       `json:"officeKey"`
		OfficePhone             string       `json:"officePhone"`
		InboundOfficePhone      string       `json:"inboundOfficePhone"`
		SourceCallID            string       `json:"sourceCallId"`
		Phone                   string       `json:"phone"`
		CallerName              string       `json:"callerName"`
		CompatibilityPatientID  string       `json:"compatibilityPatientId"`
		CompatibilityPatientDOB string       `json:"compatibilityPatientDob"`
		Summary                 string       `json:"summary"`
		Message                 string       `json:"message"`
		Category                TaskCategory `json:"category"`
		Urgency                 TaskUrgency  `json:"urgency"`
	}{
		ServiceSubject:          command.Service.Subject,
		PracticeID:              command.Service.PracticeID,
		OfficeKey:               command.OfficeKey,
		OfficePhone:             command.OfficePhone,
		InboundOfficePhone:      command.InboundOfficePhone,
		SourceCallID:            command.SourceCallID,
		Phone:                   command.Phone,
		CallerName:              command.CallerName,
		CompatibilityPatientID:  command.CompatibilityPatientID,
		CompatibilityPatientDOB: command.CompatibilityPatientDOB,
		Summary:                 command.Summary,
		Message:                 command.Message,
		Category:                command.Category,
		Urgency:                 command.Urgency,
	})
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode AI Task fingerprint: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
