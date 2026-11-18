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

type PatientWorkScope string

const (
	PatientWorkMine     PatientWorkScope = "mine"
	PatientWorkEveryone PatientWorkScope = "everyone"
)

type PatientWorkFilter string

const (
	PatientWorkAll          PatientWorkFilter = "all"
	PatientWorkFollowUp     PatientWorkFilter = "followUp"
	PatientWorkCallBack     PatientWorkFilter = "callBack"
	PatientWorkTexts        PatientWorkFilter = "texts"
	PatientWorkAppointments PatientWorkFilter = "appointments"
	PatientWorkUnowned      PatientWorkFilter = "unowned"
)

var patientWorkFilterSQL = map[PatientWorkFilter]string{
	PatientWorkAll:          `true`,
	PatientWorkFollowUp:     `item.kind_rank = 3`,
	PatientWorkCallBack:     `item.kind_rank = 0`,
	PatientWorkTexts:        `item.kind_rank = 1`,
	PatientWorkAppointments: `item.kind_rank = 2`,
	PatientWorkUnowned:      `item.unowned`,
}

var patientWorkScopeSQL = map[PatientWorkScope]string{
	PatientWorkMine:     `item.mine`,
	PatientWorkEveryone: `true`,
}

type QueryPatientWorkCommand struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Search     string
	Scope      PatientWorkScope
	Filter     PatientWorkFilter
	Cursor     string
	Limit      int
}

type PatientWorkRow struct {
	Phone        string
	DisplayName  string
	OpenCount    int
	HighPriority bool
	HasUnowned   bool
	ActivityAt   time.Time
	Top          work.Task
}

type PatientWorkCounts struct {
	All          int
	FollowUp     int
	CallBack     int
	Texts        int
	Appointments int
	Unowned      int
}

type PatientWorkTotals struct {
	OpenTasks            int
	Patients             int
	PatientsOutsideScope int
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
	if command.Scope == "" {
		command.Scope = PatientWorkMine
	}
	if command.Filter == "" {
		command.Filter = PatientWorkAll
	}
	limit := command.Limit
	if limit == 0 {
		limit = 50
	}
	_, knownScope := patientWorkScopeSQL[command.Scope]
	_, knownFilter := patientWorkFilterSQL[command.Filter]
	if m.database == nil || m.access == nil || command.PracticeID == "" ||
		len(command.Search) > 500 || !knownScope || !knownFilter ||
		limit < 1 || limit > 50 {
		return PatientWorkPage{}, ErrInvalidInput
	}
	cursor, err := decodePatientWorkCursor(command.Cursor, command.Scope, command.Filter)
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
	email := strings.ToLower(command.Identity.Email)

	page := PatientWorkPage{Rows: make([]PatientWorkRow, 0, limit+1)}
	rows, err := tx.Query(ctx, patientWorkPageSQL(command.Scope, command.Filter, search), search.arguments(
		command.PracticeID, locationIDs, search.pattern, search.digits, email,
		cursor.Present, cursor.HighPriority, cursor.ActivityAt, cursor.Phone, limit+1,
	)...)
	if err != nil {
		return PatientWorkPage{}, fmt.Errorf("query patient work: %w", err)
	}
	for rows.Next() {
		var row PatientWorkRow
		var displayName *string
		row.Top, err = scanTaskProjection(rows,
			&row.Phone, &row.OpenCount, &row.HighPriority, &row.HasUnowned,
			&row.ActivityAt, &displayName,
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

	if err := tx.QueryRow(ctx, patientWorkSummarySQL(command.Scope, search), search.arguments(
		command.PracticeID, locationIDs, search.pattern, search.digits, email,
	)...).Scan(
		&page.Counts.All, &page.Counts.FollowUp, &page.Counts.CallBack, &page.Counts.Texts,
		&page.Counts.Appointments, &page.Counts.Unowned,
		&page.Totals.OpenTasks, &page.Totals.Patients, &page.Totals.PatientsOutsideScope,
	); err != nil {
		return PatientWorkPage{}, fmt.Errorf("count patient work: %w", err)
	}

	if len(page.Rows) > limit {
		page.Rows = page.Rows[:limit]
		last := page.Rows[limit-1]
		page.NextCursor, err = encodePatientWorkCursor(patientWorkCursor{
			Scope:        command.Scope,
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

const responsibilityOwnerIsLive = `EXISTS (
		SELECT 1
		FROM access_memberships membership
		WHERE membership.practice_id = responsibility.practice_id
			AND membership.email = responsibility.account_email
			AND membership.revoked_at IS NULL
			AND (membership.location_scope = 'ALL' OR EXISTS (
				SELECT 1
				FROM access_membership_locations allowed
				WHERE allowed.membership_id = membership.id
					AND allowed.location_id = responsibility.location_id
			))
	)`

func patientDisplayNameSQL(phone string) string {
	return `(
		SELECT CASE WHEN count(DISTINCT lower(seen.name)) = 1
			THEN (array_agg(seen.name ORDER BY seen.seen_at DESC))[1]
		END
		FROM (` + phoneNamesSeen(phone) + `) seen
		WHERE seen.name <> ''
	)`
}

func patientWorkSearched(scope PatientWorkScope, search taskSearch) string {
	matched := `bool_or(` + patientWorkScopeSQL[scope] + ` AND item.searched)`
	if search.pattern == "" {
		return matched
	}
	return `(` + matched + ` OR lower(` + patientDisplayNameSQL("item.phone") + `) LIKE lower($3))`
}

func patientWorkSource(
	scope PatientWorkScope,
	filter PatientWorkFilter,
	search taskSearch,
	searchLocations string,
) string {
	return `WITH owner AS (
	SELECT
		responsibility.location_id,
		responsibility.category,
		bool_or(responsibility.account_email = $5) AS mine
	FROM work_responsibilities responsibility
	WHERE responsibility.practice_id = $1
		AND responsibility.location_id = ANY($2::uuid[])
		AND ` + responsibilityOwnerIsLive + `
	GROUP BY responsibility.location_id, responsibility.category
), item AS (
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
		(` + work.TaskIsFollowUpSQL + ` AND owner.category IS NULL) AS unowned,
		(` + work.TaskIsCommunicationReviewSQL + ` OR owner.category IS NULL OR owner.mine) AS mine,
		(true` + search.filter(searchLocations) + `) AS searched
	FROM work_tasks task
	LEFT JOIN owner
		ON owner.location_id = task.location_id
		AND owner.category = task.category
	WHERE task.practice_id = $1
		AND task.location_id = ANY($2::uuid[])
		AND task.state = 'OPEN'
), patient AS (
	SELECT
		item.phone,
		count(*) AS open_count,
		count(*) FILTER (WHERE ` + patientWorkScopeSQL[scope] + `) AS scope_open_count,
		bool_or(item.unowned) AS has_unowned,
		` + patientWorkSearched(scope, search) + ` AS searched,
		bool_or(` + patientWorkScopeSQL[scope] + `) AS in_scope,
		bool_or(` + patientWorkScopeSQL[scope] + ` AND item.kind_rank = 3) AS in_follow_up,
		bool_or(` + patientWorkScopeSQL[scope] + ` AND item.kind_rank = 0) AS in_call_back,
		bool_or(` + patientWorkScopeSQL[scope] + ` AND item.kind_rank = 1) AS in_texts,
		bool_or(` + patientWorkScopeSQL[scope] + ` AND item.kind_rank = 2) AS in_appointments,
		bool_or(` + patientWorkScopeSQL[scope] + ` AND item.unowned) AS in_unowned,
		COALESCE(bool_or(item.high_priority) FILTER (WHERE ` + patientWorkHit(scope, filter) + `), false) AS high_priority,
		max(item.activity_at) FILTER (WHERE ` + patientWorkHit(scope, filter) + `) AS activity_at
	FROM item
	GROUP BY item.phone
)`
}

func patientWorkHit(scope PatientWorkScope, filter PatientWorkFilter) string {
	return patientWorkScopeSQL[scope] + ` AND ` + patientWorkFilterSQL[filter]
}

func patientWorkPageSQL(scope PatientWorkScope, filter PatientWorkFilter, search taskSearch) string {
	return patientWorkSource(scope, filter, search, "$11") + `, page AS (
	SELECT patient.phone, patient.open_count, patient.high_priority, patient.has_unowned, patient.activity_at
	FROM patient
	WHERE patient.searched
		AND patient.activity_at IS NOT NULL
		AND (
			NOT $6
			OR (NOT patient.high_priority AND $7)
			OR (
				patient.high_priority = $7
				AND (patient.activity_at, patient.phone) < ($8, $9)
			)
		)
	ORDER BY patient.high_priority DESC, patient.activity_at DESC, patient.phone DESC
	LIMIT $10
)
SELECT
	page.phone,
	page.open_count,
	page.high_priority,
	page.has_unowned,
	page.activity_at,
	` + patientDisplayNameSQL("page.phone") + `,` + taskReadColumns + `
FROM page
JOIN LATERAL (
	SELECT item.id
	FROM item
	WHERE item.phone = page.phone
		AND ` + patientWorkHit(scope, filter) + `
	ORDER BY item.high_priority DESC, item.kind_rank, item.activity_at DESC, item.id
	LIMIT 1
) top ON true
JOIN work_tasks task ON task.id = top.id` + taskProjectionJoins + `
ORDER BY page.high_priority DESC, page.activity_at DESC, page.phone DESC`
}

func patientWorkSummarySQL(scope PatientWorkScope, search taskSearch) string {
	return patientWorkSource(scope, PatientWorkAll, search, "$6") + `
SELECT
	count(*) FILTER (WHERE patient.searched AND patient.in_scope),
	count(*) FILTER (WHERE patient.searched AND patient.in_follow_up),
	count(*) FILTER (WHERE patient.searched AND patient.in_call_back),
	count(*) FILTER (WHERE patient.searched AND patient.in_texts),
	count(*) FILTER (WHERE patient.searched AND patient.in_appointments),
	count(*) FILTER (WHERE patient.searched AND patient.in_unowned),
	COALESCE(sum(patient.scope_open_count), 0),
	count(*) FILTER (WHERE patient.in_scope),
	count(*) FILTER (WHERE NOT patient.in_scope)
FROM patient`
}

type patientWorkCursor struct {
	Present      bool              `json:"-"`
	Scope        PatientWorkScope  `json:"scope"`
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

func decodePatientWorkCursor(
	raw string,
	scope PatientWorkScope,
	filter PatientWorkFilter,
) (patientWorkCursor, error) {
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
		cursor.Scope != scope || cursor.Filter != filter ||
		!contactcontext.IsCanonicalPhone(cursor.Phone) {
		return patientWorkCursor{}, ErrInvalidInput
	}
	cursor.Present = true
	return cursor, nil
}
