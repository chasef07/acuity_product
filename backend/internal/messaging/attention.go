package messaging

import (
	"context"
	"fmt"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

// The same eligible set owns sidebar rows, totals, and bulk clearing. History
// queries deliberately do not use this window. Outbound activity never extends it.
const recentTextThreads = `
 SELECT thread.*, location.name AS location_name,
        inbound.occurred_at AS latest_activity, inbound.body AS preview,
        inbound.delivery_state AS latest_delivery, unread.latest_message_id AS unread_message_id
 FROM messaging_threads thread
 JOIN access_locations location ON location.id = thread.location_id
 JOIN messaging_thread_unreads unread
   ON unread.thread_id = thread.id AND unread.user_subject = $3
 JOIN LATERAL (
   SELECT message.id, message.created_at AS occurred_at, message.body, message.delivery_state
   FROM messaging_messages message
   WHERE message.thread_id = thread.id AND message.direction = 'INBOUND'
   ORDER BY message.created_at DESC, message.id DESC LIMIT 1
 ) inbound ON true
 WHERE thread.practice_id = $1 AND thread.location_id::text = ANY($2::text[])
   AND inbound.occurred_at >= $4 AND inbound.occurred_at <= $5
   AND NOT EXISTS (
     SELECT 1 FROM work_tasks task
     WHERE task.message_thread_id = thread.id AND task.state = 'OPEN'
   )`

func (m *Module) queryRecentThreads(ctx context.Context, command QueryThreadsCommand) (ThreadPage, error) {
	if m.database == nil || m.access == nil || command.PracticeID == "" || command.Search != "" {
		return ThreadPage{}, ErrInvalidInput
	}
	cursor, err := decodePageCursor(command.Cursor)
	if err != nil {
		return ThreadPage{}, ErrInvalidInput
	}
	limit := command.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 50 {
		return ThreadPage{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return ThreadPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockReadAuthorization(ctx, tx, command.Identity, command.PracticeID, command.LocationID)
	if err != nil {
		return ThreadPage{}, ErrDenied
	}
	locations := attentionLocationIDs(authorization)
	now := m.now()
	args := []any{command.PracticeID, locations, command.Identity.Subject, now.Add(-7 * 24 * time.Hour), now}
	total := 0
	if err := tx.QueryRow(ctx, `WITH eligible AS (`+recentTextThreads+`) SELECT count(DISTINCT external_phone) FROM eligible`, args...).Scan(&total); err != nil {
		return ThreadPage{}, fmt.Errorf("count recent Texts: %w", err)
	}
	args = append(args, nullableCursorTime(cursor), nullableCursorID(cursor), limit+1)
	rows, err := tx.Query(ctx, `WITH eligible AS (`+recentTextThreads+`),
 heads AS (
   SELECT DISTINCT ON (external_phone) external_phone, id, latest_activity
   FROM eligible ORDER BY external_phone, latest_activity DESC, id DESC
 ), page AS (
   SELECT * FROM heads WHERE $6::timestamptz IS NULL
     OR (latest_activity, id) < ($6::timestamptz, $7::uuid)
   ORDER BY latest_activity DESC, id DESC LIMIT $8
 )
 SELECT thread.id::text, thread.practice_id::text, thread.location_id::text,
   thread.location_name, thread.office_phone, thread.external_phone,
   COALESCE(thread.display_name, ''), COALESCE(thread.name_source, ''),
   thread.outbound_blocked, thread.created_at, thread.updated_at,
   COALESCE(NULLIF(thread.preview, ''), 'Attachment'), thread.latest_delivery,
   thread.latest_activity, page.id::text, page.latest_activity
 FROM page JOIN eligible thread ON thread.external_phone = page.external_phone
 ORDER BY page.latest_activity DESC, page.id DESC, thread.latest_activity DESC, thread.id DESC`, args...)
	if err != nil {
		return ThreadPage{}, fmt.Errorf("query recent Texts: %w", err)
	}
	page := ThreadPage{Items: []ThreadSummary{}, Total: &total}
	groups := 0
	priorHead := ""
	var lastHead pageCursor
	for rows.Next() {
		item := ThreadSummary{Unread: true, LatestDirection: DirectionInbound}
		var head pageCursor
		if err := rows.Scan(&item.ID, &item.PracticeID, &item.LocationID, &item.LocationName,
			&item.OfficePhone, &item.ExternalPhone, &item.DisplayName, &item.NameSource,
			&item.OutboundBlocked, &item.CreatedAt, &item.UpdatedAt, &item.Preview,
			&item.LatestDelivery, &item.LatestActivity, &head.ID, &head.OccurredAt); err != nil {
			rows.Close()
			return ThreadPage{}, err
		}
		if head.ID != priorHead {
			groups++
			priorHead = head.ID
		}
		if groups > limit {
			page.NextCursor = encodePageCursor(lastHead)
			continue
		}
		lastHead = head
		page.Items = append(page.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ThreadPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ThreadPage{}, err
	}
	return page, nil
}

func attentionLocationIDs(authorization access.Authorization) []string {
	if authorization.ActiveLocation != nil {
		return []string{authorization.ActiveLocation.ID}
	}
	ids := make([]string, 0, len(authorization.Locations))
	for _, location := range authorization.Locations {
		ids = append(ids, location.ID)
	}
	return ids
}

// MarkRecentRead clears only this User's eligible recent attention. It never
// completes Tasks or deletes messages, and includes conversations beyond page one.
func (m *Module) MarkRecentRead(ctx context.Context, identity access.Identity, practiceID, locationID string) error {
	if m.database == nil || m.access == nil {
		return ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockReadAuthorization(ctx, tx, identity, practiceID, locationID)
	if err != nil {
		return ErrDenied
	}
	now := m.now()
	changed := false
	for _, id := range attentionLocationIDs(authorization) {
		mutation, err := m.access.LockMutationAuthorization(ctx, tx, identity, practiceID, id)
		if err != nil {
			return ErrDenied
		}
		tag, err := tx.Exec(ctx, `WITH eligible AS (`+recentTextThreads+`)
     DELETE FROM messaging_thread_unreads unread USING eligible
     WHERE unread.thread_id = eligible.id AND unread.user_subject = $3
       AND unread.latest_message_id = eligible.unread_message_id`,
			practiceID, []string{id}, identity.Subject, now.Add(-7*24*time.Hour), now)
		if err != nil {
			return fmt.Errorf("clear recent Texts: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		changed = true
		if err := m.access.AuditOperatorMutation(ctx, tx, mutation, access.OperatorMutationAudit{
			Action: "message_threads.read_recent", ResourceType: "location", ResourceID: id,
			ResourceVersion: 1, OccurredAt: now,
		}); err != nil {
			return err
		}
	}
	if changed {
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
