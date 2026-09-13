package workspace

import (
	"context"

	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
)

// Expand all authorized page representatives in one query. Group rows carry the
// same summary as ordinary Task rows; ReadTask loads Interaction detail on demand.
func loadGroupMembers(ctx context.Context, tx pgx.Tx, subject string, groups []work.Task) error {
	if len(groups) == 0 {
		return nil
	}
	ids := make([]string, 0, len(groups))
	byID := make(map[string]int, len(groups))
	for i := range groups {
		ids = append(ids, groups[i].ID)
		byID[groups[i].ID] = i
		groups[i].GroupMembers = []work.Task{}
	}
	rows, err := tx.Query(ctx, `SELECT anchor.id::text,`+taskReadColumns+`
 FROM work_tasks task`+taskProjectionJoins+`
 JOIN work_tasks anchor ON anchor.practice_id=task.practice_id
 AND anchor.location_id=task.location_id AND anchor.phone=task.phone
 AND anchor.category IS NOT DISTINCT FROM task.category
 AND (anchor.origin IN ('MISSED_CALL_RECOVERY','VOICEMAIL_RECOVERY'))=(task.origin IN ('MISSED_CALL_RECOVERY','VOICEMAIL_RECOVERY'))
 WHERE anchor.id=ANY($1::uuid[]) AND task.state='OPEN'
 ORDER BY task.created_at,task.id`, ids, subject)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var anchorID string
		member, err := scanTaskProjection(rows, &anchorID)
		if err != nil {
			return err
		}
		i := byID[anchorID]
		groups[i].GroupMembers = append(groups[i].GroupMembers, member)
	}
	return rows.Err()
}
