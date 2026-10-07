package workspace

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
)

const TaskChangeLimit = 200

type QueryTaskChangesCommand struct {
	Identity       access.Identity
	PracticeID     string
	LocationID     string
	SinceVersion   int64
	Kind           string
	Responsibility string
	Category       work.TaskCategory
	Search         string
}

type TaskChanges struct {
	Version        int64
	Complete       bool
	Tasks          []work.Task
	OpenTasks      []work.Task
	CompletedTasks []work.Task
	Counts         *work.TaskFolderCounts
}

const changedTaskReadQuery = `SELECT` + taskReadColumns + ` FROM work_tasks task` + taskProjectionJoins + `
	WHERE task.id = ANY($1::uuid[]) AND task.practice_id = $2 AND task.location_id = ANY($3::uuid[])
	ORDER BY task.id`

const changedTaskFamilies = `
	AND (task.location_id, task.phone) IN (
		SELECT changed.location_id, changed.phone FROM work_tasks changed WHERE changed.id = ANY($14::uuid[])
	)`

const changedTaskIdentity = `
	AND task.id = ANY($14::uuid[])`

func (m *Module) QueryTaskChanges(
	ctx context.Context,
	command QueryTaskChangesCommand,
) (TaskChanges, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.LocationID = strings.TrimSpace(command.LocationID)
	command.Search = strings.TrimSpace(command.Search)
	if m.database == nil || m.access == nil || command.PracticeID == "" ||
		command.SinceVersion < 1 || len(command.Search) > 500 ||
		(command.Kind != "" && command.Kind != "texts" && command.Kind != "calls" && command.Kind != "appointments" && command.Kind != "follow_up") ||
		(command.Responsibility != "" && command.Responsibility != "mine" && command.Responsibility != "all") {
		return TaskChanges{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return TaskChanges{}, fmt.Errorf("begin Task change query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, locationIDs, err := m.authorizedScope(
		ctx, tx, command.Identity, command.PracticeID, command.LocationID,
	)
	if err != nil {
		return TaskChanges{}, err
	}
	incomplete := TaskChanges{Version: authorization.Practice.Version}
	changedIDs, complete, err := changedTaskIDs(ctx, tx, command.PracticeID, command.SinceVersion, incomplete.Version)
	if err != nil || !complete {
		return incomplete, err
	}
	changes := TaskChanges{Version: incomplete.Version, Complete: true, OpenTasks: []work.Task{}, CompletedTasks: []work.Task{}}
	changes.Tasks, err = queryTaskProjections(ctx, tx, changedTaskReadQuery, changedIDs, command.PracticeID, locationIDs)
	if err != nil {
		return TaskChanges{}, err
	}
	if len(changes.Tasks) > 0 {
		visibleIDs := make([]string, 0, len(changes.Tasks))
		for _, task := range changes.Tasks {
			visibleIDs = append(visibleIDs, task.ID)
		}
		arguments := func(kind string) []any {
			return []any{
				command.PracticeID, locationIDs, command.Search, normalizedDigits(command.Search),
				false, time.Time{}, "", work.TaskUrgency("").Rank(), TaskChangeLimit + 1,
				command.Responsibility, strings.ToLower(command.Identity.Email), command.Category, kind, visibleIDs,
			}
		}
		changes.OpenTasks, err = queryTaskProjections(ctx, tx,
			taskQuerySQL(work.TaskOpen, work.TaskOrderingRecent, true, changedTaskFamilies), arguments(command.Kind)...)
		if err != nil {
			return TaskChanges{}, err
		}
		if len(changes.OpenTasks) > TaskChangeLimit {
			return incomplete, nil
		}
		if err := loadGroupMembers(ctx, tx, changes.OpenTasks); err != nil {
			return TaskChanges{}, err
		}
		changes.CompletedTasks, err = queryTaskProjections(ctx, tx,
			taskQuerySQL(work.TaskCompleted, work.TaskOrderingRecent, false, changedTaskIdentity), arguments("")...)
		if err != nil {
			return TaskChanges{}, err
		}
	}
	counts, err := queryTaskFolderCounts(ctx, tx, QueryTasksCommand{
		Identity:       command.Identity,
		PracticeID:     command.PracticeID,
		Search:         command.Search,
		State:          work.TaskOpen,
		Responsibility: command.Responsibility,
		Kind:           command.Kind,
	}, locationIDs)
	if err != nil {
		return TaskChanges{}, err
	}
	changes.Counts = &counts
	if err := tx.Commit(ctx); err != nil {
		return TaskChanges{}, fmt.Errorf("commit Task change query: %w", err)
	}
	return changes, nil
}

func changedTaskIDs(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	since int64,
	current int64,
) ([]string, bool, error) {
	if since > current || current-since > access.WorkspaceChangeRetention {
		return nil, false, nil
	}
	if since == current {
		return []string{}, true, nil
	}
	var versions int64
	var declared bool
	var ids []string
	if err := tx.QueryRow(ctx, `
		SELECT
			count(DISTINCT change.version),
			COALESCE(bool_and(change.task_ids IS NOT NULL), false),
			COALESCE(array_agg(DISTINCT changed.task_id::text) FILTER (WHERE changed.task_id IS NOT NULL), '{}')
		FROM access_workspace_changes change
		LEFT JOIN LATERAL unnest(change.task_ids) AS changed(task_id) ON true
		WHERE change.practice_id = $1 AND change.version > $2 AND change.version <= $3
	`, practiceID, since, current).Scan(&versions, &declared, &ids); err != nil {
		return nil, false, fmt.Errorf("read workspace Task changes: %w", err)
	}
	if versions != current-since || !declared || len(ids) > TaskChangeLimit {
		return nil, false, nil
	}
	return ids, true, nil
}
