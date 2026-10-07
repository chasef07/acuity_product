package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
)

type ScorecardReportCommand struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Weeks      int
	TimeZone   string
}

type BookingConversionWeek struct {
	WeekStart    string   `json:"weekStart"`
	LocationID   string   `json:"locationId"`
	LocationName string   `json:"locationName"`
	Calls        int      `json:"calls"`
	JudgedCalls  int      `json:"judgedCalls"`
	BookingCalls int      `json:"bookingCalls"`
	Converted    int      `json:"converted"`
	Blocked      int      `json:"blocked"`
	Missed       int      `json:"missed"`
	Attempted    int      `json:"attempted"`
	Conversion   *float64 `json:"conversion"`
}

type UnverifiedInsurancePlan struct {
	WeekStart    string    `json:"weekStart"`
	LocationID   string    `json:"locationId"`
	LocationName string    `json:"locationName"`
	Plan         string    `json:"plan"`
	Result       string    `json:"result"`
	Calls        int       `json:"calls"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
}

type ScorecardReport struct {
	From                string                    `json:"from"`
	Through             string                    `json:"through"`
	TimeZone            string                    `json:"timeZone"`
	Weeks               []BookingConversionWeek   `json:"weeks"`
	UnverifiedInsurance []UnverifiedInsurancePlan `json:"unverifiedInsurance"`
}

func weekStart(value time.Time, zone *time.Location) time.Time {
	local := value.In(zone)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
}

func (m *Module) QueryScorecardReport(ctx context.Context, command ScorecardReportCommand) (ScorecardReport, error) {
	zone, validZone := reportingZone(command.TimeZone)
	if !validZone || command.Weeks < 1 || command.Weeks > 12 {
		return ScorecardReport{}, ErrInvalidInput
	}
	to := weekStart(m.now(), zone).AddDate(0, 0, 7)
	from := to.AddDate(0, 0, -7*command.Weeks)
	tx, authorization, locations, err := m.beginAnalyticsAuthorization(ctx, command.Identity, command.PracticeID, command.LocationID, audienceOperator)
	if err != nil {
		return ScorecardReport{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT i.started_at, i.location_id::text,
			jsonb_object_agg(a.question, a.answer),
			(array_agg(a.detail) FILTER (WHERE a.question = 'insurance_verified' AND NOT a.answer))[1]
		FROM ai_interactions i
		JOIN ai_interaction_scorecard_answers a ON a.interaction_id = i.id
		WHERE i.practice_id = $1 AND i.location_id = ANY($2::uuid[])
			AND i.started_at >= $3 AND i.started_at < $4 AND i.lifecycle_stage = 3
		GROUP BY i.id
		LIMIT $5
	`, command.PracticeID, locations, from, to, analyticsRowLimit+1)
	if err != nil {
		return ScorecardReport{}, fmt.Errorf("query scorecard report: %w", err)
	}
	names := locationNames(authorization)
	type weekKey struct{ week, location string }
	weeks := map[weekKey]*BookingConversionWeek{}
	type planKey struct{ week, location, plan, result string }
	plans := map[planKey]*UnverifiedInsurancePlan{}
	count := 0
	for rows.Next() {
		count++
		var startedAt time.Time
		var locationID string
		var answersRaw, insuranceRaw json.RawMessage
		if err := rows.Scan(&startedAt, &locationID, &answersRaw, &insuranceRaw); err != nil {
			rows.Close()
			return ScorecardReport{}, fmt.Errorf("read scorecard report: %w", err)
		}
		locationName := names[locationID]
		answers := map[string]bool{}
		if json.Unmarshal(answersRaw, &answers) != nil {
			rows.Close()
			return ScorecardReport{}, fmt.Errorf("decode scorecard answers")
		}
		week := weekStart(startedAt, zone).Format("2006-01-02")
		key := weekKey{week, locationID}
		if weeks[key] == nil {
			weeks[key] = &BookingConversionWeek{WeekStart: week, LocationID: locationID, LocationName: locationName}
		}
		row := weeks[key]
		row.Calls++
		if _, judged := answers["booking_requested"]; judged {
			row.JudgedCalls++
		}
		switch classifyBooking(answers) {
		case bookingConverted:
			row.Converted++
		case bookingBlocked:
			row.Blocked++
		case bookingMissed:
			row.Missed++
		case bookingAttempted:
			row.Attempted++
		}
		var insurance struct {
			Plan   string `json:"plan"`
			Result string `json:"result"`
		}
		if len(insuranceRaw) > 0 && json.Unmarshal(insuranceRaw, &insurance) == nil {
			plan := strings.Join(strings.Fields(insurance.Plan), " ")
			if plan == "" {
				plan = "Plan not stated"
			}
			result, _, _ := strings.Cut(insurance.Result, ":")
			pk := planKey{week, locationID, strings.ToLower(plan), strings.ToLower(result)}
			if plans[pk] == nil {
				plans[pk] = &UnverifiedInsurancePlan{WeekStart: week, LocationID: locationID, LocationName: locationName, Plan: plan, Result: strings.TrimSpace(result)}
			}
			plans[pk].Calls++
			if startedAt.After(plans[pk].LastSeenAt) {
				plans[pk].LastSeenAt = startedAt
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ScorecardReport{}, fmt.Errorf("read scorecard report: %w", err)
	}
	if count > analyticsRowLimit {
		return ScorecardReport{}, fmt.Errorf("scorecard report exceeds bounded reporting window")
	}
	report := ScorecardReport{
		From: from.Format("2006-01-02"), Through: to.AddDate(0, 0, -1).Format("2006-01-02"), TimeZone: zone.String(),
		Weeks: []BookingConversionWeek{}, UnverifiedInsurance: []UnverifiedInsurancePlan{},
	}
	for _, row := range weeks {
		row.BookingCalls = row.Converted + row.Blocked + row.Missed + row.Attempted
		if eligible := row.BookingCalls - row.Blocked; eligible > 0 {
			conversion := float64(row.Converted) / float64(eligible)
			row.Conversion = &conversion
		}
		report.Weeks = append(report.Weeks, *row)
	}
	sort.Slice(report.Weeks, func(left, right int) bool {
		if report.Weeks[left].WeekStart != report.Weeks[right].WeekStart {
			return report.Weeks[left].WeekStart > report.Weeks[right].WeekStart
		}
		return report.Weeks[left].LocationName < report.Weeks[right].LocationName
	})
	for _, plan := range plans {
		report.UnverifiedInsurance = append(report.UnverifiedInsurance, *plan)
	}
	sort.Slice(report.UnverifiedInsurance, func(left, right int) bool {
		a, b := report.UnverifiedInsurance[left], report.UnverifiedInsurance[right]
		if a.WeekStart != b.WeekStart {
			return a.WeekStart > b.WeekStart
		}
		if a.Calls != b.Calls {
			return a.Calls > b.Calls
		}
		return a.Plan < b.Plan
	})
	if err := tx.Commit(ctx); err != nil {
		return ScorecardReport{}, fmt.Errorf("commit scorecard report: %w", err)
	}
	return report, nil
}
