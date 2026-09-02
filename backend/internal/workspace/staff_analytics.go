package workspace

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type QueryStaffAnalyticsCommand struct {
	Identity                         access.Identity
	PracticeID, LocationID, TimeZone string
	Days                             int
}

type StaffPhoneMetrics struct {
	InboundCalls                 int     `json:"inboundCalls"`
	OutboundCalls                int     `json:"outboundCalls"`
	InboundSeconds               float64 `json:"inboundSeconds"`
	OutboundSeconds              float64 `json:"outboundSeconds"`
	MissingInboundDurationCalls  int     `json:"missingInboundDurationCalls"`
	MissingOutboundDurationCalls int     `json:"missingOutboundDurationCalls"`
	TasksCompleted               int     `json:"tasksCompleted"`
}

type StaffAccountAnalytics struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Status string `json:"status"`
	StaffPhoneMetrics
}

type StaffTaskMetrics struct {
	Completed            int      `json:"completed"`
	P50Seconds           *float64 `json:"p50Seconds"`
	P90Seconds           *float64 `json:"p90Seconds"`
	Eligible             int      `json:"eligible"`
	Within48Hours        int      `json:"within48Hours"`
	Within48HoursPercent *float64 `json:"within48HoursPercent"`
}

type StaffTaskDay struct {
	Day        string   `json:"day"`
	Completed  int      `json:"completed"`
	P50Seconds *float64 `json:"p50Seconds"`
}

type StaffAnalytics struct {
	From     string                  `json:"from"`
	Through  string                  `json:"through"`
	Tasks    StaffTaskMetrics        `json:"tasks"`
	Accounts []StaffAccountAnalytics `json:"accounts"`
	Total    StaffPhoneMetrics       `json:"total"`
	Daily    []StaffTaskDay          `json:"daily"`
}

type staffTaskFact struct {
	Created                                time.Time
	Completed                              *time.Time
	State, CompletedKind, CompletedSubject string
}

func (m *Module) QueryStaffAnalytics(ctx context.Context, command QueryStaffAnalyticsCommand) (StaffAnalytics, error) {
	zone, zoneErr := time.LoadLocation(command.TimeZone)
	_, practiceErr := uuid.Parse(command.PracticeID)
	var locationErr error
	if command.LocationID != "" {
		_, locationErr = uuid.Parse(command.LocationID)
	}
	if m.database == nil || m.access == nil || practiceErr != nil || locationErr != nil ||
		command.TimeZone == "" || command.TimeZone == "Local" || zoneErr != nil ||
		(command.Days != 7 && command.Days != 30 && command.Days != 90) {
		return StaffAnalytics{}, ErrInvalidInput
	}
	now := time.Now().In(zone)
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
	from := to.AddDate(0, 0, -command.Days)
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return StaffAnalytics{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '1500ms'; SET LOCAL lock_timeout = '100ms'; SET LOCAL max_parallel_workers_per_gather = 0; SET LOCAL work_mem = '4MB'`); err != nil {
		return StaffAnalytics{}, err
	}
	auth, err := m.access.LockReadAuthorization(ctx, tx, command.Identity, command.PracticeID, command.LocationID)
	if errors.Is(err, access.ErrDenied) {
		return StaffAnalytics{}, ErrDenied
	}
	if err != nil {
		return StaffAnalytics{}, err
	}
	if !auth.PlatformOperator && auth.Membership.Role != access.RoleAdmin {
		return StaffAnalytics{}, ErrDenied
	}
	locations := []string{}
	for _, location := range auth.Locations {
		if command.LocationID == "" || command.LocationID == location.ID {
			locations = append(locations, location.ID)
		}
	}
	if len(locations) == 0 {
		return StaffAnalytics{}, ErrDenied
	}

	report := StaffAnalytics{From: from.Format(time.DateOnly), Through: to.AddDate(0, 0, -1).Format(time.DateOnly), Accounts: []StaffAccountAnalytics{}, Daily: []StaffTaskDay{}}
	// Keep all accounts visible, including zero-activity and not-yet-activated accounts.
	// Location filtering changes activity, not the Practice's account directory.
	rows, err := tx.Query(ctx, `
  SELECT id::text, user_subject, email, role::text,
   CASE WHEN revoked_at IS NULL THEN 'ACTIVE' ELSE 'INACTIVE' END
  FROM access_memberships WHERE practice_id=$1::uuid
  UNION ALL
  SELECT g.id::text, '', g.email, g.role::text, 'PENDING'
  FROM access_grants g WHERE g.practice_id=$1::uuid AND g.claimed_at IS NULL AND g.revoked_at IS NULL
   AND NOT EXISTS (SELECT 1 FROM access_memberships m WHERE m.practice_id=g.practice_id AND m.email=g.email)
  ORDER BY 3, 1 LIMIT 5001`, command.PracticeID)
	if err != nil {
		return StaffAnalytics{}, fmt.Errorf("staff accounts: %w", err)
	}
	subjects := map[string]int{}
	for rows.Next() {
		var account StaffAccountAnalytics
		var subject string
		if err := rows.Scan(&account.ID, &subject, &account.Email, &account.Role, &account.Status); err != nil {
			rows.Close()
			return StaffAnalytics{}, err
		}
		if subject != "" {
			subjects[subject] = len(report.Accounts)
		}
		report.Accounts = append(report.Accounts, account)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return StaffAnalytics{}, err
	}
	if len(report.Accounts) > 5000 {
		return StaffAnalytics{}, fmt.Errorf("staff analytics exceeds account limit")
	}
	accountFor := func(subject string) *StaffAccountAnalytics {
		if index, ok := subjects[subject]; ok {
			return &report.Accounts[index]
		}
		// Preserve historical work by accounts no longer in the directory without exposing auth subjects.
		const other = "other-accounts"
		if index, ok := subjects[other]; ok {
			return &report.Accounts[index]
		}
		subjects[other] = len(report.Accounts)
		report.Accounts = append(report.Accounts, StaffAccountAnalytics{ID: other, Email: "Other accounts", Role: "", Status: "HISTORICAL"})
		return &report.Accounts[len(report.Accounts)-1]
	}
	// One handled call per person and direction; repeated legs add only their connected time.
	rows, err = tx.Query(ctx, `
  SELECT l.staff_subject, c.id::text, c.direction,
    l.bridged_at, l.ended_at
  FROM human_calling_call_legs l JOIN human_calling_calls c ON c.id=l.call_id
  WHERE c.practice_id=$1::uuid AND c.location_id=ANY($2::uuid[])
   AND l.role='STAFF' AND l.bridged_at >= $3 AND l.bridged_at < $4
   AND c.ended_at >= $3
  LIMIT 50001`, command.PracticeID, locations, from, to)
	if err != nil {
		return StaffAnalytics{}, fmt.Errorf("staff phone activity: %w", err)
	}
	seen := map[string]bool{}
	missing := map[string]bool{}
	callRows := 0
	for rows.Next() {
		var subject, callID, direction string
		var start time.Time
		var end *time.Time
		if err := rows.Scan(&subject, &callID, &direction, &start, &end); err != nil {
			rows.Close()
			return StaffAnalytics{}, err
		}
		callRows++
		account := accountFor(subject)
		key := subject + ":" + callID
		if !seen[key] {
			if direction == "INBOUND" {
				account.InboundCalls++
				report.Total.InboundCalls++
			} else {
				account.OutboundCalls++
				report.Total.OutboundCalls++
			}
			seen[key] = true
		}
		if end == nil || end.Before(start) {
			if !missing[key] {
				if direction == "INBOUND" {
					account.MissingInboundDurationCalls++
					report.Total.MissingInboundDurationCalls++
				} else {
					account.MissingOutboundDurationCalls++
					report.Total.MissingOutboundDurationCalls++
				}
				missing[key] = true
			}
			continue
		}
		seconds := end.Sub(start).Seconds()
		if direction == "INBOUND" {
			account.InboundSeconds += seconds
			report.Total.InboundSeconds += seconds
		} else {
			account.OutboundSeconds += seconds
			report.Total.OutboundSeconds += seconds
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return StaffAnalytics{}, err
	}
	if callRows > 50000 {
		return StaffAnalytics{}, fmt.Errorf("staff analytics exceeds call limit")
	}

	// The creation clock never resets on viewing, assignment, or reopening.
	// Current completed state determines completion credit; reopened work remains unfinished.
	rows, err = tx.Query(ctx, `
  SELECT created_at, completed_at, state, COALESCE(completed_by_kind,''), COALESCE(completed_by_subject,'')
  FROM work_tasks WHERE practice_id=$1::uuid AND location_id=ANY($2::uuid[])
   AND ((created_at >= $3 AND created_at < $4)
        OR (state='COMPLETED' AND completed_at >= $3 AND completed_at < $4))
  LIMIT 50001`, command.PracticeID, locations, from, to)
	if err != nil {
		return StaffAnalytics{}, fmt.Errorf("staff task activity: %w", err)
	}
	facts := []staffTaskFact{}
	for rows.Next() {
		var fact staffTaskFact
		if err := rows.Scan(&fact.Created, &fact.Completed, &fact.State, &fact.CompletedKind, &fact.CompletedSubject); err != nil {
			rows.Close()
			return StaffAnalytics{}, err
		}
		facts = append(facts, fact)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return StaffAnalytics{}, err
	}
	if len(facts) > 50000 {
		return StaffAnalytics{}, fmt.Errorf("staff analytics exceeds task limit")
	}
	if err := tx.Commit(ctx); err != nil {
		return StaffAnalytics{}, err
	}
	report.Tasks, report.Daily = summarizeStaffTasks(facts, from, to, now)
	for _, fact := range facts {
		if fact.State == "COMPLETED" && fact.Completed != nil && !fact.Completed.Before(from) && fact.Completed.Before(to) && fact.CompletedKind == "HUMAN" {
			accountFor(fact.CompletedSubject).TasksCompleted++
			report.Total.TasksCompleted++
		}
	}
	return report, nil
}

func staffPercentile(values []float64, fraction float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sort.Float64s(values)
	position := float64(len(values)-1) * fraction
	lo, hi := int(math.Floor(position)), int(math.Ceil(position))
	result := values[lo] + (values[hi]-values[lo])*(position-float64(lo))
	return &result
}

func summarizeStaffTasks(facts []staffTaskFact, from, to, now time.Time) (StaffTaskMetrics, []StaffTaskDay) {
	result := StaffTaskMetrics{}
	daily := []StaffTaskDay{}
	durations := []float64{}
	byDay := map[string][]float64{}
	for _, fact := range facts {
		deadline := fact.Created.Add(48 * time.Hour)
		validCompletion := fact.State == "COMPLETED" && fact.Completed != nil && !fact.Completed.Before(fact.Created)
		if !fact.Created.Before(from) && fact.Created.Before(to) && !deadline.After(now) {
			result.Eligible++
			if validCompletion && !fact.Completed.After(deadline) {
				result.Within48Hours++
			}
		}
		if validCompletion && !fact.Completed.Before(from) && fact.Completed.Before(to) {
			result.Completed++
			seconds := fact.Completed.Sub(fact.Created).Seconds()
			durations = append(durations, seconds)
			day := fact.Completed.In(from.Location()).Format(time.DateOnly)
			byDay[day] = append(byDay[day], seconds)
		}
	}
	if result.Eligible > 0 {
		value := 100 * float64(result.Within48Hours) / float64(result.Eligible)
		result.Within48HoursPercent = &value
	}
	result.P50Seconds = staffPercentile(durations, .5)
	result.P90Seconds = staffPercentile(durations, .9)
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		key := day.Format(time.DateOnly)
		daily = append(daily, StaffTaskDay{Day: key, Completed: len(byDay[key]), P50Seconds: staffPercentile(byDay[key], .5)})
	}
	return result, daily
}
