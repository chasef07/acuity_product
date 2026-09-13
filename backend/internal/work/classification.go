package work

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

type ChangeTaskCategoryCommand struct {
	Identity        access.Identity
	TaskID          string
	ExpectedVersion int64
	Category        TaskCategory
}

func (m *Module) ChangeTaskCategory(ctx context.Context, command ChangeTaskCategoryCommand) (Task, error) {
	if m.access == nil || strings.TrimSpace(command.TaskID) == "" || command.ExpectedVersion < 1 || !validTaskCategory(command.Category) || command.Category == TaskCategoryBilling {
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
	if task.State != TaskOpen || task.Version != command.ExpectedVersion {
		return Task{}, ErrConflict
	}
	if task.Category == command.Category {
		return task, nil
	}
	previous := task.Category
	task.Category = command.Category
	task.UpdatedAt = m.now()
	err = tx.QueryRow(ctx, `UPDATE work_tasks SET category=$2, version=version+1, updated_at=$3 WHERE id=$1 RETURNING version`, task.ID, task.Category, task.UpdatedAt).Scan(&task.Version)
	if err != nil {
		return Task{}, err
	}
	if err := appendActivityDetails(ctx, tx, task, "CATEGORY_CHANGED", humanActorSnapshot(auth.Actor), map[string]any{"oldCategory": previous, "newCategory": task.Category}); err != nil {
		return Task{}, err
	}
	if err := m.auditOperatorMutation(ctx, tx, auth, task, "task.category_changed", task.UpdatedAt); err != nil {
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

func appendActivityDetails(ctx context.Context, tx pgx.Tx, task Task, kind string, actor ActorSnapshot, details map[string]any) error {
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO work_task_activities(task_id,task_version,kind,actor_kind,actor_subject,actor_email,occurred_at,details) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, task.ID, task.Version, kind, actor.Kind, actor.Subject, nullIfEmpty(strings.ToLower(strings.TrimSpace(actor.Email))), task.UpdatedAt, encoded)
	return err
}
