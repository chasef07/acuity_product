package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
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
	PatientWorkCallBack     PatientWorkFilter = "callBack"
	PatientWorkTexts        PatientWorkFilter = "texts"
	PatientWorkAppointments PatientWorkFilter = "appointments"
	PatientWorkUnowned      PatientWorkFilter = "unowned"
	PatientWorkPriority     PatientWorkFilter = "highPriorityOnly"
)

var patientWorkFilterSQL = map[PatientWorkFilter]string{
	PatientWorkAll:          `true`,
	PatientWorkCallBack:     `item.kind_rank = 0`,
	PatientWorkTexts:        `item.kind_rank = 1`,
	PatientWorkAppointments: `item.kind_rank = 2`,
	PatientWorkUnowned:      `item.unowned`,
	PatientWorkPriority:     `item.high_priority`,
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
	Groups     []work.TaskCategory
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
	WaitingSince time.Time
	Top          work.Task
}

type PatientWorkCounts struct {
	All          int
	CallBack     int
	Texts        int
	Appointments int
	Unowned      int
	Priority     int
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
	groups, knownGroups := patientWorkGroupsSQL(command.Groups)
	if m.database == nil || m.access == nil || command.PracticeID == "" ||
		len(command.Search) > 500 || !knownScope || !knownFilter || !knownGroups ||
		limit < 1 || limit > 50 {
		return PatientWorkPage{}, ErrInvalidInput
	}
	cursor, err := decodePatientWorkCursor(command.Cursor, command.Scope, command.Filter, command.Groups)
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
	rows, err := tx.Query(ctx, patientWorkPageSQL(command.Scope, command.Filter, groups, search), search.arguments(
		command.PracticeID, locationIDs, search.pattern, search.digits, email,
		cursor.Present, cursor.ActivityAt, cursor.Phone, limit+1,
	)...)
	if err != nil {
		return PatientWorkPage{}, fmt.Errorf("query patient work: %w", err)
	}
	for rows.Next() {
		var row PatientWorkRow
		var displayName *string
		row.Top, err = scanTaskProjection(rows,
			&row.Phone, &row.OpenCount, &row.HighPriority, &row.HasUnowned,
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

	if err := tx.QueryRow(ctx, patientWorkSummarySQL(command.Scope, groups, search), search.arguments(
		command.PracticeID, locationIDs, search.pattern, search.digits, email,
	)...).Scan(
		&page.Counts.All, &page.Counts.CallBack, &page.Counts.Texts,
		&page.Counts.Appointments, &page.Counts.Unowned, &page.Counts.Priority,
		&page.Totals.OpenTasks, &page.Totals.Patients, &page.Totals.PatientsOutsideScope,
	); err != nil {
		return PatientWorkPage{}, fmt.Errorf("count patient work: %w", err)
	}

	if len(page.Rows) > limit {
		page.Rows = page.Rows[:limit]
		last := page.Rows[limit-1]
		page.NextCursor, err = encodePatientWorkCursor(patientWorkCursor{
			Scope:      command.Scope,
			Filter:     command.Filter,
			Groups:     command.Groups,
			ActivityAt: last.ActivityAt,
			Phone:      last.Phone,
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

const routedStaffCanReadTask = `
			SELECT 1 FROM access_memberships routed
			WHERE routed.practice_id = task.practice_id
				AND routed.user_subject = task.routed_to_subject
				AND routed.revoked_at IS NULL
				AND (routed.location_scope = 'ALL' OR EXISTS (
					SELECT 1 FROM access_membership_locations allowed
					WHERE allowed.membership_id = routed.id
						AND allowed.location_id = task.location_id
				))`

func routedReviewIsFor(email string) string {
	return `(task.routed_to_subject IS NULL
		OR NOT EXISTS (` + routedStaffCanReadTask + `)
		OR EXISTS (` + routedStaffCanReadTask + ` AND lower(routed.email) = ` + email + `))`
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

func patientWorkGroupsSQL(groups []work.TaskCategory) (string, bool) {
	if len(groups) == 0 {
		return `true`, true
	}
	quoted := make([]string, 0, len(groups))
	for _, group := range groups {
		if !slices.Contains(work.TaskCategories, group) {
			return "", false
		}
		quoted = append(quoted, `'`+string(group)+`'`)
	}
	return work.TaskIsCommunicationReviewSQL + ` OR task.category IS NULL OR task.category IN (` + strings.Join(quoted, ",") + `)`, true
}

func patientWorkInScope(scope PatientWorkScope) string {
	return `(` + patientWorkScopeSQL[scope] + ` AND item.in_groups)`
}

func patientWorkSearched(scope PatientWorkScope, search taskSearch) string {
	matched := `bool_or(` + patientWorkInScope(scope) + ` AND item.searched)`
	if search.pattern == "" {
		return matched
	}
	return `(` + matched + ` OR lower(` + patientDisplayNameSQL("item.phone") + `) LIKE lower($3))`
}

func patientWorkSource(
	scope PatientWorkScope,
	filter PatientWorkFilter,
	groups string,
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
		task.created_at,
		(` + work.TaskIsFollowUpSQL + ` AND owner.category IS NULL) AS unowned,
		(
			(` + work.TaskIsCommunicationReviewSQL + ` AND ` + routedReviewIsFor("$5") + `)
			OR (` + work.TaskIsFollowUpSQL + ` AND (owner.category IS NULL OR owner.mine))
		) AS mine,
		(` + groups + `) AS in_groups,
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
		count(*) FILTER (WHERE ` + patientWorkInScope(scope) + `) AS scope_open_count,
		bool_or(item.unowned) AS has_unowned,
		bool_or(item.in_groups) AS in_groups,
		` + patientWorkSearched(scope, search) + ` AS searched,
		bool_or(` + patientWorkInScope(scope) + `) AS in_scope,
		bool_or(` + patientWorkInScope(scope) + ` AND item.kind_rank = 0) AS in_call_back,
		bool_or(` + patientWorkInScope(scope) + ` AND item.kind_rank = 1) AS in_texts,
		bool_or(` + patientWorkInScope(scope) + ` AND item.kind_rank = 2) AS in_appointments,
		bool_or(` + patientWorkInScope(scope) + ` AND item.unowned) AS in_unowned,
		bool_or(` + patientWorkInScope(scope) + ` AND item.high_priority) AS in_high_priority,
		COALESCE(bool_or(item.high_priority) FILTER (WHERE ` + patientWorkHit(scope, filter) + `), false) AS high_priority,
		max(item.activity_at) FILTER (WHERE ` + patientWorkHit(scope, filter) + `) AS activity_at,
		min(item.created_at) FILTER (WHERE ` + patientWorkHit(scope, filter) + `) AS waiting_since
	FROM item
	GROUP BY item.phone
)`
}

func patientWorkHit(scope PatientWorkScope, filter PatientWorkFilter) string {
	return patientWorkInScope(scope) + ` AND ` + patientWorkFilterSQL[filter]
}

func patientWorkPageSQL(scope PatientWorkScope, filter PatientWorkFilter, groups string, search taskSearch) string {
	return patientWorkSource(scope, filter, groups, search, "$10") + `, page AS (
	SELECT patient.phone, patient.open_count, patient.high_priority, patient.has_unowned, patient.activity_at, patient.waiting_since
	FROM patient
	WHERE patient.searched
		AND patient.activity_at IS NOT NULL
		AND (NOT $6 OR (patient.activity_at, patient.phone) < ($7, $8))
	ORDER BY patient.activity_at DESC, patient.phone DESC
	LIMIT $9
)
SELECT
	page.phone,
	page.open_count,
	page.high_priority,
	page.has_unowned,
	page.activity_at,
	page.waiting_since,
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
ORDER BY page.activity_at DESC, page.phone DESC`
}

func patientWorkSummarySQL(scope PatientWorkScope, groups string, search taskSearch) string {
	return patientWorkSource(scope, PatientWorkAll, groups, search, "$6") + `
SELECT
	count(*) FILTER (WHERE patient.searched AND patient.in_scope),
	count(*) FILTER (WHERE patient.searched AND patient.in_call_back),
	count(*) FILTER (WHERE patient.searched AND patient.in_texts),
	count(*) FILTER (WHERE patient.searched AND patient.in_appointments),
	count(*) FILTER (WHERE patient.searched AND patient.in_unowned),
	count(*) FILTER (WHERE patient.searched AND patient.in_high_priority),
	COALESCE(sum(patient.scope_open_count), 0),
	count(*) FILTER (WHERE patient.in_scope),
	count(*) FILTER (WHERE patient.in_groups AND NOT patient.in_scope)
FROM patient`
}

type patientWorkCursor struct {
	Present    bool                `json:"-"`
	Scope      PatientWorkScope    `json:"scope"`
	Filter     PatientWorkFilter   `json:"filter"`
	Groups     []work.TaskCategory `json:"groups"`
	ActivityAt time.Time           `json:"activityAt"`
	Phone      string              `json:"phone"`
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
	groups []work.TaskCategory,
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
		cursor.Scope != scope || cursor.Filter != filter || !slices.Equal(cursor.Groups, groups) ||
		!contactcontext.IsCanonicalPhone(cursor.Phone) {
		return patientWorkCursor{}, ErrInvalidInput
	}
	cursor.Present = true
	return cursor, nil
}
