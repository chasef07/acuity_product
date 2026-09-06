// Package workspace owns the authorized cross-domain read projections shown in
// the Acuity Portal workspace. Domain modules continue to own their writes.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
)

var (
	ErrDenied       = errors.New("workspace read access denied")
	ErrInvalidInput = errors.New("invalid workspace read input")
)

type Module struct {
	database productpostgres.Database
	access   *access.Module
}

func New(database productpostgres.Database, accessModule *access.Module) *Module {
	return &Module{database: database, access: accessModule}
}

type EngagementLocation struct {
	ID   string
	Name string
}

type EngagementSummary struct {
	Phone          string
	DisplayName    string
	Locations      []EngagementLocation
	LatestActivity time.Time
	OpenTaskCount  int
	Unread         bool
}

type QueryEngagementsCommand struct {
	Identity   access.Identity
	PracticeID string
	Phone      string
}

type EngagementPage struct {
	Items []EngagementSummary
}

type QueryTimelineCommand struct {
	Identity access.Identity
	ThreadID string
	Cursor   string
	Limit    int
}

type QueryPhoneTimelineCommand struct {
	Ungrouped  bool
	Identity   access.Identity
	PracticeID string
	Phone      string
	Cursor     string
	Limit      int
}

type TimelineItem struct {
	Entries       []TimelineItem
	Type          string
	ID            string
	OccurredAt    time.Time
	TaskActivity  string
	Message       messaging.Message
	Call          humancalling.CallHistoryItem
	AIInteraction interaction.OutcomeItem
	Task          work.Task
}

type TimelinePage struct {
	Items      []TimelineItem
	NextCursor string
}

type QueryTasksCommand struct {
	IncludeCounts *bool
	Identity      access.Identity
	PracticeID    string
	LocationID    string
	Search        string
	State         work.TaskState
	Ordering      work.TaskOrdering
	Folder        work.TaskFolder
	Cursor        string
	Limit         int
}

func (m *Module) QueryEngagements(
	ctx context.Context,
	command QueryEngagementsCommand,
) (EngagementPage, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	phone, err := messaging.NormalizePhone(command.Phone)
	if m.database == nil || m.access == nil || command.PracticeID == "" || err != nil {
		return EngagementPage{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EngagementPage{}, fmt.Errorf("begin Engagement lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locationIDs, err := m.authorizedLocationIDs(
		ctx, tx, command.Identity, command.PracticeID, "",
	)
	if err != nil {
		return EngagementPage{}, err
	}

	var summary EngagementSummary
	var found bool
	err = tx.QueryRow(ctx, `
		WITH evidence AS (
			SELECT
				thread.location_id,
				thread.updated_at AS occurred_at,
				COALESCE(thread.display_name, '') AS display_name
			FROM messaging_threads thread
			WHERE thread.practice_id = $1
				AND thread.location_id = ANY($2::uuid[])
				AND thread.external_phone = $3
			UNION ALL
			SELECT
				call.location_id,
				call.updated_at,
				COALESCE(handoff.display_name, '')
			FROM human_calling_calls call
			LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id
			WHERE call.practice_id = $1
				AND call.location_id = ANY($2::uuid[])
				AND COALESCE(handoff.phone, call.destination_phone) = $3
			UNION ALL
			SELECT
				task.location_id,
				task.updated_at,
				COALESCE(task.caller_name, '')
			FROM work_tasks task
			WHERE task.practice_id = $1
				AND task.location_id = ANY($2::uuid[])
				AND task.phone = $3
			UNION ALL
			SELECT
				interaction.location_id,
				interaction.started_at,
				''
			FROM ai_interactions interaction
			WHERE interaction.practice_id = $1
				AND interaction.location_id = ANY($2::uuid[])
				AND interaction.phone = $3
		)
		SELECT
			$3,
			COALESCE((
				SELECT display_name
				FROM evidence
				WHERE display_name <> ''
				ORDER BY occurred_at DESC
				LIMIT 1
			), ''),
			COALESCE(max(occurred_at), '-infinity'::timestamptz),
			(
				SELECT count(*)
				FROM work_tasks task
				WHERE task.practice_id = $1
					AND task.location_id = ANY($2::uuid[])
					AND task.phone = $3
					AND task.state = 'OPEN'
			),
			EXISTS (
				SELECT 1
				FROM messaging_threads thread
				JOIN messaging_thread_unreads unread ON unread.thread_id = thread.id
				WHERE thread.practice_id = $1
					AND thread.location_id = ANY($2::uuid[])
					AND thread.external_phone = $3
					AND unread.user_subject = $4
			),
			count(*) > 0
		FROM evidence
	`, command.PracticeID, locationIDs, phone, command.Identity.Subject).Scan(
		&summary.Phone,
		&summary.DisplayName,
		&summary.LatestActivity,
		&summary.OpenTaskCount,
		&summary.Unread,
		&found,
	)
	if err != nil {
		return EngagementPage{}, fmt.Errorf("query Engagement summary: %w", err)
	}
	if !found {
		if err := tx.Commit(ctx); err != nil {
			return EngagementPage{}, fmt.Errorf("commit empty Engagement lookup: %w", err)
		}
		return EngagementPage{Items: []EngagementSummary{}}, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT location.id::text, location.name
		FROM access_locations location
		JOIN (
			SELECT location_id
			FROM messaging_threads
			WHERE practice_id = $1 AND external_phone = $3
			UNION
			SELECT call.location_id
			FROM human_calling_calls call
			LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id
			WHERE call.practice_id = $1
				AND COALESCE(handoff.phone, call.destination_phone) = $3
			UNION
			SELECT location_id
			FROM work_tasks
			WHERE practice_id = $1 AND phone = $3
			UNION
			SELECT location_id
			FROM ai_interactions
			WHERE practice_id = $1 AND phone = $3
		) evidence ON evidence.location_id = location.id
		WHERE location.practice_id = $1
			AND location.id::text = ANY($2::text[])
		ORDER BY location.name, location.id::text
	`, command.PracticeID, locationIDs, phone)
	if err != nil {
		return EngagementPage{}, fmt.Errorf("query Engagement Locations: %w", err)
	}
	for rows.Next() {
		var location EngagementLocation
		if err := rows.Scan(&location.ID, &location.Name); err != nil {
			rows.Close()
			return EngagementPage{}, fmt.Errorf("scan Engagement Location: %w", err)
		}
		summary.Locations = append(summary.Locations, location)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return EngagementPage{}, fmt.Errorf("iterate Engagement Locations: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return EngagementPage{}, fmt.Errorf("commit Engagement lookup: %w", err)
	}
	return EngagementPage{Items: []EngagementSummary{summary}}, nil
}

func (m *Module) authorizedLocationIDs(
	ctx context.Context,
	tx pgx.Tx,
	identity access.Identity,
	practiceID string,
	locationID string,
) ([]string, error) {
	authorization, err := m.access.LockReadAuthorization(
		ctx, tx, identity, practiceID, locationID,
	)
	if err != nil {
		return nil, ErrDenied
	}
	if locationID != "" {
		return []string{locationID}, nil
	}
	locationIDs := make([]string, 0, len(authorization.Locations))
	for _, location := range authorization.Locations {
		locationIDs = append(locationIDs, location.ID)
	}
	if len(locationIDs) == 0 {
		return nil, ErrDenied
	}
	return locationIDs, nil
}
