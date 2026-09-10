package workspace

import (
	"context"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
	"strings"
)

func taskListSQL(command QueryTasksCommand) string {
	query := taskQuerySQL(command.State, command.Ordering)
	if !command.Grouped || command.State != work.TaskOpen {
		return query
	}
	order := "created_at,id"
	if command.Ordering == work.TaskOrderingRecent {
		order = "updated_at DESC,id DESC"
	}
	if command.Ordering == work.TaskOrderingPriority {
		order = "CASE urgency WHEN 'high_priority' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END,created_at,id"
	}
	// Apply filters before choosing the representative; expand complete membership
	// separately in the same snapshot, so search never hides a resolution target.
	matching := "SELECT task.* " + taskQuerySelect[strings.Index(taskQuerySelect, "FROM work_tasks task"):] + " AND task.state='OPEN'"
	prefix := `WITH matching AS (` + matching + `), ranked AS (
 SELECT *,row_number() OVER(PARTITION BY practice_id,location_id,phone,category ORDER BY ` + order + `) AS member_rank FROM matching
 ), group_candidates AS (SELECT * FROM ranked WHERE member_rank=1) `
	return prefix + strings.Replace(query, "FROM work_tasks task", "FROM group_candidates task", 1)
}

func readGroupMembers(ctx context.Context, tx pgx.Tx, subject string, anchor work.Task) ([]work.Task, error) {
	query := strings.Replace(taskReadQuery, "WHERE task.id = $1", `WHERE task.practice_id=$1 AND task.location_id=$3 AND task.phone=$4 AND task.category IS NOT DISTINCT FROM $5::text AND task.state='OPEN' ORDER BY task.created_at,task.id`, 1)
	var category any
	if anchor.Category != "" {
		category = string(anchor.Category)
	}
	rows, err := tx.Query(ctx, query, anchor.PracticeID, subject, anchor.LocationID, anchor.Phone, category)
	if err != nil {
		return nil, err
	}
	members := []work.Task{}
	for rows.Next() {
		task, err := scanTaskProjection(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		members = append(members, task)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range members {
		if err := loadTaskInteractions(ctx, tx, &members[i]); err != nil {
			return nil, err
		}
		members[i].RelatedInteractionCount = len(members[i].Interactions)
	}
	return members, nil
}
