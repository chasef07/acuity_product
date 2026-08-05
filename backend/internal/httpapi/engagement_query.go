package httpapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errEngagementDenied       = errors.New("engagement access denied")
	errEngagementInvalidInput = errors.New("invalid engagement input")
)

type engagementLocation struct {
	ID   string
	Name string
}

type engagementSummary struct {
	Phone              string
	DisplayName        string
	Locations          []engagementLocation
	LatestActivity     time.Time
	OpenTaskCount      int
	Unread             bool
	TextNeedsAttention bool
}

type engagementPage struct{ Items []engagementSummary }

type engagementQueryCommand struct {
	Identity   access.Identity
	PracticeID string
	Phone      string
	Limit      int
}

type engagementQueryAdapter struct {
	pool   *pgxpool.Pool
	access *access.Module
}

// This application read adapter owns no domain state. It composes the
// authorized number-inbox projection across the five owning modules' tables.

func newEngagementQueryAdapter(pool *pgxpool.Pool, accessModule *access.Module) *engagementQueryAdapter {
	return &engagementQueryAdapter{pool: pool, access: accessModule}
}

func (m *engagementQueryAdapter) query(ctx context.Context, command engagementQueryCommand) (engagementPage, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.Phone = strings.TrimSpace(command.Phone)
	limit := command.Limit
	if limit == 0 {
		limit = 7
	}
	if m.pool == nil || m.access == nil || command.PracticeID == "" || limit < 1 || limit > 10 {
		return engagementPage{}, errEngagementInvalidInput
	}
	phone := ""
	if command.Phone != "" {
		var err error
		phone, err = messaging.NormalizePhone(command.Phone)
		if err != nil {
			return engagementPage{}, errEngagementInvalidInput
		}
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return engagementPage{}, fmt.Errorf("begin Engagement lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockReadAuthorization(ctx, tx, command.Identity, command.PracticeID, "")
	if err != nil {
		return engagementPage{}, errEngagementDenied
	}
	locationIDs := make([]string, 0, len(authorization.Locations))
	for _, location := range authorization.Locations {
		locationIDs = append(locationIDs, location.ID)
	}
	if len(locationIDs) == 0 {
		return engagementPage{}, errEngagementDenied
	}
	phones := []string{phone}
	if phone == "" {
		rows, queryErr := tx.Query(ctx, `
			WITH evidence AS (
				SELECT external_phone AS phone, updated_at AS occurred_at
				FROM messaging_threads
				WHERE practice_id = $1 AND location_id::text = ANY($2::text[])
				UNION ALL
				SELECT COALESCE(handoff.phone, call.destination_phone), call.updated_at
				FROM human_calling_calls call
				LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.handoff_id
				WHERE call.practice_id = $1 AND call.location_id::text = ANY($2::text[])
				UNION ALL
				SELECT phone, updated_at FROM work_tasks
				WHERE practice_id = $1 AND location_id::text = ANY($2::text[])
				UNION ALL
				SELECT phone, created_at FROM work_staff_notes
				WHERE practice_id = $1 AND location_id::text = ANY($2::text[])
			)
			SELECT phone FROM evidence WHERE phone IS NOT NULL AND phone <> ''
			GROUP BY phone ORDER BY max(occurred_at) DESC, phone LIMIT $3
		`, command.PracticeID, locationIDs, limit)
		if queryErr != nil {
			return engagementPage{}, fmt.Errorf("query recent Engagement phones: %w", queryErr)
		}
		phones = phones[:0]
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				return engagementPage{}, fmt.Errorf("scan recent Engagement phone: %w", err)
			}
			phones = append(phones, value)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return engagementPage{}, fmt.Errorf("iterate recent Engagement phones: %w", err)
		}
		rows.Close()
	}
	items := make([]engagementSummary, 0, len(phones))
	for _, value := range phones {
		summary, found, queryErr := querySummary(ctx, tx, command, locationIDs, value)
		if queryErr != nil {
			return engagementPage{}, queryErr
		}
		if found {
			items = append(items, summary)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return engagementPage{}, fmt.Errorf("commit Engagement lookup: %w", err)
	}
	return engagementPage{Items: items}, nil
}

func querySummary(ctx context.Context, tx pgx.Tx, command engagementQueryCommand, locationIDs []string, phone string) (engagementSummary, bool, error) {
	var summary engagementSummary
	var found bool
	err := tx.QueryRow(ctx, `
		WITH evidence AS (
			SELECT location_id, updated_at AS occurred_at, COALESCE(display_name, '') AS display_name
			FROM messaging_threads WHERE practice_id = $1 AND location_id::text = ANY($2::text[]) AND external_phone = $3
			UNION ALL
			SELECT call.location_id, call.updated_at, COALESCE(handoff.display_name, '')
			FROM human_calling_calls call LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.handoff_id
			WHERE call.practice_id = $1 AND call.location_id::text = ANY($2::text[]) AND COALESCE(handoff.phone, call.destination_phone) = $3
			UNION ALL
			SELECT location_id, updated_at, COALESCE(caller_name, '') FROM work_tasks
			WHERE practice_id = $1 AND location_id::text = ANY($2::text[]) AND phone = $3
			UNION ALL
			SELECT location_id, created_at, '' FROM work_staff_notes
			WHERE practice_id = $1 AND location_id::text = ANY($2::text[]) AND phone = $3
		)
		SELECT $3,
			COALESCE((SELECT display_name FROM evidence WHERE display_name <> '' ORDER BY occurred_at DESC LIMIT 1), ''),
			COALESCE(max(occurred_at), '-infinity'::timestamptz),
			(SELECT count(*) FROM work_tasks WHERE practice_id = $1 AND location_id::text = ANY($2::text[]) AND phone = $3 AND state = 'OPEN'),
			EXISTS (SELECT 1 FROM messaging_threads thread JOIN messaging_thread_unreads unread ON unread.thread_id = thread.id WHERE thread.practice_id = $1 AND thread.location_id::text = ANY($2::text[]) AND thread.external_phone = $3 AND unread.user_subject = $4),
			EXISTS (
				SELECT 1 FROM messaging_threads thread
				WHERE thread.practice_id = $1 AND thread.location_id::text = ANY($2::text[]) AND thread.external_phone = $3
				AND EXISTS (
					SELECT 1 FROM messaging_messages inbound
					WHERE inbound.thread_id = thread.id AND inbound.direction = 'INBOUND'
					AND inbound.created_at > COALESCE((SELECT handled_through FROM messaging_thread_handled handled WHERE handled.thread_id = thread.id), '-infinity'::timestamptz)
				)
			),
			count(*) > 0
		FROM evidence
	`, command.PracticeID, locationIDs, phone, command.Identity.Subject).Scan(
		&summary.Phone, &summary.DisplayName, &summary.LatestActivity, &summary.OpenTaskCount,
		&summary.Unread, &summary.TextNeedsAttention, &found,
	)
	if err != nil {
		return engagementSummary{}, false, fmt.Errorf("query Engagement summary: %w", err)
	}
	if !found {
		return engagementSummary{}, false, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT location.id::text, location.name
		FROM access_locations location
		JOIN (
			SELECT location_id FROM messaging_threads WHERE practice_id = $1 AND external_phone = $3
			UNION SELECT call.location_id FROM human_calling_calls call LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.handoff_id WHERE call.practice_id = $1 AND COALESCE(handoff.phone, call.destination_phone) = $3
			UNION SELECT location_id FROM work_tasks WHERE practice_id = $1 AND phone = $3
			UNION SELECT location_id FROM work_staff_notes WHERE practice_id = $1 AND phone = $3
		) evidence ON evidence.location_id = location.id
		WHERE location.practice_id = $1 AND location.id::text = ANY($2::text[])
		ORDER BY location.name, location.id::text
	`, command.PracticeID, locationIDs, phone)
	if err != nil {
		return engagementSummary{}, false, fmt.Errorf("query Engagement Locations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var location engagementLocation
		if err := rows.Scan(&location.ID, &location.Name); err != nil {
			return engagementSummary{}, false, fmt.Errorf("scan Engagement engagementLocation: %w", err)
		}
		summary.Locations = append(summary.Locations, location)
	}
	if err := rows.Err(); err != nil {
		return engagementSummary{}, false, fmt.Errorf("iterate Engagement Locations: %w", err)
	}
	return summary, true, nil
}
