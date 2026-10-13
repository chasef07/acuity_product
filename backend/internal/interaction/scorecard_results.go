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
}

type ScorecardResults struct {
	Calls          int            `json:"calls"`
	ProblemCalls   int            `json:"problemCalls"`
	BookingCallers int            `json:"bookingCallers"`
	Converted      int            `json:"converted"`
	Blocked        int            `json:"blocked"`
	Missed         int            `json:"missed"`
	NotSearched    int            `json:"notSearched"`
	Conversion     *float64       `json:"conversion"`
	Daily          []ScorecardDay `json:"daily"`
}

var bookingStrength = map[bookingOutcome]int{bookingConverted: 5, bookingBlocked: 4, bookingAttempted: 3, bookingMissed: 2, bookingNotSearched: 1}

func scorecardProblem(answers map[string]bool) bool {
	outcome := classifyBooking(answers)
	return outcome == bookingMissed || outcome == bookingNotSearched || failedReviewQuestion(answers)
}

func recordCallerBooking(callers map[string]bookingOutcome, phone string, outcome bookingOutcome) {
	if current, seen := callers[phone]; outcome != bookingNotRequested && (!seen || bookingStrength[outcome] > bookingStrength[current]) {
		callers[phone] = outcome
	}
}

func (result *ScorecardResults) countBookingCallers(callers map[string]bookingOutcome) {
	for _, outcome := range callers {
		if outcome == bookingUnknown {
			continue
		}
		result.BookingCallers++
		switch outcome {
		case bookingConverted:
			result.Converted++
		case bookingBlocked:
			result.Blocked++
		case bookingMissed:
			result.Missed++
		case bookingNotSearched:
			result.NotSearched++
		}
	}
	result.Conversion = rate(result.Converted, result.BookingCallers-result.Blocked)
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
		SELECT i.started_at, i.phone, jsonb_object_agg(a.question, a.answer)
		FROM ai_interactions i
		JOIN ai_interaction_scorecard_answers a ON a.interaction_id = i.id
		WHERE i.practice_id = $1 AND i.location_id = ANY($2::uuid[])
			AND i.started_at >= $3 AND i.started_at < $4 AND i.lifecycle_stage = 3
		GROUP BY i.id
		HAVING bool_or(a.source = 'judge' AND a.scorecard_version = $6)
		LIMIT $5
	`, command.PracticeID, locations, from, to, analyticsRowLimit+1, ScorecardJudgeVersion)
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
	callers := map[string]bookingOutcome{}
	count := 0
	for rows.Next() {
		count++
		var startedAt time.Time
		var phone string
		var raw json.RawMessage
		if err := rows.Scan(&startedAt, &phone, &raw); err != nil {
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
		recordCallerBooking(callers, phone, classifyBooking(answers))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ScorecardResults{}, fmt.Errorf("read scorecard results: %w", err)
	}
	if count > analyticsRowLimit {
		return ScorecardResults{}, fmt.Errorf("scorecard results exceed bounded reporting window")
	}
	result.countBookingCallers(callers)
	for index := range result.Daily {
		day := &result.Daily[index]
		day.ProblemRate = rate(day.ProblemCalls, day.Calls)
	}
	if err := tx.Commit(ctx); err != nil {
		return ScorecardResults{}, fmt.Errorf("commit scorecard results: %w", err)
	}
	return result, nil
}
