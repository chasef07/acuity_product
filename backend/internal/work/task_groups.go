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
	anchor, err := loadTask(ctx, tx, command.TaskID, false)
	if err != nil {
		return Task{}, err
	}
	auth, err := m.authorizeMutation(ctx, tx, command.Identity, anchor)
	if err != nil {
		return Task{}, err
	}
	ids := make([]string, 0, len(expected))
	for id := range expected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rows, err := tx.Query(ctx, `SELECT id::text, practice_id::text, location_id::text, state, version, phone, COALESCE(category, ''), origin FROM work_tasks WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return Task{}, err
	}
	tasks, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Task, error) {
		var task Task
		err := row.Scan(&task.ID, &task.PracticeID, &task.LocationID, &task.State, &task.Version, &task.Phone, &task.Category, &task.Origin)
		return task, err
	})
	if err != nil {
		return Task{}, err
	}
	for i, id := range ids {
		if i >= len(tasks) || tasks[i].ID != id {
			return Task{}, ErrDenied
		}
		task := tasks[i]
		if task.PracticeID != anchor.PracticeID || task.LocationID != anchor.LocationID {
			return Task{}, ErrDenied
		}
		if task.State != TaskOpen || task.Version != expected[id] || task.Phone != anchor.Phone || task.Category != anchor.Category || task.Origin != anchor.Origin {
			return Task{}, ErrConflict
		}
		if id == anchor.ID {
			if task.Version != anchor.Version {
				return Task{}, ErrConflict
			}
			tasks[i] = anchor
		}
	}
	var currentIDs []string
	err = tx.QueryRow(ctx, `SELECT array_agg(id::text ORDER BY id::text) FROM work_tasks WHERE practice_id=$1 AND location_id=$2 AND phone=$3 AND category IS NOT DISTINCT FROM $4::text AND state='OPEN' AND origin=$5`, anchor.PracticeID, anchor.LocationID, anchor.Phone, nullIfEmpty(string(anchor.Category)), anchor.Origin).Scan(&currentIDs)
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
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, anchor.PracticeID); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, err
	}
	return anchor, nil
}
