package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestBackfillScorecardsStoresJudgeAnswersAndKeepsEarlierJuries(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	accessModule := access.New(db.As(testdb.Portal), clock)
	if _, err := access.New(pool, clock).Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", Practices: []access.PracticeProvision{{Key: "scorecard", Name: "Synthetic Practice", Locations: []access.LocationProvision{{Key: "main", Name: "Main"}}}}}); err != nil {
		t.Fatal(err)
	}
	var practice, location string
	if err := pool.QueryRow(ctx, `SELECT p.id::text, l.id::text FROM access_practices p JOIN access_locations l ON l.practice_id=p.id WHERE p.provisioning_key='scorecard'`).Scan(&practice, &location); err != nil {
		t.Fatal(err)
	}
	evaluation := func(noul map[string]float64) map[string]any {
		results := map[string]any{}
		for name, value := range noul {
			results[name] = map[string]any{"verdict": value > scorecardNoAtOrBelow, "probability": value, "votes": map[string]any{"typesafe-ai/jev": value}, "errors": map[string]any{}}
		}
		return map[string]any{"evaluator": "jury", "evaluatorVersion": legacyJudgeVersion, "jurors": []string{"typesafe-ai/jev"}, "status": "complete", "results": results}
	}
	insert := func(index int, seconds int, tools []syntheticTool, judged map[string]float64) string {
		t.Helper()
		closeout, _ := json.Marshal(map[string]any{"versions": map[string]any{"agent": "0.13.1"}, "domainOutcomes": []any{}, "evaluation": evaluation(judged)})
		started := time.Date(2026, 10, 6, 14, 0, index, 0, time.UTC)
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions (service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload) VALUES ('agent',$1,$2,$3,'+15555550111','+15555550100',$4,$4::timestamptz + make_interval(secs => $5),'COMPLETED',3,$6,$7) RETURNING id::text`,
			practice, location, fmt.Sprintf("SCL_synthetic_%02d", index), started, seconds, syntheticTranscript(t, tools...), closeout).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	healthy := map[string]float64{"new_booking_requested": 0.1, "promises_backed": 0.9, "ended_while_caller_active": 0.9, "office_question_answered": 0.9, "office_rules_grounded": 0.9}
	insert(0, 60, nil, healthy)
	booked := insert(1, 90, []syntheticTool{
		{"check_insurance", `{"plan":"Synthetic Gold Plan"}`, "blocked: The office needs to verify this plan's coverage before scheduling."},
		{"list_available_appointments", `{}`, "success: Openings found.\nslot-1 – Monday"},
	}, map[string]float64{"new_booking_requested": 0.95, "time_offered": 0.2, "promises_backed": 0.9, "ended_while_caller_active": 0.3, "office_question_answered": 0.9, "office_rules_grounded": 0.9})

	module := New(db.As(testdb.Portal), accessModule, clock)
	backfill, err := module.BackfillScorecards(ctx, time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC), 7)
	if err != nil || backfill.Calls != 2 {
		t.Fatalf("backfill: %+v %v", backfill, err)
	}
	var agentVersion string
	var probability float64
	if err := pool.QueryRow(ctx, `SELECT agent_version, probability FROM ai_interaction_scorecard_answers WHERE interaction_id=$1 AND question='new_booking_requested'`, booked).Scan(&agentVersion, &probability); err != nil || agentVersion != "0.13.1" || probability != 0.95 {
		t.Fatalf("stored judge answer: %q %v %v", agentVersion, probability, err)
	}

	previous := insert(2, 60, nil, nil)
	if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET closeout_payload = jsonb_set(closeout_payload, '{evaluation}', $2::jsonb) WHERE id = $1`, previous,
		`{"evaluator":"jury","evaluatorVersion":"typesafe-scorecard-v6","jurors":["typesafe-ai/jev"],"status":"complete","results":{"booking_requested":{"verdict":true,"probability":0.9,"votes":{"typesafe-ai/jev":0.9},"errors":{}}}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_scorecard_answers (interaction_id, practice_id, question, source, answer, probability, scorecard_version, judge_model, computed_at) VALUES ($1, $2, 'booking_requested', 'judge', true, 0.9, 'typesafe-scorecard-v6', 'typesafe-ai/jev', $3)`, previous, practice, now); err != nil {
		t.Fatal(err)
	}
	if _, err := module.BackfillScorecards(ctx, time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC), 7); err != nil {
		t.Fatal(err)
	}
	var keptVersion string
	if err := pool.QueryRow(ctx, `SELECT scorecard_version FROM ai_interaction_scorecard_answers WHERE interaction_id = $1 AND source = 'judge'`, previous).Scan(&keptVersion); err != nil || keptVersion != "typesafe-scorecard-v6" {
		t.Fatalf("a backfill keeps the judge rows of a call the previous jury scored: %q %v", keptVersion, err)
	}
}
