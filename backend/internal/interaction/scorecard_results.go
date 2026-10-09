package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
)

type ScorecardResultsCommand struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Range      AnalyticsRange
}

type ScorecardDay struct {
	Date         string   `json:"date"`
	Calls        int      `json:"calls"`
	ProblemCalls int      `json:"problemCalls"`
	ProblemRate  *float64 `json:"problemRate"`
	BookingCalls int      `json:"bookingCalls"`
	Converted    int      `json:"converted"`
	Conversion   *float64 `json:"conversion"`
}

type ScorecardResults struct {
	Calls        int            `json:"calls"`
	ProblemCalls int            `json:"problemCalls"`
	BookingCalls int            `json:"bookingCalls"`
	Converted    int            `json:"converted"`
	Blocked      int            `json:"blocked"`
	Missed       int            `json:"missed"`
	Conversion   *float64       `json:"conversion"`
	Daily        []ScorecardDay `json:"daily"`
}

func scorecardProblem(answers map[string]bool) bool {
	if classifyBooking(answers) == bookingMissed {
		return true
	}
	for question, answer := range answers {
		if !answer && reviewFailureQuestions[question] {
			return true
		}
	}
	return false
}

func rate(part, whole int) *float64 {
	if whole == 0 {
		return nil
	}
	value := float64(part) / float64(whole)
	return &value
}

func (m *Module) QueryScorecardResults(ctx context.Context, command ScorecardResultsCommand) (ScorecardResults, error) {
	duration, valid := analyticsRangeDuration(command.Range)
	if !valid {
		return ScorecardResults{}, ErrInvalidInput
	}
	to := m.now().UTC()
	from := to.Add(-duration)
	tx, locations, err := m.beginAnalyticsScope(ctx, command.Identity, command.PracticeID, command.LocationID, audienceOperator)
	if err != nil {
		return ScorecardResults{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT i.started_at, jsonb_object_agg(a.question, a.answer)
		FROM ai_interactions i
		JOIN ai_interaction_scorecard_answers a ON a.interaction_id = i.id
		WHERE i.practice_id = $1 AND i.location_id = ANY($2::uuid[])
			AND i.started_at >= $3 AND i.started_at < $4 AND i.lifecycle_stage = 3
		GROUP BY i.id
		LIMIT $5
	`, command.PracticeID, locations, from, to, analyticsRowLimit+1)
	if err != nil {
		return ScorecardResults{}, fmt.Errorf("query scorecard results: %w", err)
	}
	result := ScorecardResults{Daily: []ScorecardDay{}}
	days := map[string]*ScorecardDay{}
	for day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC); day.Before(to); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		result.Daily = append(result.Daily, ScorecardDay{Date: key})
		days[key] = &result.Daily[len(result.Daily)-1]
	}
	count := 0
	for rows.Next() {
		count++
		var startedAt time.Time
		var raw json.RawMessage
		if err := rows.Scan(&startedAt, &raw); err != nil {
			rows.Close()
			return ScorecardResults{}, fmt.Errorf("read scorecard results: %w", err)
		}
		answers := map[string]bool{}
		if json.Unmarshal(raw, &answers) != nil {
			rows.Close()
			return ScorecardResults{}, fmt.Errorf("decode scorecard answers")
		}
		day := days[startedAt.UTC().Format("2006-01-02")]
		if day == nil {
			continue
		}
		result.Calls++
		day.Calls++
		if scorecardProblem(answers) {
			result.ProblemCalls++
			day.ProblemCalls++
		}
		switch classifyBooking(answers) {
		case bookingConverted:
			result.Converted++
			day.Converted++
			day.BookingCalls++
		case bookingBlocked:
			result.Blocked++
		case bookingMissed:
			result.Missed++
			day.BookingCalls++
		case bookingAttempted:
			day.BookingCalls++
		}
		if classifyBooking(answers) != bookingNotRequested {
			result.BookingCalls++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ScorecardResults{}, fmt.Errorf("read scorecard results: %w", err)
	}
	if count > analyticsRowLimit {
		return ScorecardResults{}, fmt.Errorf("scorecard results exceed bounded reporting window")
	}
	result.Conversion = rate(result.Converted, result.BookingCalls-result.Blocked)
	for index := range result.Daily {
		day := &result.Daily[index]
		day.ProblemRate = rate(day.ProblemCalls, day.Calls)
		day.Conversion = rate(day.Converted, day.BookingCalls)
	}
	if err := tx.Commit(ctx); err != nil {
		return ScorecardResults{}, fmt.Errorf("commit scorecard results: %w", err)
	}
	return result, nil
}
