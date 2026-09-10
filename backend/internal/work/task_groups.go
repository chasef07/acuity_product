package work

import (
	"context"
	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
	"sort"
)

type ReviewedTask struct {
	ID              string
	ExpectedVersion int64
}
type CompleteTaskGroupCommand struct {
	Identity access.Identity
	TaskID   string
	Members  []ReviewedTask
}

func (m *Module) CompleteTaskGroup(ctx context.Context, command CompleteTaskGroupCommand) (Task, error) {
	if len(command.Members) == 0 || command.TaskID == "" {
		return Task{}, ErrInvalidInput
	}
	expected := map[string]int64{}
	for _, member := range command.Members {
		if member.ID == "" || member.ExpectedVersion < 1 || expected[member.ID] != 0 {
			return Task{}, ErrInvalidInput
		}
		expected[member.ID] = member.ExpectedVersion
	}
	if expected[command.TaskID] == 0 {
		return Task{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	anchor, err := loadTask(ctx, tx, command.TaskID)
	if err != nil {
		return Task{}, err
	}
	auth, err := m.authorizeMutation(ctx, tx, command.Identity, anchor)
	if err != nil {
		return Task{}, err
	}
	// Lock every reviewed member in deterministic order before checking membership.
	// Newly arriving work is never an implicit target of this command.
	ids := make([]string, 0, len(expected))
	for id := range expected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	tasks := make([]Task, 0, len(ids))
	for _, id := range ids {
		task, err := lockTask(ctx, tx, id)
		if err != nil {
			return Task{}, err
		}
		if task.PracticeID != anchor.PracticeID || task.LocationID != anchor.LocationID {
			return Task{}, ErrDenied
		}
		if task.State != TaskOpen || task.Version != expected[id] || task.Phone != anchor.Phone || task.Category != anchor.Category {
			return Task{}, ErrConflict
		}
		tasks = append(tasks, task)
	}
	var currentIDs []string
	err = tx.QueryRow(ctx, `SELECT array_agg(id::text ORDER BY id::text) FROM work_tasks WHERE practice_id=$1 AND location_id=$2 AND phone=$3 AND category IS NOT DISTINCT FROM $4::text AND state='OPEN'`, anchor.PracticeID, anchor.LocationID, anchor.Phone, nullIfEmpty(string(anchor.Category))).Scan(&currentIDs)
	if err != nil {
		return Task{}, err
	}
	if len(currentIDs) != len(ids) {
		return Task{}, ErrConflict
	}
	for i, id := range ids {
		if currentIDs[i] != id {
			return Task{}, ErrConflict
		}
	}
	for _, task := range tasks {
		completed, err := m.completeLockedTask(ctx, tx, auth, task)
		if err != nil {
			return Task{}, err
		}
		if task.ID == anchor.ID {
			anchor = completed
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, err
	}
	return anchor, nil
}
