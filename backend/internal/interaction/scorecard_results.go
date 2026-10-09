package interaction

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
)

const scorecardResultCallLimit = 500

type ScorecardResultsCommand struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Range      AnalyticsRange
}

type ScorecardResultRow struct {
	Question string `json:"question"`
	Answered int    `json:"answered"`
	No       int    `json:"no"`
}

type ScorecardResultCall struct {
	InteractionID string    `json:"interactionId"`
	StartedAt     time.Time `json:"startedAt"`
	LocationName  string    `json:"locationName"`
	Question      string    `json:"question"`
}

type ScorecardResults struct {
	Calls     int                   `json:"calls"`
	Rows      []ScorecardResultRow  `json:"rows"`
	NoCalls   []ScorecardResultCall `json:"noCalls"`
	Truncated bool                  `json:"truncated"`
}

func (m *Module) QueryScorecardResults(ctx context.Context, command ScorecardResultsCommand) (ScorecardResults, error) {
	duration, valid := analyticsRangeDuration(command.Range)
	if !valid {
		return ScorecardResults{}, ErrInvalidInput
	}
	to := m.now().UTC()
	from := to.Add(-duration)
	tx, authorization, locations, err := m.beginAnalyticsAuthorization(ctx, command.Identity, command.PracticeID, command.LocationID, audienceOperator)
	if err != nil {
		return ScorecardResults{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	names := locationNames(authorization)
	rows, err := tx.Query(ctx, `
		SELECT i.id::text, i.started_at, i.location_id::text, a.question, a.answer
		FROM ai_interactions i
		JOIN ai_interaction_scorecard_answers a ON a.interaction_id = i.id
		WHERE i.practice_id = $1 AND i.location_id = ANY($2::uuid[])
			AND i.started_at >= $3 AND i.started_at < $4 AND i.lifecycle_stage = 3
		ORDER BY i.started_at DESC, i.id
		LIMIT $5
	`, command.PracticeID, locations, from, to, analyticsRowLimit+1)
	if err != nil {
		return ScorecardResults{}, fmt.Errorf("query scorecard results: %w", err)
	}
	result := ScorecardResults{Rows: []ScorecardResultRow{}, NoCalls: []ScorecardResultCall{}}
	counts := map[string]*ScorecardResultRow{}
	calls := map[string]bool{}
	count := 0
	for rows.Next() {
		count++
		var call ScorecardResultCall
		var locationID string
		var answer bool
		if err := rows.Scan(&call.InteractionID, &call.StartedAt, &locationID, &call.Question, &answer); err != nil {
			rows.Close()
			return ScorecardResults{}, fmt.Errorf("read scorecard results: %w", err)
		}
		calls[call.InteractionID] = true
		if counts[call.Question] == nil {
			counts[call.Question] = &ScorecardResultRow{Question: call.Question}
		}
		counts[call.Question].Answered++
		if answer {
			continue
		}
		counts[call.Question].No++
		if len(result.NoCalls) < scorecardResultCallLimit {
			call.LocationName = names[locationID]
			result.NoCalls = append(result.NoCalls, call)
		} else {
			result.Truncated = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ScorecardResults{}, fmt.Errorf("read scorecard results: %w", err)
	}
	if count > analyticsRowLimit {
		return ScorecardResults{}, fmt.Errorf("scorecard results exceed bounded reporting window")
	}
	order := map[string]int{}
	for index, question := range ScorecardQuestions {
		order[question.Key] = index
	}
	for _, row := range counts {
		if _, known := order[row.Question]; known {
			result.Rows = append(result.Rows, *row)
		}
	}
	sort.Slice(result.Rows, func(left, right int) bool {
		return order[result.Rows[left].Question] < order[result.Rows[right].Question]
	})
	result.Calls = len(calls)
	if err := tx.Commit(ctx); err != nil {
		return ScorecardResults{}, fmt.Errorf("commit scorecard results: %w", err)
	}
	return result, nil
}
