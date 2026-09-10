package work

import (
	"context"
	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
	"strings"
)

type KnowledgeFeedbackCommand struct {
	Identity        access.Identity
	TaskID          string
	ExpectedVersion int64
	Flagged         bool
	SuggestedAnswer string
}

func (m *Module) SetKnowledgeFeedback(ctx context.Context, command KnowledgeFeedbackCommand) (Task, error) {
	command.SuggestedAnswer = strings.TrimSpace(command.SuggestedAnswer)
	if m.access == nil || strings.TrimSpace(command.TaskID) == "" || command.ExpectedVersion < 1 || !textLengthBetween(command.SuggestedAnswer, 0, 2500) {
		return Task{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := loadTask(ctx, tx, command.TaskID)
	if err != nil {
		return Task{}, err
	}
	auth, err := m.authorizeMutation(ctx, tx, command.Identity, task)
	if err != nil {
		return Task{}, err
	}
	task, err = lockTask(ctx, tx, task.ID)
	if err != nil {
		return Task{}, err
	}
	if task.Version != command.ExpectedVersion {
		return Task{}, ErrConflict
	}
	if task.KnowledgeFlagged == command.Flagged && task.SuggestedAnswer == command.SuggestedAnswer {
		return task, nil
	}
	task.KnowledgeFlagged = command.Flagged
	task.SuggestedAnswer = command.SuggestedAnswer
	task.UpdatedAt = m.now()
	task.KnowledgeUpdatedAt = &task.UpdatedAt
	task.KnowledgeUpdatedBy = &auth.Actor.Subject
	err = tx.QueryRow(ctx, `UPDATE work_tasks SET knowledge_flagged=$2,suggested_answer=$3,knowledge_updated_by=$4,knowledge_updated_at=$5,updated_at=$5,version=version+1 WHERE id=$1 RETURNING version`, task.ID, command.Flagged, command.SuggestedAnswer, auth.Actor.Subject, task.UpdatedAt).Scan(&task.Version)
	if err != nil {
		return Task{}, err
	}
	if err := appendActivityDetails(ctx, tx, task, "KNOWLEDGE_FEEDBACK_CHANGED", humanActorSnapshot(auth.Actor), map[string]any{"flagged": command.Flagged, "suggestedAnswer": command.SuggestedAnswer}); err != nil {
		return Task{}, err
	}
	if err := m.auditOperatorMutation(ctx, tx, auth, task, "task.knowledge_feedback_changed", task.UpdatedAt); err != nil {
		return Task{}, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, task.PracticeID); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, err
	}
	return task, nil
}
