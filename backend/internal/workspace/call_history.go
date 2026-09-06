package workspace

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Select whole call histories before pagination. Source identity is scoped to
// the authorized Location; a shared phone number never establishes a link.
// The deterministic UUID is a display key only, never an authorization key.
const phoneHistoryRootsSQL = `
 WITH calls AS (
  SELECT call.id, call.location_id, handoff.source_call_id, call.created_at,
   md5(call.practice_id::text || ':' || call.location_id::text || ':' ||
    CASE WHEN handoff.source_call_id IS NOT NULL THEN 'source:' || handoff.source_call_id
     ELSE 'call:' || call.id::text END)::uuid AS root_id
  FROM human_calling_calls call
  LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id
  WHERE call.practice_id = $1 AND call.location_id = ANY($2::uuid[])
   AND COALESCE(handoff.phone, call.destination_phone) = $3
 ), ai AS (
  SELECT interaction.id, interaction.location_id, interaction.source_call_id,
   interaction.started_at,
   md5(interaction.practice_id::text || ':' || interaction.location_id::text ||
    ':source:' || interaction.source_call_id)::uuid AS root_id
  FROM ai_interactions interaction
  WHERE interaction.practice_id = $1 AND interaction.location_id = ANY($2::uuid[])
   AND interaction.phone = $3
 ), call_facts AS (
  SELECT root_id, 'CALL'::text AS kind, id, created_at AS occurred_at FROM calls
  UNION ALL
  SELECT root_id, 'AI_INTERACTION', id, started_at FROM ai
 ), roots AS (
  SELECT root_id, min(occurred_at) AS occurred_at FROM call_facts GROUP BY root_id
 ), tasks AS (
  SELECT task.id, task.location_id, task.source_call_id, task.call_id
  FROM work_tasks task
  WHERE task.practice_id = $1 AND task.location_id = ANY($2::uuid[]) AND task.phone = $3
 ), task_links AS (
  SELECT task.id AS task_id, ai.root_id FROM tasks task JOIN ai
   ON ai.location_id = task.location_id AND ai.source_call_id = task.source_call_id
  UNION
  SELECT task.id, call.root_id FROM tasks task JOIN calls call
   ON call.location_id = task.location_id AND
    (call.id = task.call_id OR call.source_call_id = task.source_call_id)
  UNION
  SELECT task.id, call.root_id FROM tasks task
   JOIN work_task_interactions attached ON attached.task_id = task.id
   JOIN calls call ON call.id = attached.call_id AND call.location_id = task.location_id
 ), facts AS (
  SELECT 'CALL_HISTORY'::text AS kind, fact.root_id AS id, root.occurred_at,
   fact.kind AS member_kind, fact.id AS member_id
  FROM call_facts fact JOIN roots root USING (root_id)
  UNION ALL
  SELECT 'CALL_HISTORY', link.root_id, root.occurred_at, 'TASK', activity.id
  FROM task_links link JOIN roots root USING (root_id)
   JOIN work_task_activities activity ON activity.task_id = link.task_id AND activity.kind = 'TASK_CREATED'
  UNION ALL
  SELECT 'TASK', activity.id, activity.occurred_at, 'TASK', activity.id
  FROM tasks task JOIN work_task_activities activity ON activity.task_id = task.id
  WHERE activity.kind NOT IN ('TASK_CREATED', 'INTERACTION_ATTACHED')
   OR NOT EXISTS (SELECT 1 FROM task_links link WHERE link.task_id = task.id)
  UNION ALL
  SELECT 'MESSAGE', message.id, message.created_at, 'MESSAGE', message.id
  FROM messaging_messages message JOIN messaging_threads thread ON thread.id = message.thread_id
  WHERE thread.practice_id = $1 AND thread.location_id = ANY($2::uuid[]) AND thread.external_phone = $3
 )
 SELECT kind, id::text, occurred_at, array_agg(member_kind || ':' || member_id::text ORDER BY member_kind, member_id)
 FROM facts
 WHERE $4::timestamptz IS NULL OR occurred_at < $4
  OR (occurred_at = $4 AND kind || ':' || id::text < $5)
 GROUP BY kind, id, occurred_at
 ORDER BY occurred_at DESC, kind || ':' || id::text DESC
 LIMIT $6`

func queryPhoneHistory(ctx context.Context, tx pgx.Tx, practiceID string, locationIDs []string,
	phone string, cursor *pageCursor, limit int,
) (TimelinePage, error) {
	rows, err := tx.Query(ctx, phoneHistoryRootsSQL, practiceID, locationIDs, phone,
		nullableCursorTime(cursor), nullableCursorID(cursor), limit+1)
	if err != nil {
		return TimelinePage{}, fmt.Errorf("query call history roots: %w", err)
	}
	var roots []TimelineItem
	var members [][]string
	selected := map[string][]string{}
	seen := map[string]bool{}
	for rows.Next() {
		var root TimelineItem
		var keys []string
		if err := rows.Scan(&root.Type, &root.ID, &root.OccurredAt, &keys); err != nil {
			rows.Close()
			return TimelinePage{}, err
		}
		roots = append(roots, root)
		members = append(members, keys)
		for _, key := range keys {
			if seen[key] {
				continue
			}
			seen[key] = true
			kind, id, _ := strings.Cut(key, ":")
			selected[kind] = append(selected[kind], id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return TimelinePage{}, err
	}
	evidence := map[string]TimelineItem{}
	for _, kind := range []string{"MESSAGE", "CALL", "AI_INTERACTION", "TASK"} {
		ids := selected[kind]
		if len(ids) == 0 {
			continue
		}
		var items []TimelineItem
		switch kind {
		case "MESSAGE":
			items, err = queryTimelineMessages(ctx, tx, practiceID, locationIDs, phone, "", nil, len(ids), true, ids)
		case "CALL":
			items, err = queryTimelineCalls(ctx, tx, practiceID, locationIDs, phone, nil, len(ids), true, ids)
		case "AI_INTERACTION":
			items, err = queryPhoneInteractions(ctx, tx, practiceID, locationIDs, phone, nil, len(ids), ids)
		case "TASK":
			items, err = queryPhoneTaskActivities(ctx, tx, practiceID, locationIDs, phone, nil, len(ids), ids)
		}
		if err != nil {
			return TimelinePage{}, err
		}
		for _, item := range items {
			evidence[timelineItemKey(item)] = item
		}
	}
	for index := range roots {
		for _, key := range members[index] {
			item, ok := evidence[key]
			if !ok {
				return TimelinePage{}, fmt.Errorf("call history evidence unavailable")
			}
			if roots[index].Type == "CALL_HISTORY" {
				roots[index].Entries = append(roots[index].Entries, item)
			} else {
				roots[index] = item
			}
		}
		sort.SliceStable(roots[index].Entries, func(i, j int) bool {
			return roots[index].Entries[i].OccurredAt.Before(roots[index].Entries[j].OccurredAt)
		})
	}
	return paginateTimeline(roots, limit, true), nil
}
