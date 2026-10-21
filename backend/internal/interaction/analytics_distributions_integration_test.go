package interaction

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestAnalyticsDistributionsCoverFullScopedWindow(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
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
	module := New(db.As(testdb.Portal), access.New(db.As(testdb.Portal), func() time.Time { return now }), func() time.Time { return now })
	cmd := QueryAnalyticsCommand{Identity: access.Identity{Subject: "distribution", Email: "distribution@example.test", EmailVerified: true}, PracticeID: practice, LocationID: location, Range: AnalyticsRange7Days, Limit: 50}
	page, err := module.QueryAnalytics(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if page.Summary == nil || page.Summary.TotalCalls != 65 || len(page.Calls) != 50 || page.NextCursor == "" {
		t.Fatalf("full window/pagination: %+v", page)
	}
	d := page.Summary.Distributions
	if d.TotalCalls != 65 || d.UnlabeledIntentCalls != 62 || d.UnlabeledOutcomeCalls != 62 || d.EvaluatedCalls != 3 || d.UnevaluatedCalls != 62 || d.FailedCalls != 2 {
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
	pathCalls, routes := 0, map[string]int{}
	for _, path := range d.Paths {
		pathCalls += path.Calls
		routes[pathKey(path)] = path.Calls
	}
	if pathCalls != d.TotalCalls ||
		routes["office_information|resolved|true|call_ended_correctly,office_question_answered,office_rules_grounded"] != 1 ||
		routes["office_information|resolved|true|office_question_answered"] != 1 ||
		routes["insurance_question||true|"] != 1 || routes["||false|"] != 61 {
		t.Fatalf("paths: %d calls, routes %v", pathCalls, routes)
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
		if total != day.TotalCalls || total != page.Summary.Daily[i].TotalCalls {
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
		totals.summary.TotalCalls += day.TotalCalls
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
	judgeCalls := map[string]int{}
	for _, path := range d.Paths {
		switch {
		case !path.Evaluated:
			judgeCalls[JudgeResultUnjudged] += path.Calls
		case len(path.Failures) == 0:
			judgeCalls[JudgeResultPass] += path.Calls
		}
	}
	for _, judge := range []string{JudgeResultPass, JudgeResultUnjudged} {
		filtered := cmd
		filtered.JudgeResult = judge
		filtered.Limit = 100
		result, err := module.QueryAnalytics(ctx, filtered)
		if err != nil || len(result.Calls) != judgeCalls[judge] || judgeCalls[judge] == 0 {
			t.Fatalf("%s calls disagree with paths (%d): %d %v", judge, judgeCalls[judge], len(result.Calls), err)
		}
		if !reflect.DeepEqual(result.Summary, page.Summary) {
			t.Fatalf("%s filter changed full-window summary", judge)
		}
	}
	for _, invalid := range []QueryAnalyticsCommand{
		func() QueryAnalyticsCommand { c := cmd; c.JudgeResult = "maybe"; return c }(),
		func() QueryAnalyticsCommand {
			c := cmd
			c.JudgeResult = JudgeResultPass
			c.FailuresOnly = true
			return c
		}(),
		func() QueryAnalyticsCommand {
			c := cmd
			c.JudgeResult = JudgeResultUnjudged
			c.FailingJudge = "office_question_answered"
			return c
		}(),
	} {
		if _, err := module.QueryAnalytics(ctx, invalid); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("judge result %q with failure filters must be invalid: %v", invalid.JudgeResult, err)
		}
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
	for i := 0; i < 60; i++ {
		outcome := "resolved"
		if i%2 == 1 {
			outcome = "transferred"
		}
		insert(practice, location, fmt.Sprint("drill-", i), now.Add(-24*time.Hour), "office_information", outcome)
	}
	cmd.Cursor = ""
	baseline, err := module.QueryAnalytics(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Summary.TotalCalls != 125 {
		t.Fatalf("drill baseline: %+v", baseline.Summary)
	}
	for _, test := range []struct {
		name, intent, outcome                                       string
		total, evaluated, failed, unlabeledIntent, unlabeledOutcome int
		outcomes                                                    []AnalyticsOutcomeCount
	}{
		{name: "intent spans more than one page", intent: "office_information", total: 62, evaluated: 2, failed: 2, outcomes: []AnalyticsOutcomeCount{{Outcome: "resolved", Calls: 32}, {Outcome: "transferred", Calls: 30}}},
		{name: "intent and outcome retain only matching failures", intent: "office_information", outcome: "resolved", total: 32, evaluated: 2, failed: 2, outcomes: []AnalyticsOutcomeCount{{Outcome: "resolved", Calls: 32}}},
		{name: "other outcome excludes failing calls", intent: "office_information", outcome: "transferred", total: 30, outcomes: []AnalyticsOutcomeCount{{Outcome: "transferred", Calls: 30}}},
		{name: "partial intent retains missing outcome", intent: "insurance_question", total: 1, evaluated: 1, unlabeledOutcome: 1, outcomes: []AnalyticsOutcomeCount{}},
		{name: "partial outcome retains missing intent", outcome: "transferred", total: 31, unlabeledIntent: 1, outcomes: []AnalyticsOutcomeCount{{Outcome: "transferred", Calls: 31}}},
		{name: "missing outcome cannot match", intent: "insurance_question", outcome: "resolved", outcomes: []AnalyticsOutcomeCount{}},
		{name: "no matching intent", intent: "prescription_or_refill", outcomes: []AnalyticsOutcomeCount{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			filtered := cmd
			filtered.Intent, filtered.Outcome = test.intent, test.outcome
			result, err := module.QueryAnalytics(ctx, filtered)
			if err != nil {
				t.Fatal(err)
			}
			counts := result.Summary.Distributions
			if counts.TotalCalls != test.total || counts.EvaluatedCalls+counts.UnevaluatedCalls != test.total || counts.EvaluatedCalls != test.evaluated || counts.FailedCalls != test.failed || counts.UnlabeledIntentCalls != test.unlabeledIntent || counts.UnlabeledOutcomeCalls != test.unlabeledOutcome || !reflect.DeepEqual(counts.Outcomes, test.outcomes) {
				t.Fatalf("drill distribution: %+v", counts)
			}
			if test.failed == 0 && len(counts.Failures) != 0 {
				t.Fatalf("unrelated failures leaked: %+v", counts.Failures)
			}
			if test.failed == 2 && !reflect.DeepEqual(counts.Failures, d.Failures) {
				t.Fatalf("scoped failures: %+v", counts.Failures)
			}
			global := *result.Summary
			global.Distributions = baseline.Summary.Distributions
			if !reflect.DeepEqual(&global, baseline.Summary) {
				t.Fatal("drill filters changed global summary")
			}
			if len(result.Calls) != min(test.total, cmd.Limit) {
				t.Fatalf("calls did not use same scope: %d", len(result.Calls))
			}
			if (result.NextCursor != "") != (test.total > cmd.Limit) {
				t.Fatal("filtered pagination differs from distribution total")
			}
			dailyTotal, dailyEvaluated, dailyFailed, missingIntent, missingOutcome := 0, 0, 0, 0, 0
			dailyIntents, dailyOutcomes, dailyFailures := map[CallIntent]int{}, map[CallOutcome]int{}, map[string]int{}
			if len(counts.Daily) != len(baseline.Summary.Daily) {
				t.Fatal("filtered daily dates missing")
			}
			for i, day := range counts.Daily {
				if day.Date != baseline.Summary.Daily[i].Date {
					t.Fatal("filtered dates out of order")
				}
				if day.TotalCalls != day.EvaluatedCalls+day.UnevaluatedCalls {
					t.Fatal("daily denominator differs from coverage")
				}
				dailyTotal += day.TotalCalls
				dailyEvaluated += day.EvaluatedCalls
				dailyFailed += day.FailedCalls
				missingIntent += day.UnlabeledIntentCalls
				missingOutcome += day.UnlabeledOutcomeCalls
				for _, row := range day.Intents {
					dailyIntents[row.Intent] += row.Calls
				}
				for _, row := range day.Outcomes {
					dailyOutcomes[row.Outcome] += row.Calls
				}
				for _, row := range day.Failures {
					dailyFailures[row.Key] += row.Calls
				}
			}
			if dailyTotal != test.total || dailyEvaluated != test.evaluated || dailyFailed != test.failed || missingIntent != test.unlabeledIntent || missingOutcome != test.unlabeledOutcome {
				t.Fatal("filtered daily coverage does not reconcile")
			}
			for _, row := range counts.Intents {
				if dailyIntents[row.Intent] != row.Calls {
					t.Fatal("daily intents do not reconcile")
				}
				delete(dailyIntents, row.Intent)
			}
			for _, row := range counts.Outcomes {
				if dailyOutcomes[row.Outcome] != row.Calls {
					t.Fatal("daily outcomes do not reconcile")
				}
				delete(dailyOutcomes, row.Outcome)
			}
			for _, row := range counts.Failures {
				if dailyFailures[row.Key] != row.Calls {
					t.Fatal("daily failures do not reconcile")
				}
				delete(dailyFailures, row.Key)
			}
			if len(dailyIntents)+len(dailyOutcomes)+len(dailyFailures) != 0 {
				t.Fatal("daily categories absent from aggregate")
			}
			for _, key := range []string{"", "office_question_answered"} {
				failedFilter := filtered
				failedFilter.FailuresOnly = true
				failedFilter.FailingJudge = key
				failedPage, err := module.QueryAnalytics(ctx, failedFilter)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(failedPage.Summary.Distributions, counts) {
					t.Fatal("failure filters changed distribution scope")
				}
			}
		})
	}

}
