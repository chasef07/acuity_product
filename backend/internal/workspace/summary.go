package workspace

import (
	"context"
	"fmt"
	"strings"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/contactcontext"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
)

type QueryPhoneSummaryCommand struct {
	Identity   access.Identity
	PracticeID string
	Phone      string
}

type PhoneSummary struct {
	Phone       string
	DisplayName string
	Names       []string
	OpenTasks   []work.Task
	ClosedToday []work.Task
}

func (m *Module) QueryPhoneSummary(
	ctx context.Context,
	command QueryPhoneSummaryCommand,
) (PhoneSummary, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.Phone = strings.TrimSpace(command.Phone)
	if m.database == nil || m.access == nil || command.PracticeID == "" ||
		!contactcontext.IsCanonicalPhone(command.Phone) {
		return PhoneSummary{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return PhoneSummary{}, fmt.Errorf("begin phone summary: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locationIDs, err := m.authorizedLocationIDs(
		ctx, tx, command.Identity, command.PracticeID, "",
	)
	if err != nil {
		return PhoneSummary{}, err
	}
	summary := PhoneSummary{Phone: command.Phone}
	if summary.OpenTasks, err = queryPhoneTasks(
		ctx, tx, phoneOpenTasksQuery, command.PracticeID, locationIDs, command.Phone,
	); err != nil {
		return PhoneSummary{}, fmt.Errorf("query open Tasks for phone: %w", err)
	}
	if summary.ClosedToday, err = queryPhoneTasks(
		ctx, tx, phoneClosedTodayQuery, command.PracticeID, locationIDs, command.Phone,
	); err != nil {
		return PhoneSummary{}, fmt.Errorf("query closed Tasks for phone: %w", err)
	}
	if summary.Names, err = queryPhoneNames(
		ctx, tx, command.PracticeID, locationIDs, command.Phone,
	); err != nil {
		return PhoneSummary{}, fmt.Errorf("query names for phone: %w", err)
	}
	if len(summary.Names) > 0 {
		summary.DisplayName = summary.Names[0]
	}
	if err := tx.Commit(ctx); err != nil {
		return PhoneSummary{}, fmt.Errorf("commit phone summary: %w", err)
	}
	return summary, nil
}

const phoneTasksSource = taskQueryColumns + `
	FROM work_tasks task` + taskProjectionJoins + `
	WHERE task.practice_id = $1
		AND task.location_id = ANY($2::uuid[])
		AND task.phone = $3`

const phoneOpenTasksQuery = phoneTasksSource + `
		AND task.state = 'OPEN'
	ORDER BY task.updated_at DESC, task.id DESC`

const phoneClosedTodayQuery = phoneTasksSource + `
		AND task.state = 'COMPLETED'
		AND task.completed_at >= CURRENT_TIMESTAMP - INTERVAL '24 hours'
	ORDER BY task.completed_at DESC, task.id DESC
	LIMIT 20`

const phoneNamesQuery = `
	SELECT latest.name
	FROM (
		SELECT DISTINCT ON (lower(seen.name)) seen.name, seen.seen_at
		FROM (
			SELECT btrim(task.caller_name) AS name, task.created_at AS seen_at
			FROM work_tasks task
			WHERE task.practice_id = $1
				AND task.location_id = ANY($2::uuid[])
				AND task.phone = $3
			UNION ALL
			SELECT btrim(thread.display_name), thread.updated_at
			FROM messaging_threads thread
			WHERE thread.practice_id = $1
				AND thread.location_id = ANY($2::uuid[])
				AND thread.external_phone = $3
		) seen
		WHERE seen.name <> ''
		ORDER BY lower(seen.name), seen.seen_at DESC
	) latest
	ORDER BY latest.seen_at DESC, latest.name
	LIMIT 5`

func queryPhoneTasks(
	ctx context.Context,
	tx pgx.Tx,
	query string,
	practiceID string,
	locationIDs []string,
	phone string,
) ([]work.Task, error) {
	rows, err := tx.Query(ctx, query, practiceID, locationIDs, phone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []work.Task{}
	for rows.Next() {
		task, err := scanTaskProjection(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func queryPhoneNames(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	locationIDs []string,
	phone string,
) ([]string, error) {
	rows, err := tx.Query(ctx, phoneNamesQuery, practiceID, locationIDs, phone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
