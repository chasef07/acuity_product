package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/contactcontext"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
)

type PatientWorkFilter string

const (
	PatientWorkAll          PatientWorkFilter = "all"
	PatientWorkFollowUp     PatientWorkFilter = "followUp"
	PatientWorkCallBack     PatientWorkFilter = "callBack"
	PatientWorkTexts        PatientWorkFilter = "texts"
	PatientWorkAppointments PatientWorkFilter = "appointments"
)

var patientWorkFilterSQL = map[PatientWorkFilter]string{
	PatientWorkAll:          `true`,
	PatientWorkFollowUp:     `item.kind_rank = 3`,
	PatientWorkCallBack:     `item.kind_rank = 0`,
	PatientWorkTexts:        `item.kind_rank = 1`,
	PatientWorkAppointments: `item.kind_rank = 2`,
}

type QueryPatientWorkCommand struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Search     string
	Filter     PatientWorkFilter
	Cursor     string
	Limit      int
}

type PatientWorkRow struct {
	Phone        string
	DisplayName  string
	OpenCount    int
	HighPriority bool
	ActivityAt   time.Time
	WaitingSince time.Time
	Top          work.Task
}

type PatientWorkCounts struct {
	All          int
	FollowUp     int
	CallBack     int
	Texts        int
	Appointments int
}

type PatientWorkTotals struct {
	OpenTasks int
	Patients  int
}

type PatientWorkPage struct {
	Rows       []PatientWorkRow
	Counts     PatientWorkCounts
	Totals     PatientWorkTotals
	NextCursor string
}

func (m *Module) QueryPatientWork(
	ctx context.Context,
	command QueryPatientWorkCommand,
) (PatientWorkPage, error) {
	command.Search = strings.TrimSpace(command.Search)
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.LocationID = strings.TrimSpace(command.LocationID)
	if command.Filter == "" {
		command.Filter = PatientWorkAll
	}
	limit := command.Limit
	if limit == 0 {
		limit = 50
	}
	_, knownFilter := patientWorkFilterSQL[command.Filter]
	if m.database == nil || m.access == nil || command.PracticeID == "" ||
		len(command.Search) > 500 || !knownFilter ||
		limit < 1 || limit > 50 {
		return PatientWorkPage{}, ErrInvalidInput
	}
	cursor, err := decodePatientWorkCursor(command.Cursor, command.Filter)
	if err != nil {
		return PatientWorkPage{}, ErrInvalidInput
	}

	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return PatientWorkPage{}, fmt.Errorf("begin patient work query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locationIDs, err := m.authorizedLocationIDs(
		ctx, tx, command.Identity, command.PracticeID, command.LocationID,
	)
	if err != nil {
		return PatientWorkPage{}, err
	}
	search, err := resolveTaskSearch(ctx, tx, command.PracticeID, locationIDs, command.Search)
	if err != nil {
		return PatientWorkPage{}, err
	}

	page := PatientWorkPage{Rows: make([]PatientWorkRow, 0, limit+1)}
	rows, err := tx.Query(ctx, patientWorkPageSQL(command.Filter, search), search.arguments(
		command.PracticeID, locationIDs, search.pattern, search.digits,
		cursor.Present, cursor.HighPriority, cursor.ActivityAt, cursor.Phone, limit+1,
	)...)
	if err != nil {
		return PatientWorkPage{}, fmt.Errorf("query patient work: %w", err)
	}
	for rows.Next() {
		var row PatientWorkRow
		var displayName *string
		row.Top, err = scanTaskProjection(rows,
			&row.Phone, &row.OpenCount, &row.HighPriority,
			&row.ActivityAt, &row.WaitingSince, &displayName,
		)
		if err != nil {
			rows.Close()
			return PatientWorkPage{}, fmt.Errorf("scan patient work: %w", err)
		}
		if displayName != nil {
			row.DisplayName = *displayName
		}
		page.Rows = append(page.Rows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PatientWorkPage{}, fmt.Errorf("iterate patient work: %w", err)
	}
	rows.Close()

	if err := tx.QueryRow(ctx, patientWorkSummarySQL(search), search.arguments(
		command.PracticeID, locationIDs, search.pattern, search.digits,
	)...).Scan(
		&page.Counts.All, &page.Counts.FollowUp, &page.Counts.CallBack, &page.Counts.Texts,
		&page.Counts.Appointments, &page.Totals.OpenTasks, &page.Totals.Patients,
	); err != nil {
		return PatientWorkPage{}, fmt.Errorf("count patient work: %w", err)
	}

	if len(page.Rows) > limit {
		page.Rows = page.Rows[:limit]
		last := page.Rows[limit-1]
		page.NextCursor, err = encodePatientWorkCursor(patientWorkCursor{
			Filter:       command.Filter,
			HighPriority: last.HighPriority,
			ActivityAt:   last.ActivityAt,
			Phone:        last.Phone,
		})
		if err != nil {
			return PatientWorkPage{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return PatientWorkPage{}, fmt.Errorf("commit patient work query: %w", err)
	}
	return page, nil
}

func patientDisplayNameSQL(phone string) string {
	return `(
		SELECT CASE WHEN count(DISTINCT lower(seen.name)) = 1
			THEN (array_agg(seen.name ORDER BY seen.seen_at DESC))[1]
		END
		FROM (` + phoneNamesSeen(phone) + `) seen
		WHERE seen.name <> ''
	)`
}

func patientWorkSearched(search taskSearch) string {
	if search.pattern == "" {
		return `bool_or(item.searched)`
	}
	return `(bool_or(item.searched) OR lower(` + patientDisplayNameSQL("item.phone") + `) LIKE lower($3))`
}

func patientWorkSource(filter PatientWorkFilter, search taskSearch, searchLocations string) string {
	hit := patientWorkFilterSQL[filter]
	return `WITH item AS (
	SELECT
		task.id,
		task.phone,
		task.urgency = 'high_priority' AS high_priority,
		CASE
			WHEN ` + work.TaskIsCallRecoverySQL + ` THEN 0
			WHEN ` + work.TaskIsTextReviewSQL + ` THEN 1
			WHEN ` + work.TaskIsAppointmentReviewSQL + ` THEN 2
			ELSE 3
		END AS kind_rank,
		CASE WHEN ` + work.TaskIsTextReviewSQL + ` THEN task.updated_at ELSE task.created_at END AS activity_at,
		task.created_at,
		(true` + search.filter(searchLocations) + `) AS searched
	FROM work_tasks task
	WHERE task.practice_id = $1
		AND task.location_id = ANY($2::uuid[])
		AND task.state = 'OPEN'
), patient AS (
	SELECT
		item.phone,
		count(*) AS open_count,
		` + patientWorkSearched(search) + ` AS searched,
		bool_or(item.kind_rank = 3) AS in_follow_up,
		bool_or(item.kind_rank = 0) AS in_call_back,
		bool_or(item.kind_rank = 1) AS in_texts,
		bool_or(item.kind_rank = 2) AS in_appointments,
		COALESCE(bool_or(item.high_priority) FILTER (WHERE ` + hit + `), false) AS high_priority,
		max(item.activity_at) FILTER (WHERE ` + hit + `) AS activity_at,
		min(item.created_at) FILTER (WHERE ` + hit + `) AS waiting_since
	FROM item
	GROUP BY item.phone
)`
}

func patientWorkPageSQL(filter PatientWorkFilter, search taskSearch) string {
	return patientWorkSource(filter, search, "$10") + `, page AS (
	SELECT patient.phone, patient.open_count, patient.high_priority, patient.activity_at, patient.waiting_since
	FROM patient
	WHERE patient.searched
		AND patient.activity_at IS NOT NULL
		AND (
			NOT $5
			OR (NOT patient.high_priority AND $6)
			OR (
				patient.high_priority = $6
				AND (patient.activity_at, patient.phone) < ($7, $8)
			)
		)
	ORDER BY patient.high_priority DESC, patient.activity_at DESC, patient.phone DESC
	LIMIT $9
)
SELECT
	page.phone,
	page.open_count,
	page.high_priority,
	page.activity_at,
	page.waiting_since,
	` + patientDisplayNameSQL("page.phone") + `,` + taskReadColumns + `
FROM page
JOIN LATERAL (
	SELECT item.id
	FROM item
	WHERE item.phone = page.phone
		AND ` + patientWorkFilterSQL[filter] + `
	ORDER BY item.high_priority DESC, item.kind_rank, item.activity_at DESC, item.id
	LIMIT 1
) top ON true
JOIN work_tasks task ON task.id = top.id` + taskProjectionJoins + `
ORDER BY page.high_priority DESC, page.activity_at DESC, page.phone DESC`
}

func patientWorkSummarySQL(search taskSearch) string {
	return patientWorkSource(PatientWorkAll, search, "$5") + `
SELECT
	count(*) FILTER (WHERE patient.searched),
	count(*) FILTER (WHERE patient.searched AND patient.in_follow_up),
	count(*) FILTER (WHERE patient.searched AND patient.in_call_back),
	count(*) FILTER (WHERE patient.searched AND patient.in_texts),
	count(*) FILTER (WHERE patient.searched AND patient.in_appointments),
	COALESCE(sum(patient.open_count), 0),
	count(*)
FROM patient`
}

type patientWorkCursor struct {
	Present      bool              `json:"-"`
	Filter       PatientWorkFilter `json:"filter"`
	HighPriority bool              `json:"highPriority"`
	ActivityAt   time.Time         `json:"activityAt"`
	Phone        string            `json:"phone"`
}

func encodePatientWorkCursor(cursor patientWorkCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode patient work cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodePatientWorkCursor(raw string, filter PatientWorkFilter) (patientWorkCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return patientWorkCursor{ActivityAt: time.Unix(0, 0).UTC()}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return patientWorkCursor{}, err
	}
	var cursor patientWorkCursor
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.ActivityAt.IsZero() ||
		cursor.Filter != filter ||
		!contactcontext.IsCanonicalPhone(cursor.Phone) {
		return patientWorkCursor{}, ErrInvalidInput
	}
	cursor.Present = true
	return cursor, nil
}
