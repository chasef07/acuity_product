package interaction

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestAnalyticsDistributionsCoverFullScopedWindow(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	var practice, otherPractice, location, otherLocation, foreignLocation, emptyLocation string
	for i, target := range []*string{&practice, &otherPractice} {
		if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES($1,'Synthetic') RETURNING id::text`, fmt.Sprint("distribution-", i)).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	for i, target := range []*string{&location, &otherLocation, &foreignLocation, &emptyLocation} {
		owner := practice
		if i == 2 {
			owner = otherPractice
		}
		if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,$2,'Synthetic') RETURNING id::text`, owner, fmt.Sprint(i)).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES('distribution@example.test','distribution')`); err != nil {
		t.Fatal(err)
	}
	insert := func(owner, office, source string, started time.Time, intent, outcome string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage) VALUES('agent',$1,$2,$3,'+15555550101','+15555550102',$4,$4,'COMPLETED',3) RETURNING id::text`, owner, office, source, started).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if intent != "" || outcome != "" {
			if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_classifications(interaction_id,intent,outcome,status) VALUES($1,NULLIF($2,''),NULLIF($3,''),'failed') ON CONFLICT(interaction_id) DO UPDATE SET intent=EXCLUDED.intent,outcome=EXCLUDED.outcome,status=EXCLUDED.status`, id, intent, outcome); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	answer := func(id, question, version, source string, value bool) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_scorecard_answers(interaction_id,practice_id,question,scorecard_version,source,answer,judge_model,computed_at) SELECT id,practice_id,$2,$3,$4,$5,CASE WHEN $4='judge' THEN 'synthetic' ELSE NULL END,$6 FROM ai_interactions WHERE id=$1`, id, question, version, source, value, now); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 60; i++ {
		insert(practice, location, fmt.Sprint("unlabeled-", i), now.Add(-48*time.Hour), "", "")
	}
	first := insert(practice, location, "first", now.Add(-time.Minute), "office_information", "resolved")
	second := insert(practice, location, "second", now.Add(-2*time.Minute), "office_information", "resolved")
	partial := insert(practice, location, "partial-intent", now.Add(-3*time.Minute), "insurance_question", "")
	contextual := insert(practice, location, "partial-outcome", now.Add(-4*time.Minute), "", "transferred")
	unknown := insert(practice, location, "unknown-version", now.Add(-5*time.Minute), "", "")
	answer(first, "office_question_answered", ScorecardJudgeVersion, "judge", false)
	answer(first, "call_ended_correctly", ScorecardJudgeVersion, "judge", false)
	answer(first, "office_rules_grounded", previousJudgeVersion, "judge", false)
	answer(second, "office_question_answered", legacyJudgeVersion, "judge", false)
	answer(partial, "office_question_answered", ScorecardJudgeVersion, "judge", true)
	answer(contextual, "new_booking_requested", legacyJudgeVersion, "judge", false)
	answer(contextual, "office_rules_grounded", ScorecardJudgeVersion, "judge", false)
	answer(contextual, "transfer_not_refused", ScorecardCodeVersion, "code", false)
	answer(unknown, "office_question_answered", "unknown-version", "judge", false)
	for i, excluded := range []struct {
		practice, location string
		at                 time.Time
	}{{practice, otherLocation, now}, {otherPractice, foreignLocation, now}, {practice, location, now.Add(-8 * 24 * time.Hour)}, {practice, location, now.Add(time.Second)}} {
		id := insert(excluded.practice, excluded.location, fmt.Sprint("excluded-", i), excluded.at, "billing_question", "abandoned")
		answer(id, "office_question_answered", ScorecardJudgeVersion, "judge", false)
	}
	module := New(pool, access.New(pool, func() time.Time { return now }), func() time.Time { return now })
	cmd := QueryAnalyticsCommand{Identity: access.Identity{Subject: "distribution", Email: "distribution@example.test", EmailVerified: true}, PracticeID: practice, LocationID: location, Range: AnalyticsRange7Days, Limit: 50}
	page, err := module.QueryAnalytics(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if page.Summary == nil || page.Summary.TotalCalls != 65 || len(page.Calls) != 50 || page.NextCursor == "" {
		t.Fatalf("full window/pagination: %+v", page)
	}
	d := page.Summary.Distributions
	if d.UnlabeledIntentCalls != 62 || d.UnlabeledOutcomeCalls != 62 || d.EvaluatedCalls != 3 || d.UnevaluatedCalls != 62 || d.FailedCalls != 2 {
		t.Fatalf("distribution coverage: %+v", d)
	}
	if len(d.Intents) != 2 || d.Intents[0].Intent != "office_information" || d.Intents[0].Calls != 2 || d.Intents[1].Intent != "insurance_question" || d.Intents[1].Calls != 1 {
		t.Fatalf("intents: %+v", d.Intents)
	}
	if len(d.Outcomes) != 2 || d.Outcomes[0].Outcome != "resolved" || d.Outcomes[0].Calls != 2 || d.Outcomes[1].Outcome != "transferred" || d.Outcomes[1].Calls != 1 {
		t.Fatalf("outcomes: %+v", d.Outcomes)
	}
	if len(d.Failures) != 3 || d.Failures[0].Key != "office_question_answered" || d.Failures[0].Calls != 2 || d.Failures[1].Key != "call_ended_correctly" || d.Failures[1].Calls != 1 || d.Failures[2].Key != "office_rules_grounded" || d.Failures[2].Calls != 1 {
		t.Fatalf("failure categories: %+v", d.Failures)
	}
	if len(d.Daily) != len(page.Summary.Daily) {
		t.Fatal("distribution days must match summary days")
	}
	totals := newDistributionAccumulator()
	for i, day := range d.Daily {
		if day.Date != page.Summary.Daily[i].Date {
			t.Fatal("daily dates out of order")
		}
		total := day.EvaluatedCalls + day.UnevaluatedCalls
		if total != page.Summary.Daily[i].TotalCalls {
			t.Fatalf("daily call coverage mismatch: %+v", day)
		}
		intents, outcomes := day.UnlabeledIntentCalls, day.UnlabeledOutcomeCalls
		for _, row := range day.Intents {
			intents += row.Calls
			totals.intents[row.Intent] += row.Calls
		}
		for _, row := range day.Outcomes {
			outcomes += row.Calls
			totals.outcomes[row.Outcome] += row.Calls
		}
		for _, row := range day.Failures {
			totals.failures[row.Key] += row.Calls
		}
		if intents != total || outcomes != total {
			t.Fatalf("daily labels do not cover every call: %+v", day)
		}
		totals.summary.UnlabeledIntentCalls += day.UnlabeledIntentCalls
		totals.summary.UnlabeledOutcomeCalls += day.UnlabeledOutcomeCalls
		totals.summary.EvaluatedCalls += day.EvaluatedCalls
		totals.summary.FailedCalls += day.FailedCalls
		totals.summary.UnevaluatedCalls += day.UnevaluatedCalls
		if total == 0 && (day.Intents == nil || day.Outcomes == nil || day.Failures == nil) {
			t.Fatal("empty dates must have empty arrays")
		}
	}
	if !reflect.DeepEqual(totals.finish(), d.AnalyticsDistributionCounts) {
		t.Fatal("daily distribution counts do not sum to aggregate")
	}
	for _, failure := range d.Failures {
		if failure.Label == "" {
			t.Fatal("missing failure label")
		}
		filtered := cmd
		filtered.FailingJudge = failure.Key
		result, err := module.QueryAnalytics(ctx, filtered)
		if err != nil || len(result.Calls) != failure.Calls {
			t.Fatalf("failure bucket disagrees with Calls filter: %+v %v", result, err)
		}
		if !reflect.DeepEqual(result.Summary, page.Summary) {
			t.Fatal("table filter changed full-window summary")
		}
	}
	filtered := cmd
	filtered.FailuresOnly = true
	result, err := module.QueryAnalytics(ctx, filtered)
	if err != nil || len(result.Calls) != d.FailedCalls {
		t.Fatalf("failed calls disagree with Calls filter: %+v %v", result, err)
	}
	emptyCommand := cmd
	emptyCommand.LocationID = emptyLocation
	empty, err := module.QueryAnalytics(ctx, emptyCommand)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Summary.TotalCalls != 0 || !reflect.DeepEqual(empty.Summary.Distributions.AnalyticsDistributionCounts, AnalyticsDistributionCounts{Intents: []AnalyticsIntentCount{}, Outcomes: []AnalyticsOutcomeCount{}, Failures: []AnalyticsFailureCount{}}) {
		t.Fatalf("empty window: %+v", empty.Summary)
	}
	cmd.Cursor = page.NextCursor
	next, err := module.QueryAnalytics(ctx, cmd)
	if err != nil || next.Summary != nil || len(next.Calls) != 15 {
		t.Fatalf("continuation: %+v %v", next, err)
	}
}
