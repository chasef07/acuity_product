// Package engagement owns the authorized, cross-module read projection for a
// phone-led inbox. It deliberately contains no workflow state.
package engagement

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrDenied       = errors.New("engagement access denied")
	ErrInvalidInput = errors.New("invalid engagement input")
)

type Location struct {
	ID   string
	Name string
}

type Summary struct {
	Phone              string
	DisplayName        string
	Locations          []Location
	LatestActivity     time.Time
	OpenTaskCount      int
	Unread             bool
	TextNeedsAttention bool
}

type Page struct{ Items []Summary }

type QueryCommand struct {
	Identity   access.Identity
	PracticeID string
	Phone      string
	Limit      int
}

type Module struct {
	pool   *pgxpool.Pool
	access *access.Module
}

func New(pool *pgxpool.Pool, accessModule *access.Module) *Module {
	return &Module{pool: pool, access: accessModule}
}

func (m *Module) Query(ctx context.Context, command QueryCommand) (Page, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.Phone = strings.TrimSpace(command.Phone)
	limit := command.Limit
	if limit == 0 {
		limit = 7
	}
	if m.pool == nil || m.access == nil || command.PracticeID == "" || limit < 1 || limit > 10 {
		return Page{}, ErrInvalidInput
	}
	phone := ""
	if command.Phone != "" {
		var err error
		phone, err = normalizePhone(command.Phone)
		if err != nil {
			return Page{}, ErrInvalidInput
		}
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Page{}, fmt.Errorf("begin Engagement lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockReadAuthorization(ctx, tx, command.Identity, command.PracticeID, "")
	if err != nil {
		return Page{}, ErrDenied
	}
	locationIDs := make([]string, 0, len(authorization.Locations))
	for _, location := range authorization.Locations {
		locationIDs = append(locationIDs, location.ID)
	}
	if len(locationIDs) == 0 {
		return Page{}, ErrDenied
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
			)
			SELECT phone FROM evidence WHERE phone IS NOT NULL AND phone <> ''
			GROUP BY phone ORDER BY max(occurred_at) DESC, phone LIMIT $3
		`, command.PracticeID, locationIDs, limit)
		if queryErr != nil {
			return Page{}, fmt.Errorf("query recent Engagement phones: %w", queryErr)
		}
		phones = phones[:0]
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				return Page{}, fmt.Errorf("scan recent Engagement phone: %w", err)
			}
			phones = append(phones, value)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return Page{}, fmt.Errorf("iterate recent Engagement phones: %w", err)
		}
		rows.Close()
	}
	items := make([]Summary, 0, len(phones))
	for _, value := range phones {
		summary, found, queryErr := querySummary(ctx, tx, command, locationIDs, value)
		if queryErr != nil {
			return Page{}, queryErr
		}
		if found {
			items = append(items, summary)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Page{}, fmt.Errorf("commit Engagement lookup: %w", err)
	}
	return Page{Items: items}, nil
}

func querySummary(ctx context.Context, tx pgx.Tx, command QueryCommand, locationIDs []string, phone string) (Summary, bool, error) {
	var summary Summary
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
		return Summary{}, false, fmt.Errorf("query Engagement summary: %w", err)
	}
	if !found {
		return Summary{}, false, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT location.id::text, location.name
		FROM access_locations location
		JOIN (
			SELECT location_id FROM messaging_threads WHERE practice_id = $1 AND external_phone = $3
			UNION SELECT call.location_id FROM human_calling_calls call LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.handoff_id WHERE call.practice_id = $1 AND COALESCE(handoff.phone, call.destination_phone) = $3
			UNION SELECT location_id FROM work_tasks WHERE practice_id = $1 AND phone = $3
		) evidence ON evidence.location_id = location.id
		WHERE location.practice_id = $1 AND location.id::text = ANY($2::text[])
		ORDER BY location.name, location.id::text
	`, command.PracticeID, locationIDs, phone)
	if err != nil {
		return Summary{}, false, fmt.Errorf("query Engagement Locations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var location Location
		if err := rows.Scan(&location.ID, &location.Name); err != nil {
			return Summary{}, false, fmt.Errorf("scan Engagement Location: %w", err)
		}
		summary.Locations = append(summary.Locations, location)
	}
	if err := rows.Err(); err != nil {
		return Summary{}, false, fmt.Errorf("iterate Engagement Locations: %w", err)
	}
	return summary, true, nil
}

func normalizePhone(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "+") {
		for _, character := range value[1:] {
			if character < '0' || character > '9' {
				return "", ErrInvalidInput
			}
		}
		if len(value) >= 9 && len(value) <= 16 && value[1] != '0' {
			return value, nil
		}
	}
	digits := make([]rune, 0, len(value))
	for _, character := range value {
		if character >= '0' && character <= '9' {
			digits = append(digits, character)
		}
	}
	if len(digits) == 10 {
		return "+1" + string(digits), nil
	}
	if len(digits) == 11 && digits[0] == '1' {
		return "+" + string(digits), nil
	}
	return "", ErrInvalidInput
}
