package work

import (
	"context"
	"encoding/json"
	"fmt"
)

func (m *Module) CallClassificationTasks(ctx context.Context, practiceID, serviceSubject, sourceCallID string) (json.RawMessage, error) {
	var tasks json.RawMessage
	err := m.database.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('title',title,'message',source_message,
 'state',state,'createdAt',created_at)),'[]'::jsonb) FROM work_tasks WHERE practice_id=$1 AND source_call_id=$3
 AND created_by_subject=$2 AND origin='ABITA_AI'`, practiceID, serviceSubject, sourceCallID).Scan(&tasks)
	if err != nil {
		return nil, fmt.Errorf("load saved call tasks: %w", err)
	}
	return tasks, nil
}
